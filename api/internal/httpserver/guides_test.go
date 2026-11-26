package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/docstore"
	"github.com/shottah/lineup/api/internal/guide"
)

// fixedClock is the Deps.Now used across guide handler tests: a fixed
// mid-guide instant so seed/today math is deterministic.
var fixedClock = time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC)

// itemKey addresses a guide item within its guide: item IDs are only unique
// per guide (they live in the guide document's items array).
type itemKey struct{ guideID, itemID string }

type createCall struct {
	userID             string
	startDate, endDate string
	seed               int64
	items              []guide.Item
}

type replaceCall struct {
	userID, guideID string
	keepIDs         []string
	newItems        []guide.Item
	seed            int64
}

// fakeGuides implements GuideStore in memory, recording call args so tests
// can assert what the handlers passed downstream. It mirrors just enough of
// docstore's guide semantics for the flows under test; full behavior lives
// in the docstore emulator tests.
type fakeGuides struct {
	titles []guide.Title

	createArgs *createCall

	currentResult *docstore.Guide
	currentErr    error
	currentToday  string // records the `today` arg CurrentGuide was called with

	guides map[string]map[string]*docstore.Guide // userID -> guideID -> guide

	replaceArgs *replaceCall

	items      map[itemKey]string // item -> owning userID
	itemStore  map[itemKey]*docstore.GuideItem
	updateArgs *docstore.GuideItemUpdate
	updateErr  error // when set, UpdateGuideItem returns this instead of applying the update

	swapInfo map[string]*docstore.SwapInfo
}

func newFakeGuides() *fakeGuides {
	return &fakeGuides{
		guides:    map[string]map[string]*docstore.Guide{},
		items:     map[itemKey]string{},
		itemStore: map[itemKey]*docstore.GuideItem{},
		swapInfo:  map[string]*docstore.SwapInfo{},
	}
}

// fixtureTitles is a single small rotation title (a non-airing series with
// one provider and a 20-episode season), enough for the real engine to
// fill each enabled day with back-to-back episodes.
func fixtureTitles() []guide.Title {
	return []guide.Title{{
		ID: "series-1", Kind: "series", Name: "Show", Runtime: 30,
		Providers:      []int64{1},
		Pointer:        guide.Pointer{Season: 1, Episode: 1},
		SeasonEpisodes: map[int]int{1: 20},
	}}
}

// setGuide registers a guide g owned by userID for GuideWithItems, and
// indexes its items for patch/delete/watch ownership checks.
func (f *fakeGuides) setGuide(userID string, g *docstore.Guide) {
	if f.guides[userID] == nil {
		f.guides[userID] = map[string]*docstore.Guide{}
	}
	f.guides[userID][g.ID] = g
	for i := range g.Items {
		it := g.Items[i]
		k := itemKey{g.ID, it.ID}
		f.items[k] = userID
		cp := it
		f.itemStore[k] = &cp
	}
}

func toStoreItems(items []guide.Item, startID int) []docstore.GuideItem {
	out := make([]docstore.GuideItem, len(items))
	for i, it := range items {
		out[i] = docstore.GuideItem{
			ID: fmt.Sprintf("it%d", startID+i), Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin,
			TitleID: it.TitleID, Season: it.Season, Episode: it.Episode, ProviderID: it.Provider,
			IsPlan: it.IsPlan, Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched,
		}
	}
	return out
}

// toGuideItem mirrors handleRegenerate's keep conversion, for asserting
// newItems excludes echoed keeps.
func toGuideItem(it docstore.GuideItem) guide.Item {
	return guide.Item{Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin, TitleID: it.TitleID,
		Season: it.Season, Episode: it.Episode, Provider: it.ProviderID, IsPlan: it.IsPlan,
		Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched}
}

func (f *fakeGuides) GuideInputTitles(_ context.Context, _ string, _ string) ([]guide.Title, error) {
	return f.titles, nil
}

func (f *fakeGuides) CreateGuideReplacingOverlaps(_ context.Context, userID string, startDate, endDate string, seed int64, items []guide.Item) (*docstore.Guide, error) {
	f.createArgs = &createCall{userID: userID, startDate: startDate, endDate: endDate, seed: seed, items: items}
	g := &docstore.Guide{ID: "g1", StartDate: startDate, EndDate: endDate, Seed: seed, Items: toStoreItems(items, 100)}
	return g, nil
}

