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

	"github.com/shottah/lineup/api/internal/guide"
	"github.com/shottah/lineup/api/internal/store"
)

// fixedClock is the Deps.Now used across guide handler tests: a fixed
// mid-guide instant so seed/today math is deterministic.
var fixedClock = time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC)

// itemOwner scopes a guide item to its (userID, guideID), mirroring the
// store's user/guide-scoped ownership checks.
type itemOwner struct{ userID, guideID int64 }

type createCall struct {
	userID             int64
	startDate, endDate string
	seed               int64
	items              []guide.Item
}

type replaceCall struct {
	userID, guideID int64
	keepIDs         []int64
	newItems        []guide.Item
}

// fakeGuides implements GuideStore in memory, recording call args so tests
// can assert what the handlers passed downstream. It mirrors just enough of
// store/guides.go's semantics for the flows under test; full behavior lives
// in Task 2's integration tests.
type fakeGuides struct {
	titles []guide.Title

	createArgs *createCall

	currentResult *store.Guide
	currentErr    error
	currentToday  string // records the `today` arg CurrentGuide was called with

	guides map[int64]map[int64]*store.Guide // userID -> guideID -> guide

	replaceArgs *replaceCall

	items      map[int64]itemOwner
	itemStore  map[int64]*store.GuideItem
	updateArgs *store.GuideItemUpdate

	swapInfo map[int64]*store.SwapInfo

	// lastGuide is the most recent guide handed back by one of the three
	// wrapped responses (create/current/regenerate), for GuideLookups to
	// derive its sidecar maps from.
	lastGuide *store.Guide
}

func newFakeGuides() *fakeGuides {
	return &fakeGuides{
		guides:    map[int64]map[int64]*store.Guide{},
		items:     map[int64]itemOwner{},
		itemStore: map[int64]*store.GuideItem{},
		swapInfo:  map[int64]*store.SwapInfo{},
	}
}

// fixtureTitles is a single small rotation title (a non-airing series with
// one provider and a 20-episode season), enough for the real engine to
// place one episode per enabled day.
func fixtureTitles() []guide.Title {
	return []guide.Title{{
		ID: 1, Kind: "series", Name: "Show", Runtime: 30,
		Providers:      []int64{1},
		Pointer:        guide.Pointer{Season: 1, Episode: 1},
		SeasonEpisodes: map[int]int{1: 20},
	}}
}

// setGuide registers a guide g owned by userID for GuideWithItems, and
// indexes its items for patch/delete/watch ownership checks.
func (f *fakeGuides) setGuide(userID int64, g *store.Guide) {
	if f.guides[userID] == nil {
		f.guides[userID] = map[int64]*store.Guide{}
	}
	f.guides[userID][g.ID] = g
	for i := range g.Items {
		it := g.Items[i]
		f.items[it.ID] = itemOwner{userID: userID, guideID: g.ID}
		cp := it
		f.itemStore[it.ID] = &cp
	}
}

func toStoreItems(items []guide.Item, startID int64) []store.GuideItem {
	out := make([]store.GuideItem, len(items))
	for i, it := range items {
		out[i] = store.GuideItem{
			ID: startID + int64(i), Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin,
			TitleID: it.TitleID, Season: it.Season, Episode: it.Episode, ProviderID: it.Provider,
			IsPlan: it.IsPlan, Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched,
		}
	}
	return out
}

// toGuideItem mirrors handleRegenerate's keep conversion, for asserting
// newItems excludes echoed keeps.
func toGuideItem(it store.GuideItem) guide.Item {
	return guide.Item{Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin, TitleID: it.TitleID,
		Season: it.Season, Episode: it.Episode, Provider: it.ProviderID, IsPlan: it.IsPlan,
		Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched}
}

func (f *fakeGuides) GuideInputTitles(_ context.Context, _ int64, _ string) ([]guide.Title, error) {
	return f.titles, nil
}

func (f *fakeGuides) CreateGuideReplacingOverlaps(_ context.Context, userID int64, startDate, endDate string, seed int64, items []guide.Item) (*store.Guide, error) {
	f.createArgs = &createCall{userID: userID, startDate: startDate, endDate: endDate, seed: seed, items: items}
	g := &store.Guide{ID: 1, StartDate: startDate, EndDate: endDate, Seed: seed, Items: toStoreItems(items, 100)}
	f.lastGuide = g
	return g, nil
}

