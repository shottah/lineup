package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
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

	guides map[int64]map[int64]*store.Guide // userID -> guideID -> guide

	replaceArgs *replaceCall

	items      map[int64]itemOwner
	itemStore  map[int64]*store.GuideItem
	updateArgs *store.GuideItemUpdate

	swapInfo map[int64]*store.SwapInfo
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
	return &store.Guide{ID: 1, StartDate: startDate, EndDate: endDate, Seed: seed, Items: toStoreItems(items, 100)}, nil
}

func (f *fakeGuides) CurrentGuide(_ context.Context, _ int64, _ string) (*store.Guide, error) {
	if f.currentErr != nil {
		return nil, f.currentErr
	}
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
	return &store.Guide{ID: guideID, Items: toStoreItems(newItems, 1000)}, nil
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

func (f *fakeGuides) SwapTitle(_ context.Context, _ int64, titleID int64) (*store.SwapInfo, error) {
	info, ok := f.swapInfo[titleID]
	if !ok {
		return nil, store.ErrTitleNotFound
	}
	return info, nil
}

func guidesServer(t *testing.T, fg *fakeGuides) http.Handler {
	t.Helper()
	users := newFakeUsers()
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
	h := guidesServer(t, fg)

	rec := do(t, h, http.MethodPost, "/v1/guides", "tok-1", `{"start_date":"2026-01-10","days":3}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201 (body %s)", rec.Code, rec.Body.String())
	}
	if fg.createArgs == nil {
		t.Fatal("CreateGuideReplacingOverlaps was not called")
	}
	if fg.createArgs.seed != fixedClock.UnixNano() {
		t.Fatalf("seed = %d, want %d (fixed clock nanos)", fg.createArgs.seed, fixedClock.UnixNano())
	}
	wantEnd := time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC).Format(dateFmt)
	if fg.createArgs.startDate != "2026-01-10" || fg.createArgs.endDate != wantEnd {
		t.Fatalf("dates = %s..%s, want 2026-01-10..%s", fg.createArgs.startDate, fg.createArgs.endDate, wantEnd)
	}
	// The fixture has one series title that places one episode per enabled
	// day; the fake user's default prefs enable all 7 weekdays, so 3
	// requested days must yield 3 generated items.
	if len(fg.createArgs.items) != 3 {
		t.Fatalf("generated items = %d, want 3 (one per enabled day): %+v", len(fg.createArgs.items), fg.createArgs.items)
	}

	var got store.Guide
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if got.StartDate != "2026-01-10" || got.EndDate != wantEnd || got.Seed != fixedClock.UnixNano() || len(got.Items) != 3 {
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
		fg.swapInfo[7] = &store.SwapInfo{TitleID: 7, Kind: "series", Runtime: 45, PointerSeason: 2, PointerEpisode: 3}
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
		fg.swapInfo[8] = &store.SwapInfo{TitleID: 8, Kind: "movie", Runtime: 100}
		h := guidesServer(t, fg)
		rec := do(t, h, http.MethodPatch, "/v1/guides/1/items/1", "tok-1", `{"title_id":8}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("swap movie = %d, want 200 (body %s)", rec.Code, rec.Body.String())
		}
		u := fg.updateArgs
		if u.Season == nil || *u.Season != 0 || u.Episode == nil || *u.Episode != 0 {
			t.Fatalf("movie season/episode = %v/%v, want 0/0", u.Season, u.Episode)
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