func (f *fakeGuides) CurrentGuide(_ context.Context, _ string, today string) (*docstore.Guide, error) {
	f.currentToday = today
	if f.currentErr != nil {
		return nil, f.currentErr
	}
	return f.currentResult, nil
}

func (f *fakeGuides) GuideWithItems(_ context.Context, userID, guideID string) (*docstore.Guide, error) {
	byGuide, ok := f.guides[userID]
	if !ok {
		return nil, docstore.ErrGuideNotFound
	}
	g, ok := byGuide[guideID]
	if !ok {
		return nil, docstore.ErrGuideNotFound
	}
	return g, nil
}

func (f *fakeGuides) ReplaceUnkeptItems(_ context.Context, userID, guideID string, keepIDs []string, newItems []guide.Item, seed int64) (*docstore.Guide, error) {
	f.replaceArgs = &replaceCall{userID: userID, guideID: guideID, keepIDs: keepIDs, newItems: newItems, seed: seed}
	g := &docstore.Guide{ID: guideID, Seed: seed, Items: toStoreItems(newItems, 1000)}
	return g, nil
}

// GuideLookups derives sidecar maps from the items of the guide being
// rendered (#18), mirroring just enough of the real docstore method's shape for
// handler tests: every item's title/provider id resolves to a non-empty
// fixture entry.
func (f *fakeGuides) GuideLookups(_ context.Context, items []docstore.GuideItem) (map[string]docstore.TitleLookup, map[int64]docstore.ProviderRow, error) {
	titles := map[string]docstore.TitleLookup{}
	provs := map[int64]docstore.ProviderRow{}
	for _, it := range items {
		titles[it.TitleID] = docstore.TitleLookup{Name: "Title " + it.TitleID, Kind: "series", TMDBID: 100000, PosterPath: "/p-" + it.TitleID + ".jpg"}
		provs[it.ProviderID] = docstore.ProviderRow{ID: it.ProviderID, Name: fmt.Sprintf("Provider %d", it.ProviderID), LogoPath: ""}
	}
	return titles, provs, nil
}

func (f *fakeGuides) UpdateGuideItem(_ context.Context, userID, guideID, itemID string, upd docstore.GuideItemUpdate) (*docstore.GuideItem, error) {
	owner, ok := f.items[itemKey{guideID, itemID}]
	if !ok || owner != userID {
		return nil, docstore.ErrGuideNotFound
	}
	f.updateArgs = &upd
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	it := f.itemStore[itemKey{guideID, itemID}]
	if upd.Date != nil {
		it.Date = *upd.Date
	}
	dur := it.EndMin - it.StartMin
	if upd.DurationMin != nil {
		dur = *upd.DurationMin
	}
	if upd.StartMin != nil {
		it.StartMin = *upd.StartMin
	}
	it.EndMin = it.StartMin + dur
	if upd.TitleID != nil {
		it.TitleID = *upd.TitleID
	}
	if upd.Season != nil {
		it.Season = *upd.Season
	}
	if upd.Episode != nil {
		it.Episode = *upd.Episode
	}
	if upd.Pinned != nil {
		it.Pinned = *upd.Pinned
	}
	if upd.SetEdited {
		it.Edited = true
	}
	if len(upd.SwapProviders) > 0 {
		// Mirror docstore.UpdateGuideItem's CASE: keep the current provider
		// when the new title streams there too, else fall back to the
		// lowest of the new title's providers.
		kept := false
		for _, p := range upd.SwapProviders {
			if p == it.ProviderID {
				kept = true
				break
			}
		}
		if !kept {
			lowest := upd.SwapProviders[0]
			for _, p := range upd.SwapProviders[1:] {
				if p < lowest {
					lowest = p
				}
			}
			it.ProviderID = lowest
		}
	}
	cp := *it
	return &cp, nil
}

func (f *fakeGuides) DeleteGuideItem(_ context.Context, userID, guideID, itemID string) error {
	owner, ok := f.items[itemKey{guideID, itemID}]
	if !ok || owner != userID {
		return docstore.ErrGuideNotFound
	}
	delete(f.items, itemKey{guideID, itemID})
	delete(f.itemStore, itemKey{guideID, itemID})
	return nil
}