func (f *fakeGuides) CurrentGuide(_ context.Context, _ int64, today string) (*store.Guide, error) {
	f.currentToday = today
	if f.currentErr != nil {
		return nil, f.currentErr
	}
	f.lastGuide = f.currentResult
	return f.currentResult, nil
}

func (f *fakeGuides) GuideWithItems(_ context.Context, userID, guideID int64) (*store.Guide, error) {
	byGuide, ok := f.guides[userID]
	if !ok {
		return nil, store.ErrGuideNotFound
	}
	g, ok := byGuide[guideID]
	if !ok {
		return nil, store.ErrGuideNotFound
	}
	return g, nil
}

func (f *fakeGuides) ReplaceUnkeptItems(_ context.Context, userID, guideID int64, keepIDs []int64, newItems []guide.Item) (*store.Guide, error) {
	f.replaceArgs = &replaceCall{userID: userID, guideID: guideID, keepIDs: keepIDs, newItems: newItems}
	g := &store.Guide{ID: guideID, Items: toStoreItems(newItems, 1000)}
	f.lastGuide = g
	return g, nil
}

// GuideLookups derives sidecar maps from whatever guide was last handed back
// by one of the three wrapped responses (#18), mirroring just enough of the
// real store method's shape for handler tests: every item's title/provider
// id resolves to a non-empty fixture entry.
func (f *fakeGuides) GuideLookups(_ context.Context, guideID int64) (map[int64]store.TitleLookup, map[int64]store.ProviderRow, error) {
	titles := map[int64]store.TitleLookup{}
	provs := map[int64]store.ProviderRow{}
	if f.lastGuide == nil || f.lastGuide.ID != guideID {
		return titles, provs, nil
	}
	for _, it := range f.lastGuide.Items {
		titles[it.TitleID] = store.TitleLookup{Name: fmt.Sprintf("Title %d", it.TitleID), Kind: "series", TMDBID: it.TitleID + 100000}
		provs[it.ProviderID] = store.ProviderRow{ID: it.ProviderID, Name: fmt.Sprintf("Provider %d", it.ProviderID), LogoPath: ""}
	}
	return titles, provs, nil
}

