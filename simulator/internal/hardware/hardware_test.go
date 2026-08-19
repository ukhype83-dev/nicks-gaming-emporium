package hardware

import (
	"testing"
	"time"
)

func loadTestIndex(t *testing.T) *Index {
	t.Helper()
	idx, err := Load("../../../seed_data/hardware.tsv")
	if err != nil {
		t.Skipf("hardware.tsv not loadable from test cwd: %v", err)
	}
	return idx
}

func TestLoadCountAndNulls(t *testing.T) {
	idx := loadTestIndex(t)
	if idx.Count() != 118 {
		t.Errorf("Count() = %d, want 118", idx.Count())
	}
	// Xbox (id 31) has model_number "-" in the TSV → empty string.
	m, ok := idx.ByID(31)
	if !ok {
		t.Fatal("hardware_id 31 (Xbox) missing")
	}
	if m.ModelNumber != "" {
		t.Errorf("Xbox model_number = %q, want empty (was '-')", m.ModelNumber)
	}
	if m.Platform != "Xbox" || m.Kind != "console" {
		t.Errorf("Xbox row mismatched: platform=%q kind=%q", m.Platform, m.Kind)
	}
}

func TestByPlatformDateSorted(t *testing.T) {
	idx := loadTestIndex(t)
	ps2 := idx.ModelsForPlatform("PlayStation 2")
	if len(ps2) < 2 {
		t.Fatalf("PlayStation 2 has %d models, want >=2", len(ps2))
	}
	for i := 1; i < len(ps2); i++ {
		if ps2[i].FirstRelease.Before(ps2[i-1].FirstRelease) {
			t.Errorf("PS2 models not launch-sorted: %s before %s", ps2[i].ModelName, ps2[i-1].ModelName)
		}
	}
	if ps2[0].RevisionOf != 0 {
		t.Errorf("first PS2 model %q should be the base (revision_of 0), got %d", ps2[0].ModelName, ps2[0].RevisionOf)
	}
}

func TestAvailableModelsGating(t *testing.T) {
	idx := loadTestIndex(t)
	has := func(models []Model, id int64) bool {
		for _, m := range models {
			if m.HardwareID == id {
				return true
			}
		}
		return false
	}
	d2001 := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	d2006 := time.Date(2006, 1, 1, 0, 0, 0, 0, time.UTC)
	a01 := idx.AvailableModels("PlayStation 2", "US", d2001)
	a06 := idx.AvailableModels("PlayStation 2", "US", d2006)
	if !has(a01, 3) {
		t.Error("PS2 fat (id 3) should be available in US 2001")
	}
	if has(a01, 4) {
		t.Error("PS2 Slim (id 4) should NOT be available in US 2001 (launched 2004)")
	}
	if !has(a06, 4) {
		t.Error("PS2 Slim (id 4) should be available in US 2006")
	}
}

func TestPlatformLaunchAnchor(t *testing.T) {
	idx := loadTestIndex(t)
	// PS2's launch anchor is the fat model's earliest regional date (JP 2000-03).
	launch := idx.PlatformLaunch("PlayStation 2")
	if launch.Year() != 2000 {
		t.Errorf("PlayStation 2 launch anchor year = %d, want 2000", launch.Year())
	}
}
