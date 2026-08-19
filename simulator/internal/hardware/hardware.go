// Package hardware loads the NGE console/hardware catalog — the consoles,
// handhelds and home computers NGE sold — from seed_data/hardware.tsv and
// answers "which models of platform P are sellable in country C as of date
// D" for the V1.21 hardware-sales generator. Deliberately stdlib-only and
// modelled on internal/catalog so the two seed pipelines stay parallel.
package hardware

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Model is one row of seed_data/hardware.tsv — a single hardware MODEL (a
// base console/handheld/computer or a later revision). Zero-value dates and
// 0 numerics mean "null" ("-" in the TSV).
type Model struct {
	HardwareID   int64
	ModelName    string
	Platform     string // matches dbo.platforms.name exactly
	Kind         string // console|handheld|computer|accessory
	Manufacturer string
	ModelNumber  string
	ReleaseNA    time.Time
	ReleaseJP    time.Time
	ReleaseEU    time.Time
	LaunchUSD    float64
	RevisionOf   int64 // 0 = base model
	SourceURL    string
	Notes        string

	// FirstRelease is the earliest non-zero regional launch date — the
	// launch anchor used for launch-curve timing and model-progression.
	FirstRelease time.Time
}

// Index holds the loaded hardware catalog with per-platform lookup.
type Index struct {
	all        []Model
	byPlatform map[string][]Model // each slice sorted ascending by FirstRelease
	byID       map[int64]Model
}

// Load parses seed_data/hardware.tsv into an Index. Columns (tab-separated):
// hardware_id, model_name, platform, kind, manufacturer, model_number,
// release_na, release_jp, release_eu, launch_usd, revision_of, source_url,
// notes. "-" / "" / parse errors become null (zero values).
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open hardware tsv: %w", err)
	}
	defer f.Close()

	idx := &Index{byID: make(map[int64]Model, 128)}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue // header
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 11 {
			continue
		}
		id, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		na := parseHWDate(fields[6])
		jp := parseHWDate(fields[7])
		eu := parseHWDate(fields[8])
		m := Model{
			HardwareID:   id,
			ModelName:    fields[1],
			Platform:     fields[2],
			Kind:         fields[3],
			Manufacturer: dashEmpty(fieldAt(fields, 4)),
			ModelNumber:  dashEmpty(fieldAt(fields, 5)),
			ReleaseNA:    na,
			ReleaseJP:    jp,
			ReleaseEU:    eu,
			LaunchUSD:    parseHWFloat(fields[9]),
			RevisionOf:   parseHWInt(fields[10]),
			SourceURL:    fieldAt(fields, 11),
			Notes:        fieldAt(fields, 12),
			FirstRelease: earliestNonZero(na, jp, eu),
		}
		idx.all = append(idx.all, m)
		idx.byID[id] = m
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read hardware tsv: %w", err)
	}
	idx.byPlatform = make(map[string][]Model, 64)
	for _, m := range idx.all {
		idx.byPlatform[m.Platform] = append(idx.byPlatform[m.Platform], m)
	}
	for name, rels := range idx.byPlatform {
		sort.SliceStable(rels, func(i, j int) bool {
			return rels[i].FirstRelease.Before(rels[j].FirstRelease)
		})
		idx.byPlatform[name] = rels
	}
	return idx, nil
}

func (idx *Index) Count() int   { return len(idx.all) }
func (idx *Index) All() []Model { return idx.all }

func (idx *Index) ByID(id int64) (Model, bool) {
	m, ok := idx.byID[id]
	return m, ok
}

// Platforms returns the distinct platform names with hardware, sorted.
func (idx *Index) Platforms() []string {
	out := make([]string, 0, len(idx.byPlatform))
	for name := range idx.byPlatform {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ModelsForPlatform returns a platform's models, sorted ascending by launch.
func (idx *Index) ModelsForPlatform(platform string) []Model {
	return idx.byPlatform[platform]
}

// AvailableModels returns the models of `platform` whose region-appropriate
// launch date has passed by `asOf`, for a shop in `country`. A model with no
// date for the country's region falls back to its earliest regional launch
// (FirstRelease), so a single-region model still sells elsewhere (the demand
// generator damps it by RegionBias).
func (idx *Index) AvailableModels(platform, country string, asOf time.Time) []Model {
	rels := idx.byPlatform[platform]
	if len(rels) == 0 {
		return nil
	}
	out := make([]Model, 0, len(rels))
	for _, m := range rels {
		d := idx.RegionDate(m, country)
		if d.IsZero() || d.After(asOf) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// RegionDate picks the launch date for the shop country's region, with
// FirstRelease fallback.
func (idx *Index) RegionDate(m Model, country string) time.Time {
	switch regionForCountry(country) {
	case "NA":
		if !m.ReleaseNA.IsZero() {
			return m.ReleaseNA
		}
	case "JP":
		if !m.ReleaseJP.IsZero() {
			return m.ReleaseJP
		}
	default: // EU / rest-of-world (PAL)
		if !m.ReleaseEU.IsZero() {
			return m.ReleaseEU
		}
	}
	return m.FirstRelease
}

// PlatformLaunch returns the earliest FirstRelease across a platform's
// models — the platform's launch anchor for the launch-shape curve.
func (idx *Index) PlatformLaunch(platform string) time.Time {
	rels := idx.byPlatform[platform]
	if len(rels) == 0 {
		return time.Time{}
	}
	return rels[0].FirstRelease // byPlatform is sorted by FirstRelease asc
}

// regionForCountry maps a shop country to a hardware launch region.
func regionForCountry(country string) string {
	switch country {
	case "US", "CA":
		return "NA"
	case "JP", "KR":
		return "JP"
	default: // GB/DE/FR/IT/ES/NL/CH/PL/SE/CZ/DK/NO/AU/BR → PAL/general
		return "EU"
	}
}

// ---- TSV parse helpers ("-" / "" / errors → null) ----

func parseHWDate(s string) time.Time {
	if s == "" || s == "-" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseHWFloat(s string) float64 {
	if s == "" || s == "-" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseHWInt(s string) int64 {
	if s == "" || s == "-" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func dashEmpty(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func fieldAt(fields []string, i int) string {
	if i < len(fields) {
		return fields[i]
	}
	return ""
}

func earliestNonZero(ts ...time.Time) time.Time {
	var best time.Time
	for _, t := range ts {
		if t.IsZero() {
			continue
		}
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	return best
}
