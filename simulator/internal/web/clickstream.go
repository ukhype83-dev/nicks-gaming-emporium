// The surviving web-server logs (web.page_views, design §5). Survival is the
// realism: nothing before 2004-06, a 1-in-10 sample through the legacy stack's
// life (2004-06 → 2007-09, "incomplete event capture"), full fidelity after
// the 2007-10 migration. Traffic tracks the business — ramp, ~2010-11 peak,
// decline, and a Sept-2016 death-rattle when the crawlers outnumber the
// mourners. Sessions are NOT stored (deriving them is the gaps-and-islands
// lab); session_id is the log server's own stamp to validate against.
package web

import (
	"math/rand/v2"
	"sort"
	"time"

	"emporium/internal/rng"
)

// PageViewRecord is one web.page_views row.
type PageViewRecord struct {
	PageViewID      int64
	SessionID       int64
	AccountID       int64 // 0 → not logged in (NULL)
	OccurredAt      time.Time
	URLPath         string
	HTTPStatus      int
	ReferrerDomain  string // "" → direct (NULL)
	UserAgentFamily string
	ClientCountry   string
	BytesSent       int
	SourceSystem    string
}

var clickstreamStart = time.Date(2004, 6, 1, 0, 0, 0, 0, time.UTC)

// EmitClickstream generates page views day by day across the web era. scale
// sizes the daily volume to the tier (the 3T baseline curve × scale). pvBase
// and sessionBase start the id counters. emit receives every row.
func (e *Emitter) EmitClickstream(scale float64, pvBase, sessionBase int64, emit func(PageViewRecord)) (int64, int64) {
	return e.EmitClickstreamShard(scale, 0, 1, pvBase, sessionBase, emit)
}

// EmitClickstreamShard generates the clickstream for the day-shard
// {d : dayIndex % shardCount == shardIdx}. Days are independent — each derives
// its own RNG stream ("web/clickstream/<date>") — so a worker reproduces its
// days identically regardless of the others. Each worker takes a disjoint
// pvBase/sessionBase range so page_view_id stays unique and sessions never
// merge across workers. shardCount==1 (shardIdx 0) is the whole stream.
func (e *Emitter) EmitClickstreamShard(scale float64, shardIdx, shardCount int, pvBase, sessionBase int64, emit func(PageViewRecord)) (int64, int64) {
	if shardCount < 1 {
		shardCount = 1
	}
	if pvBase == 0 {
		pvBase = 1
	}
	if sessionBase == 0 {
		sessionBase = 1
	}
	pvID, sessionID := pvBase, sessionBase
	e.ensureTrafficReleases()

	dayIdx := -1
	for day := clickstreamStart; !day.After(SiteDark); day = day.AddDate(0, 0, 1) {
		dayIdx++
		if dayIdx%shardCount != shardIdx {
			continue // another worker owns this day
		}
		surv := survivalRate(day)
		if surv == 0 {
			continue
		}
		rr := rng.Derive(e.seed, "web/clickstream/"+day.Format("2006-01-02"))
		target := int(basePerDay(day) * scale * surv)
		if target <= 0 {
			continue
		}
		emitted := 0
		for emitted < target {
			isBot := rr.Float64() < botShare(day)
			country := trafficCountry(rr)
			ua := userAgent(rr, day.Year(), isBot)
			ref := referrer(rr, day.Year(), isBot)
			var acctID int64
			if !isBot && rr.Float64() < loggedInShare(day.Year()) {
				if a, ok := e.drawVisitor(rr, day); ok {
					acctID = a.id
					country = a.country // logged-in visitors carry their real country
				}
			}
			views := 1 + rr.IntN(8)
			if isBot {
				views = 3 + rr.IntN(25) // crawlers sweep deep
			}
			secOfDay := rr.IntN(86400)
			for v := 0; v < views; v++ {
				at := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC).
					Add(time.Duration(secOfDay) * time.Second)
				path := e.trafficPath(rr, isBot)
				status := httpStatus(rr, day)
				emit(PageViewRecord{
					PageViewID: pvID, SessionID: sessionID, AccountID: acctID,
					OccurredAt: at, URLPath: path, HTTPStatus: status,
					ReferrerDomain: ref, UserAgentFamily: ua, ClientCountry: country,
					BytesSent:    800 + rr.IntN(48000),
					SourceSystem: webClickSource(day),
				})
				pvID++
				emitted++
				secOfDay += 5 + rr.IntN(180) // seconds between page loads
				if secOfDay >= 86400 {
					break
				}
				ref = "" // subsequent hits in a session are internal
			}
			sessionID++
		}
	}
	return pvID, sessionID
}

// survivalRate: what fraction of a day's traffic survives in the archive.
func survivalRate(day time.Time) float64 {
	switch {
	case day.Before(clickstreamStart):
		return 0
	case day.Before(time.Date(2007, 10, 1, 0, 0, 0, 0, time.UTC)):
		return 0.10 // legacy stack: 1-in-10 sampled export
	default:
		return 1.0
	}
}

// basePerDay is the 3T-scale daily page-view baseline: a ramp to a 2010-11
// peak, decline, and a September-2016 death-rattle.
func basePerDay(day time.Time) float64 {
	var base float64
	switch day.Year() {
	case 2004:
		base = 80000
	case 2005:
		base = 200000
	case 2006:
		base = 400000
	case 2007:
		base = 650000
	case 2008:
		base = 900000
	case 2009:
		base = 1100000
	case 2010:
		base = 1300000
	case 2011:
		base = 1200000
	case 2012:
		base = 1000000
	case 2013:
		base = 800000
	case 2014:
		base = 600000
	case 2015:
		base = 460000
	case 2016:
		base = 400000
	}
	// Holiday lift (Nov-Dec) and the final-month death-rattle (everyone came
	// to watch it die).
	switch day.Month() {
	case time.November, time.December:
		base *= 1.4
	case time.September:
		if day.Year() == 2016 {
			base *= 3.0
		}
	}
	return base
}