func (f *fakeGuides) MarkItemWatched(_ context.Context, userID, guideID, itemID string) (*docstore.GuideItem, error) {
	owner, ok := f.items[itemKey{guideID, itemID}]
	if !ok || owner != userID {
		return nil, docstore.ErrGuideNotFound
	}
	it := f.itemStore[itemKey{guideID, itemID}]
	it.Watched = true
	cp := *it
	return &cp, nil
}

// UnmarkItemWatched mirrors just the route/status wiring the handler test
// needs (fake, not real reversal semantics — those are covered by the
// docstore's emulator-backed TestUnmarkItemWatched).
func (f *fakeGuides) UnmarkItemWatched(_ context.Context, userID, guideID, itemID string) (*docstore.GuideItem, error) {
	owner, ok := f.items[itemKey{guideID, itemID}]
	if !ok || owner != userID {
		return nil, docstore.ErrGuideNotFound
	}
	it := f.itemStore[itemKey{guideID, itemID}]
	it.Watched = false
	cp := *it
	return &cp, nil
}

func (f *fakeGuides) SwapTitle(_ context.Context, _ string, titleID string, _ string) (*docstore.SwapInfo, error) {
	info, ok := f.swapInfo[titleID]
	if !ok {
		return nil, docstore.ErrTitleNotFound
	}
	return info, nil
}

func guidesServer(t *testing.T, fg *fakeGuides) http.Handler {
	t.Helper()
	return guidesServerWithUsers(t, fg, newFakeUsers())
}

// guidesServerWithUsers is like guidesServer but lets the caller pre-seed the
// fake user store (e.g. with non-default SchedulePrefs) before any request
// runs.
func guidesServerWithUsers(t *testing.T, fg *fakeGuides, users *fakeUsers) http.Handler {
	t.Helper()
	verifier := fakeVerifierWithTok1()
	srv := New(Deps{Users: users, Verifier: verifier, Guides: fg, Now: func() time.Time { return fixedClock }})
	return srv.Handler
}

func TestCreateGuideValidation(t *testing.T) {
	fg := newFakeGuides()
	fg.titles = fixtureTitles()
	h := guidesServer(t, fg)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"malformed json", `{`, http.StatusBadRequest},
		{"bad start_date", `{"start_date":"not-a-date","days":3}`, http.StatusUnprocessableEntity},
		{"days zero", `{"start_date":"2026-01-10","days":0}`, http.StatusUnprocessableEntity},
		{"days 15", `{"start_date":"2026-01-10","days":15}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/v1/guides", "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s = %d, want %d (body %s)", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	if fg.createArgs != nil {
		t.Fatal("invalid create requests must not reach the store")
	}
}

func TestCreateGuideHappyPath(t *testing.T) {
	fg := newFakeGuides()
	fg.titles = fixtureTitles()

	// Enable only tue/thu/sat (all seven keys must be present with valid
	// HH:MM times to pass prefs.Validate). A uniform prefs.Default() fixture
	// (all 7 days enabled) can't verify the weekday-key mapping in guideDays
	// (guides.go), since every requested day would produce an item
	// regardless of whether the mapping used the right key.
	const tueThuSatPrefs = `{"windows":{` +
		`"mon":{"enabled":false,"start":"19:00","end":"23:00"},` +
		`"tue":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"wed":{"enabled":false,"start":"19:00","end":"23:00"},` +
		`"thu":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"fri":{"enabled":false,"start":"19:00","end":"23:00"},` +
		`"sat":{"enabled":true,"start":"19:00","end":"23:00"},` +
		`"sun":{"enabled":false,"start":"19:00","end":"23:00"}` +
		`}}`
	users := newFakeUsers()
	users.byUID["uid-1"] = &docstore.User{
		ID: "uid-1", Email: "one@example.com", DisplayName: "One",
		Region: "US", SchedulePrefs: json.RawMessage(tueThuSatPrefs),
	}
	h := guidesServerWithUsers(t, fg, users)

	// 2026-01-05 is a Monday, so a 4-day request spans Mon 05..Thu 08: only
	// Tue 06 and Thu 08 are enabled weekdays within this range (Sat is
	// enabled in prefs but falls outside the requested window).
	rec := do(t, h, http.MethodPost, "/v1/guides", "tok-1", `{"start_date":"2026-01-05","days":4}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	// writeGuide must set Content-Type before WriteHeader to ensure header ships with 201 response.
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
	}
	if fg.createArgs == nil {
		t.Fatal("CreateGuideReplacingOverlaps was not called")
	}
	if fg.createArgs.seed != fixedClock.UnixNano() {
		t.Fatalf("seed = %d, want %d (fixed clock nanos)", fg.createArgs.seed, fixedClock.UnixNano())
	}
	wantEnd := time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC).Format(dateFmt)
	if fg.createArgs.startDate != "2026-01-05" || fg.createArgs.endDate != wantEnd {
		t.Fatalf("dates = %s..%s, want 2026-01-05..%s", fg.createArgs.startDate, fg.createArgs.endDate, wantEnd)
	}
	// The fixture has one 30-min series that fills each enabled day back to
	// back (8 episodes in 19:00-23:00); with only tue/thu falling inside the
	// requested range, exactly 16 items must be generated, each landing on
	// a Tue/Thu/Sat date.
	if len(fg.createArgs.items) != 16 {
		t.Fatalf("generated items = %d, want 16 (8 back-to-back 30-min episodes per enabled weekday in range): %+v", len(fg.createArgs.items), fg.createArgs.items)
	}
	wantWeekdays := map[time.Weekday]bool{time.Tuesday: true, time.Thursday: true, time.Saturday: true}
	for _, it := range fg.createArgs.items {
		d, err := time.Parse(dateFmt, it.Date)
		if err != nil {
			t.Fatalf("item date %q not parseable: %v", it.Date, err)
		}
		if !wantWeekdays[d.Weekday()] {
			t.Fatalf("item date %q is a %s, want Tue/Thu/Sat", it.Date, d.Weekday())
		}
	}

	var got docstore.Guide
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.StartDate != "2026-01-05" || got.EndDate != wantEnd || got.Seed != fixedClock.UnixNano() || len(got.Items) != 16 {
		t.Fatalf("echoed guide = %+v", got)
	}
}

