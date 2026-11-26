package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/docstore"
	"github.com/shottah/lineup/api/internal/guide"
	"github.com/shottah/lineup/api/internal/prefs"
)

// eveningUS is the production report's clock: 02:18 UTC on Nov 26 is still
// the evening of Nov 25 for a US user.
var eveningUS = time.Date(2026, 11, 26, 2, 18, 0, 0, time.UTC)

// recordingIngester records every EnsureTitle call (guide generation
// refreshes each rotation title before hydrating).
type recordingIngester struct {
	mu    sync.Mutex
	calls []string // "kind/tmdbID/region"
}

func (f *recordingIngester) EnsureTitle(_ context.Context, kind string, tmdbID int64, region string) (*docstore.Title, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, kind+"/"+strconv.FormatInt(tmdbID, 10)+"/"+region)
	return &docstore.Title{}, nil
}

// schedServer is a guides server with a chosen clock, a pre-seeded user,
// and optional ingester.
func schedServer(t *testing.T, fg *fakeGuides, u *docstore.User, now time.Time, ing Ingester) http.Handler {
	t.Helper()
	users := newFakeUsers()
	if u != nil {
		users.byUID["uid-1"] = u
	}
	d := Deps{Users: users, Verifier: fakeVerifierWithTok1(), Guides: fg, Now: func() time.Time { return now }}
	if ing != nil {
		d.Ingest = ing
		d.Titles = &fakeTitleReader{}
	}
	return New(d).Handler
}

func userIn(tz string) *docstore.User {
	return &docstore.User{ID: "uid-1", Email: "one@example.com", DisplayName: "One",
		Region: "US", SchedulePrefs: prefs.Default(), Timezone: tz}
}

func TestCurrentGuideUsesUserTimezone(t *testing.T) {
	cases := []struct {
		tz, want string
	}{
		{"America/Chicago", "2026-11-25"},
		{"America/Los_Angeles", "2026-11-25"},
		{"", "2026-11-26"}, // unset: UTC
		{"Asia/Tokyo", "2026-11-26"},
	}
	for _, tc := range cases {
		fg := newFakeGuides()
		fg.currentResult = &docstore.Guide{ID: "g1", StartDate: "2026-11-25", EndDate: "2026-12-01", Items: []docstore.GuideItem{}}
		h := schedServer(t, fg, userIn(tc.tz), eveningUS, nil)
		if rec := do(t, h, http.MethodGet, "/v1/guides/current", "tok-1", ""); rec.Code != http.StatusOK {
			t.Fatalf("tz %q: current = %d (body %s)", tc.tz, rec.Code, rec.Body.String())
		}
		if fg.currentToday != tc.want {
			t.Fatalf("tz %q: CurrentGuide today = %q, want %q", tc.tz, fg.currentToday, tc.want)
		}
	}
}

// TestRegenerateUsesUserTimezone is the production regression: regenerate
// ran at 02:18 UTC on Nov 26, treated Nov 25 as the past, and left the
// user's actual tonight empty.
func TestRegenerateUsesUserTimezone(t *testing.T) {
	for _, tc := range []struct {
		tz        string
		wantNov25 bool
	}{{"America/Chicago", true}, {"", false}} {
		fg := newFakeGuides()
		fg.titles = fixtureTitles()
		fg.setGuide("uid-1", &docstore.Guide{ID: "g1", StartDate: "2026-11-25", EndDate: "2026-12-01", Items: []docstore.GuideItem{}})
		h := schedServer(t, fg, userIn(tc.tz), eveningUS, nil)
		if rec := do(t, h, http.MethodPost, "/v1/guides/g1/regenerate", "tok-1", ""); rec.Code != http.StatusOK {
			t.Fatalf("tz %q: regenerate = %d (body %s)", tc.tz, rec.Code, rec.Body.String())
		}
		got := false
		for _, it := range fg.replaceArgs.newItems {
			if it.Date == "2026-11-25" {
				got = true
			}
		}
		if got != tc.wantNov25 {
			t.Fatalf("tz %q: items on 2026-11-25 = %v, want %v", tc.tz, got, tc.wantNov25)
		}
	}
}