func botShare(day time.Time) float64 {
	if day.Year() >= 2016 && day.Month() == time.September {
		return 0.60 // the last visitors were mostly robots
	}
	if day.Before(time.Date(2008, 1, 1, 0, 0, 0, 0, time.UTC)) {
		return 0.08
	}
	return 0.20
}

func loggedInShare(year int) float64 {
	switch {
	case year <= 2005:
		return 0.12
	case year <= 2009:
		return 0.25
	case year <= 2013:
		return 0.42
	default:
		return 0.55
	}
}

// drawVisitor samples a logged-in account that existed on `day`.
func (e *Emitter) drawVisitor(rr *rand.Rand, day time.Time) (acctRef, bool) {
	n := len(e.accountList)
	if n == 0 {
		return acctRef{}, false
	}
	for try := 0; try < 6; try++ {
		a := e.accountList[rr.IntN(n)]
		if a.created.After(day) {
			continue
		}
		return a, true
	}
	return acctRef{}, false
}

// trafficPath builds a URL. Bots sweep product pages; humans browse a wider
// mix. Product paths reference real release_ids (joinable to the catalog).
func (e *Emitter) trafficPath(rr *rand.Rand, isBot bool) string {
	if isBot || rr.Float64() < 0.62 {
		return "/product/" + itoa(e.sampleTrafficRelease(rr))
	}
	switch rr.IntN(10) {
	case 0, 1:
		return "/reviews/" + itoa(e.sampleTrafficRelease(rr))
	case 2, 3:
		return "/search"
	case 4:
		return "/cart"
	case 5:
		return "/checkout"
	case 6:
		return "/account"
	case 7:
		return "/deals"
	case 8:
		return "/"
	default:
		return "/platform/browse"
	}
}

func httpStatus(rr *rand.Rand, day time.Time) int {
	// The migration week (2007-10-01..07) throws 404/500s.
	migrating := day.Year() == 2007 && day.Month() == time.October && day.Day() <= 7
	x := rr.Float64()
	if migrating {
		switch {
		case x < 0.55:
			return 200
		case x < 0.80:
			return 404
		case x < 0.93:
			return 500
		default:
			return 301
		}
	}
	switch {
	case x < 0.94:
		return 200
	case x < 0.965:
		return 301
	case x < 0.99:
		return 404
	default:
		return 500
	}
}

func userAgent(rr *rand.Rand, year int, isBot bool) string {
	if isBot {
		return pickN(rr, "Googlebot", "bingbot", "YahooSlurp", "crawler")
	}
	switch {
	case year <= 2006:
		return pickN(rr, "IE6", "Netscape4", "Firefox1", "IE5")
	case year <= 2009:
		return pickN(rr, "IE7", "Firefox3", "IE6", "Safari3")
	case year <= 2013:
		return pickN(rr, "Chrome", "IE8", "Firefox", "Safari", "IE9")
	default:
		return pickN(rr, "Chrome", "Firefox", "Edge", "SafariMobile", "ChromeMobile")
	}
}

func referrer(rr *rand.Rand, year int, isBot bool) string {
	if isBot {
		return ""
	}
	if rr.Float64() < 0.30 {
		return "" // direct / bookmarked
	}
	switch {
	case year <= 2007:
		return pickN(rr, "gamefaqs.com", "altavista.com", "yahoo.com", "webring.org")
	case year <= 2011:
		return pickN(rr, "google.com", "gamefaqs.com", "ign.com", "youtube.com")
	default:
		return pickN(rr, "google.com", "reddit.com", "youtube.com", "facebook.com")
	}
}

func trafficCountry(rr *rand.Rand) string {
	// US-heavy, matching the estate.
	switch x := rr.Float64(); {
	case x < 0.55:
		return "US"
	case x < 0.68:
		return "GB"
	case x < 0.78:
		return "CA"
	case x < 0.86:
		return "AU"
	case x < 0.92:
		return "DE"
	case x < 0.97:
		return "FR"
	default:
		return "JP"
	}
}

func webClickSource(day time.Time) string {
	if day.Before(time.Date(2007, 10, 1, 0, 0, 0, 0, time.UTC)) {
		return "web_legacy_2001_2007"
	}
	return "web_2008_plus"
}

// sampleTrafficRelease returns a release_id weighted toward the famous
// (curated) titles — they pull most of the traffic.
func (e *Emitter) sampleTrafficRelease(rr *rand.Rand) int64 {
	if len(e.curatedReleaseIDs) > 0 && rr.Float64() < 0.6 {
		return e.curatedReleaseIDs[rr.IntN(len(e.curatedReleaseIDs))]
	}
	if len(e.allReleaseIDs) == 0 {
		return 0
	}
	return e.allReleaseIDs[rr.IntN(len(e.allReleaseIDs))]
}

// ensureTrafficReleases builds the release-id pools once.
func (e *Emitter) ensureTrafficReleases() {
	if len(e.curatedReleaseIDs) == 0 {
		e.buildCuratedReleaseIDs()
	}
	if len(e.allReleaseIDs) == 0 {
		ids := make([]int64, 0, len(e.releaseMeta))
		for id := range e.releaseMeta {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		e.allReleaseIDs = ids
	}
}