func TestCurrentGuide(t *testing.T) {
	fg := newFakeGuides()
	fg.currentResult = &docstore.Guide{ID: "g5", StartDate: "2026-01-05", EndDate: "2026-01-11", Seed: 42, Items: []docstore.GuideItem{}}
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got docstore.Guide
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.ID != "g5" {
		t.Fatalf("current guide id = %q, want g5", got.ID)
	}
	if fg.currentToday != "2026-01-08" {
		t.Fatalf("CurrentGuide today = %q, want %q (fixed clock, UTC-formatted)", fg.currentToday, "2026-01-08")
	}

	fg2 := newFakeGuides()
	fg2.currentErr = docstore.ErrGuideNotFound
	h2 := guidesServer(t, fg2)
	rec2 := do(t, h2, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("current (not found) = %d, want 404", rec2.Code)
	}
}

func TestRegenerate(t *testing.T) {
	fg := newFakeGuides()
	fg.titles = fixtureTitles()

	items := []docstore.GuideItem{
		{ID: "i1", Date: "2026-01-10", StartMin: 1140, EndMin: 1170, TitleID: "series-1", Season: 1, Episode: 1, ProviderID: 1, IsPlan: true, Pinned: true},
		{ID: "i2", Date: "2026-01-10", StartMin: 1170, EndMin: 1200, TitleID: "series-1", Season: 1, Episode: 2, ProviderID: 1, IsPlan: true, Edited: true},
		{ID: "i3", Date: "2026-01-11", StartMin: 1140, EndMin: 1170, TitleID: "series-1", Season: 1, Episode: 3, ProviderID: 1, IsPlan: true, Watched: true},
		{ID: "i4", Date: "2026-01-05", StartMin: 1140, EndMin: 1170, TitleID: "series-1", Season: 1, Episode: 4, ProviderID: 1, IsPlan: true}, // past-dated (before fixed "today")
		{ID: "i5", Date: "2026-01-12", StartMin: 1140, EndMin: 1170, TitleID: "series-1", Season: 1, Episode: 5, ProviderID: 1, IsPlan: true}, // future, unpinned -> dropped
	}
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", StartDate: "2026-01-05", EndDate: "2026-01-14", Seed: 999, Items: items})
	// A guide owned by a different user, to exercise the foreign-guide 404.
	fg.setGuide("uid-2", &docstore.Guide{ID: "g2", StartDate: "2026-01-05", EndDate: "2026-01-14", Items: []docstore.GuideItem{
		{ID: "i50", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1"},
	}})

	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides/g1/regenerate", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if fg.replaceArgs == nil {
		t.Fatal("ReplaceUnkeptItems was not called")
	}
	// Regenerate must mint a fresh seed (d.Now().UnixNano()) rather than
	// reusing the guide's original seed (999).
	if fg.replaceArgs.seed != fixedClock.UnixNano() {
		t.Fatalf("seed = %d, want %d (fixed clock nanos)", fg.replaceArgs.seed, fixedClock.UnixNano())
	}
	if fg.replaceArgs.seed == 999 {
		t.Fatal("regenerate must not reuse the guide's original seed")
	}
	wantKeep := []string{"i1", "i2", "i3", "i4"}
	if !reflect.DeepEqual(fg.replaceArgs.keepIDs, wantKeep) {
		t.Fatalf("keepIDs = %v, want %v", fg.replaceArgs.keepIDs, wantKeep)
	}
	kept := map[guide.Item]bool{}
	for _, it := range items[:4] {
		kept[toGuideItem(it)] = true
	}
	for _, ni := range fg.replaceArgs.newItems {
		if kept[ni] {
			t.Fatalf("newItems echoes a kept item: %+v", ni)
		}
	}

	rec2 := do(t, h, http.MethodPost, "/v1/guides/g2/regenerate", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("regenerate foreign guide = %d, want 404", rec2.Code)
	}
}