func TestPatchItemTodayUsesUserTimezone(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
		{ID: "i1", Date: "2026-11-25", StartMin: 1140, EndMin: 1170, TitleID: "series-1"},
	}})
	h := schedServer(t, fg, userIn("America/New_York"), eveningUS, nil)
	if rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"start_min":1200}`); rec.Code != http.StatusOK {
		t.Fatalf("patch = %d (body %s)", rec.Code, rec.Body.String())
	}
	if fg.updateArgs.Today != "2026-11-25" {
		t.Fatalf("update Today = %q, want 2026-11-25 (user's local date)", fg.updateArgs.Today)
	}
}

func TestPatchItemSwapCarriesOffer(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
		{ID: "i1", Date: "2026-11-27", StartMin: 1140, EndMin: 1170, TitleID: "series-1", ProviderID: 257, Offer: guide.OfferStream},
	}})
	fg.swapInfo["movie-79"] = &docstore.SwapInfo{TitleID: "movie-79", Kind: "movie", Runtime: 99, Providers: []int64{2, 10}, Offer: guide.OfferRent}
	h := schedServer(t, fg, userIn("America/Chicago"), eveningUS, nil)
	if rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"title_id":"movie-79"}`); rec.Code != http.StatusOK {
		t.Fatalf("swap = %d (body %s)", rec.Code, rec.Body.String())
	}
	if fg.updateArgs.SwapOffer != guide.OfferRent {
		t.Fatalf("SwapOffer = %q, want rent", fg.updateArgs.SwapOffer)
	}
}

// TestGuideResponseCarriesWindows: the web sizes the calendar axis from the
// viewing windows of the guide's days, so every guide response lists the
// enabled windows (from the user's current prefs) for its date range.
func TestGuideResponseCarriesWindows(t *testing.T) {
	const monOff = `{"windows":{` +
		`"mon":{"enabled":false,"start":"19:00","end":"23:00"},` +
		`"tue":{"enabled":true,"start":"18:30","end":"22:00"},` +
		`"wed":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"thu":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"fri":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"sat":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"sun":{"enabled":true,"start":"19:00","end":"23:00"}}}`
	u := userIn("")
	u.SchedulePrefs = json.RawMessage(monOff)
	fg := newFakeGuides()
	// 2026-11-30 is a Monday (disabled), 2026-12-01 a Tuesday.
	fg.currentResult = &docstore.Guide{ID: "g1", StartDate: "2026-11-29", EndDate: "2026-12-01", Items: []docstore.GuideItem{}}
	h := schedServer(t, fg, u, eveningUS, nil)
	rec := do(t, h, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current = %d (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Windows []struct {
			Date     string `json:"date"`
			StartMin int    `json:"start_min"`
			EndMin   int    `json:"end_min"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	type w struct {
		Date       string
		Start, End int
	}
	var got []w
	for _, x := range body.Windows {
		got = append(got, w{x.Date, x.StartMin, x.EndMin})
	}
	want := []w{{"2026-11-29", 1140, 1380}, {"2026-12-01", 1110, 1320}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("windows = %+v, want %+v", got, want)
	}
}

// TestGenerateRefreshesRotationTitles: guide generation runs each rotation
// title through ingest first, so provider data (including the rent/buy
// fallback for docs ingested flatrate-only) is current before scheduling.
func TestGenerateRefreshesRotationTitles(t *testing.T) {
	fg := newFakeGuides()
	fg.titles = []guide.Title{
		{ID: "movie-79", Kind: "movie", Runtime: 99},
		{ID: "series-61695", Kind: "series", Runtime: 24, Providers: []int64{257}, Offer: guide.OfferStream,
			Pointer: guide.Pointer{Season: 1, Episode: 1}, SeasonEpisodes: map[int]int{1: 12}},
	}
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", StartDate: "2026-11-25", EndDate: "2026-12-01", Items: []docstore.GuideItem{}})
	ing := &recordingIngester{}
	h := schedServer(t, fg, userIn("America/Chicago"), eveningUS, ing)

	if rec := do(t, h, http.MethodPost, "/v1/guides", "tok-1", `{"start_date":"2026-11-25","days":7}`); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (body %s)", rec.Code, rec.Body.String())
	}
	if rec := do(t, h, http.MethodPost, "/v1/guides/g1/regenerate", "tok-1", ""); rec.Code != http.StatusOK {
		t.Fatalf("regenerate = %d (body %s)", rec.Code, rec.Body.String())
	}
	sort.Strings(ing.calls)
	want := []string{"movie/79/US", "movie/79/US", "series/61695/US", "series/61695/US"}
	if len(ing.calls) != len(want) {
		t.Fatalf("ensure calls = %v, want %v", ing.calls, want)
	}
	for i := range want {
		if ing.calls[i] != want[i] {
			t.Fatalf("ensure calls = %v, want %v", ing.calls, want)
		}
	}
}