func (f *fakeGuides) UpdateGuideItem(_ context.Context, userID, guideID, itemID int64, upd store.GuideItemUpdate) (*store.GuideItem, error) {
	owner, ok := f.items[itemID]
	if !ok || owner.userID != userID || owner.guideID != guideID {
		return nil, store.ErrGuideNotFound
	}
	f.updateArgs = &upd
	it := f.itemStore[itemID]
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
		// Mirror store.UpdateGuideItem's CASE: keep the current provider
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

func (f *fakeGuides) DeleteGuideItem(_ context.Context, userID, guideID, itemID int64) error {
	owner, ok := f.items[itemID]
	if !ok || owner.userID != userID || owner.guideID != guideID {
		return store.ErrGuideNotFound
	}
	delete(f.items, itemID)
	delete(f.itemStore, itemID)
	return nil
}

func (f *fakeGuides) MarkItemWatched(_ context.Context, userID, guideID, itemID int64) (*store.GuideItem, error) {
	owner, ok := f.items[itemID]
	if !ok || owner.userID != userID || owner.guideID != guideID {
		return nil, store.ErrGuideNotFound
	}
	it := f.itemStore[itemID]
	it.Watched = true
	cp := *it
	return &cp, nil
}

func (f *fakeGuides) SwapTitle(_ context.Context, _ int64, titleID int64, _ string) (*store.SwapInfo, error) {
	info, ok := f.swapInfo[titleID]
	if !ok {
		return nil, store.ErrTitleNotFound
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
	users.byUID["uid-1"] = &store.User{
		ID: 1, FirebaseUID: "uid-1", Email: "one@example.com", DisplayName: "One",
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
	// The fixture has one series title that places one episode per enabled
	// day; with only tue/thu falling inside the requested range, exactly 2
	// items must be generated, each landing on a Tue/Thu/Sat date.
	if len(fg.createArgs.items) != 2 {
		t.Fatalf("generated items = %d, want 2 (one per enabled weekday in range): %+v", len(fg.createArgs.items), fg.createArgs.items)
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

	var got store.Guide
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.StartDate != "2026-01-05" || got.EndDate != wantEnd || got.Seed != fixedClock.UnixNano() || len(got.Items) != 2 {
		t.Fatalf("echoed guide = %+v", got)
	}
}

func TestCurrentGuide(t *testing.T) {
	fg := newFakeGuides()
	fg.currentResult = &store.Guide{ID: 5, StartDate: "2026-01-05", EndDate: "2026-01-11", Seed: 42, Items: []store.GuideItem{}}
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got store.Guide
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.ID != 5 {
		t.Fatalf("current guide id = %d, want 5", got.ID)
	}
	if fg.currentToday != "2026-01-08" {
		t.Fatalf("CurrentGuide today = %q, want %q (fixed clock, UTC-formatted)", fg.currentToday, "2026-01-08")
	}

	fg2 := newFakeGuides()
	fg2.currentErr = store.ErrGuideNotFound
	h2 := guidesServer(t, fg2)
	rec2 := do(t, h2, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("current (not found) = %d, want 404", rec2.Code)
	}
}

func TestRegenerate(t *testing.T) {
	fg := newFakeGuides()
	fg.titles = fixtureTitles()

	items := []store.GuideItem{
		{ID: 1, Date: "2026-01-10", StartMin: 1140, EndMin: 1170, TitleID: 1, Season: 1, Episode: 1, ProviderID: 1, IsPlan: true, Pinned: true},
		{ID: 2, Date: "2026-01-10", StartMin: 1170, EndMin: 1200, TitleID: 1, Season: 1, Episode: 2, ProviderID: 1, IsPlan: true, Edited: true},
		{ID: 3, Date: "2026-01-11", StartMin: 1140, EndMin: 1170, TitleID: 1, Season: 1, Episode: 3, ProviderID: 1, IsPlan: true, Watched: true},
		{ID: 4, Date: "2026-01-05", StartMin: 1140, EndMin: 1170, TitleID: 1, Season: 1, Episode: 4, ProviderID: 1, IsPlan: true}, // past-dated (before fixed "today")
		{ID: 5, Date: "2026-01-12", StartMin: 1140, EndMin: 1170, TitleID: 1, Season: 1, Episode: 5, ProviderID: 1, IsPlan: true}, // future, unpinned -> dropped
	}
	fg.setGuide(1, &store.Guide{ID: 1, StartDate: "2026-01-05", EndDate: "2026-01-14", Seed: 999, Items: items})
	// A guide owned by a different user, to exercise the foreign-guide 404.
	fg.setGuide(2, &store.Guide{ID: 2, StartDate: "2026-01-05", EndDate: "2026-01-14", Items: []store.GuideItem{
		{ID: 50, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: 1},
	}})

	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides/1/regenerate", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("regenerate = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if fg.replaceArgs == nil {
		t.Fatal("ReplaceUnkeptItems was not called")
	}
	wantKeep := []int64{1, 2, 3, 4}
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

	rec2 := do(t, h, http.MethodPost, "/v1/guides/2/regenerate", "tok-1", "")
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
		{ID: 1, Kind: "series", Name: "Show1", Runtime: 30, Providers: []int64{1},
			Pointer: guide.Pointer{Season: 1, Episode: 1}, SeasonEpisodes: map[int]int{1: 20}},
		{ID: 2, Kind: "series", Name: "Show2", Runtime: 30, Providers: []int64{2},
			Pointer: guide.Pointer{Season: 1, Episode: 1}, SeasonEpisodes: map[int]int{1: 20}},
	}
	items := []store.GuideItem{
		{ID: 1, Date: "2026-01-05", StartMin: 1140, EndMin: 1170, TitleID: 1, Season: 1, Episode: 1, ProviderID: 1, IsPlan: true},
	}
	fg.setGuide(1, &store.Guide{ID: 1, StartDate: "2026-01-05", EndDate: "2026-01-14", Seed: 555, Items: items})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides/1/regenerate", "tok-1", "")
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
		fg.setGuide(1, &store.Guide{ID: 1, Items: []store.GuideItem{
			{ID: 1, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: 1},
		}})
		return fg
	}

	t.Run("pin only", func(t *testing.T) {
		fg := newItemGuide()
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"pinned":true}`)
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
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"date":"2026-01-10","start_min":120}`)
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
		fg.swapInfo[7] = &store.SwapInfo{TitleID: 7, Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3, Providers: []int64{}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"title_id":7}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap series = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		u := fg.updateArgs
		if u == nil || !u.SetEdited {
			t.Fatal("swap must SetEdited")
		}
		if u.TitleID == nil || *u.TitleID != 7 {
			t.Fatalf("title_id = %v, want 7", u.TitleID)
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
		fg.swapInfo[8] = &store.SwapInfo{TitleID: 8, Kind: "movie", Runtime: 100, PointerSeason: 9, PointerEpisode: 9, Providers: []int64{}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"title_id":8}`)
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
		fg.setGuide(1, &store.Guide{ID: 1, Items: []store.GuideItem{
			{ID: 1, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: 1, ProviderID: 5},
		}})
		fg.swapInfo[7] = &store.SwapInfo{TitleID: 7, Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3, Providers: []int64{3, 5, 9}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"title_id":7}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		if !reflect.DeepEqual(fg.updateArgs.SwapProviders, []int64{3, 5, 9}) {
			t.Fatalf("SwapProviders = %v, want [3 5 9]", fg.updateArgs.SwapProviders)
		}
		var got store.GuideItem
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("body not JSON: %v", err)
		}
		if got.ProviderID != 5 {
			t.Fatalf("provider_id = %d, want 5 (kept: new title streams on the item's current provider)", got.ProviderID)
		}
	})

	t.Run("swap sets lowest provider when new title doesn't stream there", func(t *testing.T) {
		fg := newFakeGuides()
		fg.setGuide(1, &store.Guide{ID: 1, Items: []store.GuideItem{
			{ID: 1, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: 1, ProviderID: 99},
		}})
		fg.swapInfo[7] = &store.SwapInfo{TitleID: 7, Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3, Providers: []int64{3, 5, 9}}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"title_id":7}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		var got store.GuideItem
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
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"title_id":999}`)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "invalid title") {
			t.Fatalf("swap rejected = %d body %s, want 422 invalid title", rec.Code, rec.Body.String())
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
			rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s = %d, want %d (body %s)", tc.name, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/999", "tok-1", `{"pinned":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown item = %d, want 404", rec.Code)
	}
}

func TestDeleteItem(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide(1, &store.Guide{ID: 1, Items: []store.GuideItem{
		{ID: 1, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: 1},
	}})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodDelete, "/v1/guides/1/items/1", "tok-1", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204 (body %s)", rec.Code, rec.Body.String())
	}

	rec2 := do(t, h, http.MethodDelete, "/v1/guides/1/items/999", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("delete unknown = %d, want 404", rec2.Code)
	}
}

func TestWatchItem(t *testing.T) {
	fg := newFakeGuides()
	fg.setGuide(1, &store.Guide{ID: 1, Items: []store.GuideItem{
		{ID: 1, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: 1},
	}})
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides/1/items/1/watched", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("watch = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var got store.GuideItem
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if !got.Watched {
		t.Fatal("watched item not marked watched in response")
	}

	rec2 := do(t, h, http.MethodPost, "/v1/guides/1/items/999/watched", "tok-1", "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("watch unknown = %d, want 404", rec2.Code)
	}
}

// TestCurrentGuideCarriesSidecars guards #18: guide-returning responses must
// carry titles/providers rendering dictionaries alongside the bare items, so
// the web guide views can resolve names/kinds/tmdb_ids/logos without extra
// round trips.
func TestCurrentGuideCarriesSidecars(t *testing.T) {
	fg := newFakeGuides()
	fg.currentResult = &store.Guide{ID: 5, StartDate: "2026-01-05", EndDate: "2026-01-11", Seed: 42, Items: []store.GuideItem{
		{ID: 1, Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: 10, ProviderID: 20, IsPlan: true},
		{ID: 2, Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: 11, ProviderID: 21, IsPlan: true},
	}}
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodGet, "/v1/guides/current", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("current = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Items     []store.GuideItem            `json:"items"`
		Titles    map[string]store.TitleLookup `json:"titles"`
		Providers map[string]store.ProviderRow `json:"providers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(body.Items))
	}
	for _, it := range body.Items {
		tl, ok := body.Titles[strconv.FormatInt(it.TitleID, 10)]
		if !ok || tl.Name == "" {
			t.Fatalf("titles[%d] missing/empty: %+v", it.TitleID, body.Titles)
		}
		pr, ok := body.Providers[strconv.FormatInt(it.ProviderID, 10)]
		if !ok || pr.Name == "" {
			t.Fatalf("providers[%d] missing/empty: %+v", it.ProviderID, body.Providers)
		}
	}
}