// TestRegenerateDoesNotBackfillPast guards against the engine treating a
// past day's spare capacity as fair game. The past day here holds a single
// 30-minute item inside the default 19:00-23:00 (1140-1380) window, leaving
// 210 idle minutes; a second title with open episodes is available, so
// without zeroing past-day windows before Generate, passFill would happily
// schedule it into that gap.
func TestRegenerateDoesNotBackfillPast(t *testing.T) {
	fg := newFakeGuides()
	fg.titles = []guide.Title{
		{ID: "series-1", Kind: "series", Name: "Show1", Runtime: 30, Providers: []int64{1},
			Pointer: guide.Pointer{Season: 1, Episode: 1}, SeasonEpisodes: map[int]int{1: 20}},
		{ID: "series-2", Kind: "series", Name: "Show2", Runtime: 30, Providers: []int64{2},
			Pointer: guide.Pointer{Season: 1, Episode: 1}, SeasonEpisodes: map[int]int{1: 20}},
	}
	items := []docstore.GuideItem{
		{ID: "i1", Date: "2026-01-05", StartMin: 1140, EndMin: 1170, TitleID: "series-1", Season: 1, Episode: 1, ProviderID: 1, IsPlan: true},
	}
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", StartDate: "2026-01-05", EndDate: "2026-01-14", Seed: 555, Items: items})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides/g1/regenerate", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if fg.replaceArgs == nil {
		t.Fatal("ReplaceUnkeptItems was not called")
	}
	const today = "2026-01-08" // fixedClock, UTC-formatted
	sawFuture := false
	for _, ni := range fg.replaceArgs.newItems {
		if ni.Date < today {
			t.Fatalf("regenerate backfilled a past day with a new item: %+v", ni)
		}
		sawFuture = true
	}
	if !sawFuture {
		t.Fatal("expected the engine to generate at least one future item (test would be vacuous otherwise)")
	}
}

func TestPatchItem(t *testing.T) {
	newItemGuide := func() *fakeGuides {
		fg := newFakeGuides()
		fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
			{ID: "i1", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1"},
		}})
		return fg
	}

	t.Run("pin only", func(t *testing.T) {
		fg := newItemGuide()
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"pinned":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("pin = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if fg.updateArgs == nil || fg.updateArgs.SetEdited {
			t.Fatalf("pin-only update = %+v, want SetEdited false", fg.updateArgs)
		}
		if fg.updateArgs.Pinned == nil || !*fg.updateArgs.Pinned {
			t.Fatal("pinned not passed through")
		}
	})

	t.Run("move", func(t *testing.T) {
		fg := newItemGuide()
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"date":"2026-01-10","start_min":120}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("move = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if fg.updateArgs == nil || !fg.updateArgs.SetEdited {
			t.Fatal("move must SetEdited")
		}
		if fg.updateArgs.Date == nil || *fg.updateArgs.Date != "2026-01-10" {
			t.Fatalf("date = %v, want 2026-01-10", fg.updateArgs.Date)
		}
		if fg.updateArgs.StartMin == nil || *fg.updateArgs.StartMin != 120 {
			t.Fatalf("start_min = %v, want 120", fg.updateArgs.StartMin)
		}
	})

	t.Run("swap series", func(t *testing.T) {
		fg := newItemGuide()
		fg.swapInfo["series-7"] = &docstore.SwapInfo{TitleID: "series-7", Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3, Providers: []int64{}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"title_id":"series-7"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap series = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		u := fg.updateArgs
		if u == nil || !u.SetEdited {
			t.Fatal("swap must SetEdited")
		}
		if u.TitleID == nil || *u.TitleID != "series-7" {
			t.Fatalf("title_id = %v, want series-7", u.TitleID)
		}
		if u.DurationMin == nil || *u.DurationMin != 45 {
			t.Fatalf("duration_min = %v, want 45 (swap runtime)", u.DurationMin)
		}
		if u.Season == nil || *u.Season != 2 || u.Episode == nil || *u.Episode != 3 {
			t.Fatalf("season/episode = %v/%v, want pointer 2/3", u.Season, u.Episode)
		}
	})

	t.Run("swap movie", func(t *testing.T) {
		fg := newItemGuide()
		// PointerSeason/PointerEpisode are stale (nonzero) values a swap
		// target's data might legitimately carry; the handler must ignore
		// them for movies (the `if info.Kind == "series"` gate), so the
		// season/episode assertion below genuinely depends on that gate
		// rather than just reflecting zero-valued fixture fields.
		fg.swapInfo["series-8"] = &docstore.SwapInfo{TitleID: "series-8", Kind: "movie", Runtime: 100, PointerSeason: 9, PointerEpisode: 9, Providers: []int64{}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"title_id":"series-8"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap movie = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		u := fg.updateArgs
		if u.Season == nil || *u.Season != 0 || u.Episode == nil || *u.Episode != 0 {
			t.Fatalf("movie season/episode = %v/%v, want 0/0", u.Season, u.Episode)
		}
		if u.DurationMin == nil || *u.DurationMin != 100 {
			t.Fatalf("duration_min = %v, want 100 (swap runtime)", u.DurationMin)
		}
	})

	t.Run("swap keeps provider when new title streams there", func(t *testing.T) {
		fg := newFakeGuides()
		fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
			{ID: "i1", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1", ProviderID: 5},
		}})
		fg.swapInfo["series-7"] = &docstore.SwapInfo{TitleID: "series-7", Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3, Providers: []int64{3, 5, 9}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"title_id":"series-7"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if !reflect.DeepEqual(fg.updateArgs.SwapProviders, []int64{3, 5, 9}) {
			t.Fatalf("SwapProviders = %v, want [3 5 9]", fg.updateArgs.SwapProviders)
		}
		var got docstore.GuideItem
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("body not JSON: %v", err)
		}
		if got.ProviderID != 5 {
			t.Fatalf("provider_id = %d, want 5 (kept: new title streams on the item's current provider)", got.ProviderID)
		}
	})

	t.Run("swap sets lowest provider when new title doesn't stream there", func(t *testing.T) {
		fg := newFakeGuides()
		fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
			{ID: "i1", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1", ProviderID: 99},
		}})
		fg.swapInfo["series-7"] = &docstore.SwapInfo{TitleID: "series-7", Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3, Providers: []int64{3, 5, 9}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"title_id":"series-7"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var got docstore.GuideItem
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("body not JSON: %v", err)
		}
		if got.ProviderID != 3 {
			t.Fatalf("provider_id = %d, want 3 (lowest of the new title's providers: item's current provider isn't among them)", got.ProviderID)
		}
	})

	t.Run("swap target rejected", func(t *testing.T) {
		fg := newItemGuide()
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"title_id":"series-999"}`)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "invalid title") {
			t.Fatalf("swap rejected = %d body %s, want 422 invalid title", rec.Code, rec.Body.String())
		}
	})

	// TestUpdateGuideItemPastMove (docstore package) covers the actual
	// enforcement; here we only assert the handler maps docstore.ErrPastMove
	// to a 422 with the documented error body.
	t.Run("past move rejected", func(t *testing.T) {
		fg := newItemGuide()
		fg.updateErr = docstore.ErrPastMove
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", `{"start_min":120}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("past move = %d, want 422 (body %s)", rec.Code, rec.Body.String())
		}
		if rec.Body.String() != `{"error":"cannot move past slot"}` {
			t.Fatalf("past move body = %q, want %q", rec.Body.String(), `{"error":"cannot move past slot"}`)
		}
	})

	fg := newItemGuide()
	h := guidesServer(t, fg)

	valCases := []struct {
		name string
		body string
		want int
	}{
		{"empty body object", `{}`, http.StatusUnprocessableEntity},
		{"start_min -1", `{"start_min":-1}`, http.StatusUnprocessableEntity},
		{"start_min 1441", `{"start_min":1441}`, http.StatusUnprocessableEntity},
	}
	for _, tc := range valCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i1", "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s = %d, want %d (body %s)", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	rec := do(t, h, http.MethodPatch, "/v1/guides/g1/items/i999", "tok-1", `{"pinned":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown item = %d, want 404", rec.Code)
	}
}

func TestDeleteItem(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
		{ID: "i1", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1"},
	}})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodDelete, "/v1/guides/g1/items/i1", "tok-1", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	rec2 := do(t, h, http.MethodDelete, "/v1/guides/g1/items/i999", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d, want 404", rec2.Code)
	}
}

func TestWatchItem(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
		{ID: "i1", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1"},
	}})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides/g1/items/i1/watched", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("watch = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got docstore.GuideItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if !got.Watched {
		t.Fatal("watched item not marked watched in response")
	}

	rec2 := do(t, h, http.MethodPost, "/v1/guides/g1/items/i999/watched", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("watch unknown = %d, want 404", rec2.Code)
	}
}

func TestUnwatchItem(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide("uid-1", &docstore.Guide{ID: "g1", Items: []docstore.GuideItem{
		{ID: "i1", Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1", Watched: true},
	}})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodDelete, "/v1/guides/g1/items/i1/watched", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("unwatch = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got docstore.GuideItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.Watched {
		t.Fatal("unwatched item still marked watched in response")
	}

	// Idempotent: unwatching an already-unwatched item is a 200 no-op.
	rec2 := do(t, h, http.MethodDelete, "/v1/guides/g1/items/i1/watched", "tok-1", "")
	if rec2.Code != http.StatusOK {
		t.Fatalf("re-unwatch = %d, want 200 (body %s)", rec2.Code, rec2.Body.String())
	}

	rec3 := do(t, h, http.MethodDelete, "/v1/guides/g1/items/i999/watched", "tok-1", "")
	if rec3.Code != http.StatusNotFound {
		t.Fatalf("unwatch unknown = %d, want 404", rec3.Code)
	}
}

// TestCurrentGuideCarriesSidecars guards #18: guide-returning responses must
// carry titles/providers rendering dictionaries alongside the bare items, so
// the web guide views can resolve names/kinds/tmdb_ids/logos without extra
// round trips.
func TestCurrentGuideCarriesSidecars(t *testing.T) {
	fg := newFakeGuides()
	fg.currentResult = &docstore.Guide{ID: "g5", StartDate: "2026-01-05", EndDate: "2026-01-11", Seed: 42, Items: []docstore.GuideItem{
		{ID: "i1", Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: "series-10", ProviderID: 20, IsPlan: true},
		{ID: "i2", Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: "series-11", ProviderID: 21, IsPlan: true},
	}}
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Items     []docstore.GuideItem            `json:"items"`
		Titles    map[string]docstore.TitleLookup `json:"titles"`
		Providers map[string]docstore.ProviderRow `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	for _, it := range body.Items {
		tl, ok := body.Titles[it.TitleID]
		if !ok || tl.Name == "" || tl.PosterPath == "" {
			t.Fatalf("titles[%s] missing/empty: %+v", it.TitleID, body.Titles)
		}
		pr, ok := body.Providers[strconv.FormatInt(it.ProviderID, 10)]
		if !ok || pr.Name == "" {
			t.Fatalf("providers[%d] missing/empty: %+v", it.ProviderID, body.Providers)
		}
	}
}
