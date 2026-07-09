# Guide Endpoints (issue #14) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The six guide endpoints wiring `internal/guide.Generate` to Postgres: create (with overlap replacement), current, regenerate (keep-set), item move/swap/pin, delete, watched-marking with pointer rollover.

**Architecture:** `store/guides.go` owns hydration (returns `[]guide.Title` — store imports the pure guide package) and all persistence in ownership-scoped queries/transactions; a pure `nextPointer` helper makes rollover unit-testable without a DB. Handlers stay thin behind a `GuideStore` interface with a `Deps.Now` clock seam.

**Tech Stack:** Go 1.25, chi v5, pgx v5, internal/guide, internal/prefs.

## Global Constraints

- Branch `feat/14-guide-endpoints` (off `main`), squash-merge. Commit per task.
- Dates are "YYYY-MM-DD" strings end-to-end (`to_char` in SQL; `2006-01-02` in Go). "Today" = `Deps.Now().UTC().Format("2006-01-02")`.
- Errors in house style: 400 `malformed json`; 404 `not_found`; 422 `invalid start_date` / `invalid days` / `invalid title` / `invalid date` / `invalid start_min` / `nothing to update`; 500 `internal`.
- `days` bounds: 1–14 inclusive. `start_min` bounds when patched: 0–1440.
- Ownership: every guide/item query joins `guides.user_id`; misses surface `ErrGuideNotFound` → 404. Item routes also match the `{id}` path guide.
- Move preserves duration (`end_min` follows `start_min`); swap sets duration to the new title's runtime and season/episode to the user's pointer (series) or 0/0 (movie). Move and swap set `edited`; pin-only does not.
- Regenerate keep set: `pinned OR edited OR watched OR date < today`. Engine echoes keeps verbatim; new rows = engine output minus the keep set (guide.Item is comparable — filter via `map[guide.Item]bool`).
- MarkItemWatched: pointer advances only when the item's episode ≥ current pointer; rollover via `title_seasons`; past finale → `user_titles.status='watched', watched_at=now()` (pointer parks on the finale). Movies → watched immediately.
- Pre-#11: no `title_providers` rows → empty guides are legitimate; tests seed providers.
- Store integration tests gated on `TEST_DATABASE_URL`; handler tests hermetic with fakes + fixed clock. TDD per task.

---

### Task 1: `prefs.Windows` helper

**Files:**
- Modify: `api/internal/prefs/prefs.go` (add ParsedWindow, Windows, hhmm)
- Modify: `api/internal/prefs/prefs_test.go` (add tests)

**Interfaces:**
- Produces: `prefs.ParsedWindow{Enabled bool; StartMin, EndMin int}`; `prefs.Windows(raw json.RawMessage) (map[string]ParsedWindow, error)` — weekday key ("mon".."sun") → window; legacy empty doc `{}` (and empty/nil raw) → empty map, nil error; invalid docs → error.

- [ ] **Step 1: Write the failing tests** (append to prefs_test.go)

```go
func TestWindows(t *testing.T) {
	got, err := Windows(Default())
	if err != nil {
		t.Fatalf("Windows(Default()): %v", err)
	}
	if len(got) != 7 {
		t.Fatalf("windows = %d, want 7", len(got))
	}
	if w := got["mon"]; !w.Enabled || w.StartMin != 19*60 || w.EndMin != 23*60 {
		t.Fatalf("mon = %+v, want enabled 1140-1380", w)
	}

	empty, err := Windows(json.RawMessage(`{}`))
	if err != nil || len(empty) != 0 {
		t.Fatalf("Windows({}) = %v, %v; want empty map, nil", empty, err)
	}
	if _, err := Windows(nil); err != nil {
		t.Fatalf("Windows(nil) = %v, want nil error", err)
	}
	if _, err := Windows(json.RawMessage(`{"windows":{"mon":{"enabled":true,"start":"9:00","end":"10:00"}}}`)); err == nil {
		t.Fatal("invalid doc must error")
	}
}
```

- [ ] **Step 2: FAIL run** — `cd api && go test ./internal/prefs/` (undefined Windows).

- [ ] **Step 3: Implement** (append to prefs.go; add `"strconv"` to imports)

```go
// ParsedWindow is a decoded viewing window in minutes from midnight.
type ParsedWindow struct {
	Enabled  bool
	StartMin int
	EndMin   int
}

// Windows decodes a schedule_prefs document into per-weekday windows for
// guide generation. The legacy empty document `{}` (the schema default
// before first sign-in) and empty input yield an empty map, not an error.
func Windows(raw json.RawMessage) (map[string]ParsedWindow, error) {
	trimmed := string(bytes.TrimSpace(raw))
	if trimmed == "" || trimmed == "{}" {
		return map[string]ParsedWindow{}, nil
	}
	if err := Validate(raw); err != nil {
		return nil, err
	}
	var doc prefsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("prefs: %w", err) // unreachable after Validate
	}
	out := make(map[string]ParsedWindow, len(doc.Windows))
	for day, w := range doc.Windows {
		out[day] = ParsedWindow{Enabled: w.Enabled, StartMin: hhmm(w.Start), EndMin: hhmm(w.End)}
	}
	return out, nil
}

// hhmm converts a Validate-checked "HH:MM" to minutes from midnight.
func hhmm(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}
```

- [ ] **Step 4: PASS run** — `cd api && go test ./internal/prefs/ && go vet ./...`
- [ ] **Step 5: Commit** — `git add api/internal/prefs/ && git commit -m "feat(api): prefs windows decoder for guide days"`

### Task 2: store guides layer

**Files:**
- Create: `api/internal/store/guides.go`
- Create: `api/internal/store/guides_test.go`

**Interfaces (Task 3 consumes verbatim):**
- `store.Guide{ID int64; StartDate, EndDate string; Seed int64; Items []GuideItem}` (json: id/start_date/end_date/seed/items)
- `store.GuideItem{ID int64; Date string; StartMin, EndMin int; TitleID int64; Season, Episode int; ProviderID int64; IsPlan, Pinned, Edited, Watched bool}` (json: id/date/start_min/end_min/title_id/season/episode/provider_id/is_plan/pinned/edited/watched)
- `store.GuideItemUpdate{Date *string; StartMin *int; DurationMin *int; TitleID *int64; Season, Episode *int; Pinned *bool; SetEdited bool}`
- `store.SwapInfo{TitleID int64; Kind string; Runtime int; PointerSeason, PointerEpisode int}`
- `var ErrGuideNotFound = errors.New("store: guide not found")`
- Methods: `GuideInputTitles(ctx, userID int64, region string) ([]guide.Title, error)`; `CreateGuideReplacingOverlaps(ctx, userID int64, startDate, endDate string, seed int64, items []guide.Item) (*Guide, error)`; `CurrentGuide(ctx, userID int64, today string) (*Guide, error)`; `GuideWithItems(ctx, userID, guideID int64) (*Guide, error)`; `ReplaceUnkeptItems(ctx, userID, guideID int64, keepIDs []int64, items []guide.Item) (*Guide, error)`; `UpdateGuideItem(ctx, userID, guideID, itemID int64, upd GuideItemUpdate) (*GuideItem, error)`; `DeleteGuideItem(ctx, userID, guideID, itemID int64) error`; `MarkItemWatched(ctx, userID, guideID, itemID int64) (*GuideItem, error)`; `SwapTitle(ctx, userID, titleID int64) (*SwapInfo, error)`
- Pure helper: `nextPointer(itemSeason, itemEpisode int, counts map[int]int) (Pointer, bool)` — returns the pointer after the item and whether the finale was passed.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/store/guides_test.go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/shottah/lineup/api/internal/guide"
)

func TestNextPointer(t *testing.T) {
	counts := map[int]int{1: 10, 2: 8}
	cases := []struct {
		name       string
		s, e       int
		want       Pointer
		pastFinale bool
	}{
		{"mid season", 1, 5, Pointer{1, 6}, false},
		{"season rollover", 1, 10, Pointer{2, 1}, false},
		{"finale", 2, 8, Pointer{2, 8}, true},
		{"beyond known seasons", 3, 1, Pointer{3, 1}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, past := nextPointer(tc.s, tc.e, counts)
			if got != tc.want || past != tc.pastFinale {
				t.Fatalf("nextPointer(%d,%d) = %v,%v want %v,%v", tc.s, tc.e, got, past, tc.want, tc.pastFinale)
			}
		})
	}
}

// seedGuideWorld creates a user with default prefs, two titles (series with
// seasons+airings+provider, movie with provider) in rotation, returning ids.
func seedGuideWorld(t *testing.T, s *Store) (userID, seriesID, movieID int64) {
	t.Helper()
	ctx := context.Background()
	userID = seedUser(t, s)
	seriesID = seedTitle(t, s, "Guide Series")
	movieID = seedTitle(t, s, "Guide Movie")
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("seed exec: %v", err)
		}
	}
	mustExec(`UPDATE titles SET kind='movie', runtime_minutes=120 WHERE id=$1`, movieID)
	mustExec(`UPDATE titles SET runtime_minutes=60, airing=true WHERE id=$1`, seriesID)
	mustExec(`INSERT INTO providers (id, name) VALUES (901,'P901'),(902,'P902') ON CONFLICT DO NOTHING`)
	mustExec(`INSERT INTO title_providers (title_id, region, provider_id) VALUES ($1,'US',901),($2,'US',902),($1,'GB',902)`, seriesID, movieID)
	mustExec(`INSERT INTO title_seasons (title_id, season_number, episode_count) VALUES ($1,1,2),($1,2,2)`, seriesID)
	mustExec(`INSERT INTO title_airings (title_id, season, episode, air_date) VALUES ($1,1,1,'2026-01-01'),($1,1,2,'2026-01-06')`, seriesID)
	for _, tid := range []int64{seriesID, movieID} {
		if _, err := s.UpsertEntry(ctx, userID, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}
	return userID, seriesID, movieID
}

func TestGuideInputTitles(t *testing.T) {
	s := testStore(t)
	uid, seriesID, movieID := seedGuideWorld(t, s)

	titles, err := s.GuideInputTitles(context.Background(), uid, "US")
	if err != nil {
		t.Fatalf("GuideInputTitles: %v", err)
	}
	if len(titles) != 2 {
		t.Fatalf("titles = %d, want 2", len(titles))
	}
	var ser, mov *guide.Title
	for i := range titles {
		switch titles[i].ID {
		case seriesID:
			ser = &titles[i]
		case movieID:
			mov = &titles[i]
		}
	}
	if ser == nil || mov == nil {
		t.Fatalf("missing titles: %+v", titles)
	}
	if ser.Kind != "series" || !ser.Airing || ser.Runtime != 60 ||
		len(ser.Providers) != 1 || ser.Providers[0] != 901 ||
		ser.SeasonEpisodes[1] != 2 || ser.SeasonEpisodes[2] != 2 ||
		len(ser.AirDates) != 2 || ser.Pointer != (guide.Pointer{Season: 1, Episode: 1}) {
		t.Fatalf("series hydration = %+v", ser)
	}
	if mov.Kind != "movie" || mov.Runtime != 120 || len(mov.Providers) != 1 || mov.Providers[0] != 902 || len(mov.AirDates) != 0 {
		t.Fatalf("movie hydration = %+v", mov)
	}

	// Region filter: GB drops the movie's provider and swaps the series'.
	gb, err := s.GuideInputTitles(context.Background(), uid, "GB")
	if err != nil {
		t.Fatalf("GB: %v", err)
	}
	for _, tt := range gb {
		if tt.ID == movieID && len(tt.Providers) != 0 {
			t.Fatalf("GB movie providers = %v, want none", tt.Providers)
		}
		if tt.ID == seriesID && (len(tt.Providers) != 1 || tt.Providers[0] != 902) {
			t.Fatalf("GB series providers = %v, want [902]", tt.Providers)
		}
	}
}

func TestGuideLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)

	items := []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: true},
	}
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 42, items)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.Seed != 42 || g.StartDate != "2026-01-05" || len(g.Items) != 2 || g.Items[0].Date != "2026-01-05" {
		t.Fatalf("created guide = %+v", g)
	}

	// Overlapping create replaces the first guide.
	g2, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-08", "2026-01-14", 43, nil)
	if err != nil {
		t.Fatalf("overlap create: %v", err)
	}
	if _, err := s.GuideWithItems(ctx, uid, g.ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("old guide err = %v, want ErrGuideNotFound", err)
	}

	// Current: today inside g2's range finds it; after end_date, 404.
	cur, err := s.CurrentGuide(ctx, uid, "2026-01-10")
	if err != nil || cur.ID != g2.ID {
		t.Fatalf("current = %+v, %v", cur, err)
	}
	if _, err := s.CurrentGuide(ctx, uid, "2026-02-01"); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("stale current err = %v", err)
	}

	// Ownership isolation.
	other := seedUser(t, s)
	if _, err := s.GuideWithItems(ctx, other, g2.ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign guide err = %v", err)
	}
}

func TestReplaceUnkeptItems(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true, Pinned: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	keepID := g.Items[0].ID
	got, err := s.ReplaceUnkeptItems(ctx, uid, g.ID, []int64{keepID}, []guide.Item{
		{Date: "2026-01-07", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2 (keep + new)", len(got.Items))
	}
	if got.Items[0].ID != keepID || got.Items[1].Season != 2 {
		t.Fatalf("replace result = %+v", got.Items)
	}
	if _, err := s.ReplaceUnkeptItems(ctx, seedUser(t, s), g.ID, nil, nil); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign replace err = %v", err)
	}
}

func TestUpdateDeleteGuideItem(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)
	g, _ := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	itemID := g.Items[0].ID

	// Move preserves duration and sets edited.
	ns := 1200
	nd := "2026-01-07"
	it, err := s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{Date: &nd, StartMin: &ns, SetEdited: true})
	if err != nil || it.Date != nd || it.StartMin != 1200 || it.EndMin != 1260 || !it.Edited {
		t.Fatalf("move = %+v, %v", it, err)
	}

	// Swap: duration from new runtime, season/episode overridden, edited stays.
	dur := 120
	sz, ez := 0, 0
	it, err = s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{TitleID: &movieID, Season: &sz, Episode: &ez, DurationMin: &dur, SetEdited: true})
	if err != nil || it.TitleID != movieID || it.EndMin != 1200+120 || it.Season != 0 {
		t.Fatalf("swap = %+v, %v", it, err)
	}

	// Pin-only leaves edited semantics to the handler: SetEdited=false must not clear it.
	pt := true
	it, err = s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{Pinned: &pt})
	if err != nil || !it.Pinned || !it.Edited {
		t.Fatalf("pin = %+v, %v", it, err)
	}

	if _, err := s.UpdateGuideItem(ctx, seedUser(t, s), g.ID, itemID, GuideItemUpdate{Pinned: &pt}); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign update err = %v", err)
	}
	if err := s.DeleteGuideItem(ctx, uid, g.ID, itemID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeleteGuideItem(ctx, uid, g.ID, itemID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("re-delete err = %v", err)
	}
}

func TestMarkItemWatched(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // series: S1=2 eps, S2=2 eps

	g, _ := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-07", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: true},
	})

	// S1E2 watched: rollover to S2E1.
	it, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
	if err != nil || !it.Watched {
		t.Fatalf("watch rollover = %+v, %v", it, err)
	}
	me := entryOf(t, s, uid, seriesID)
	if me.Pointer != (Pointer{Season: 2, Episode: 1}) || me.Status != "rotation" {
		t.Fatalf("after rollover entry = %+v", me)
	}

	// S2E2 (finale) watched: auto-complete to watched shelf.
	if _, err = s.MarkItemWatched(ctx, uid, g.ID, g.Items[1].ID); err != nil {
		t.Fatalf("watch finale: %v", err)
	}
	me = entryOf(t, s, uid, seriesID)
	if me.Status != "watched" || me.WatchedAt == nil {
		t.Fatalf("after finale entry = %+v", me)
	}

	// Movie watched: straight to watched shelf.
	if _, err = s.MarkItemWatched(ctx, uid, g.ID, g.Items[2].ID); err != nil {
		t.Fatalf("watch movie: %v", err)
	}
	if me = entryOf(t, s, uid, movieID); me.Status != "watched" {
		t.Fatalf("movie entry = %+v", me)
	}

	if _, err := s.MarkItemWatched(ctx, seedUser(t, s), g.ID, g.Items[0].ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign watch err = %v", err)
	}
}

// entryOf fetches one entry via the shelf queries (any shelf) for asserts.
func entryOf(t *testing.T, s *Store, userID, titleID int64) *Entry {
	t.Helper()
	for _, shelf := range []string{"rotation", "watchlist", "watched"} {
		es, err := s.Shelf(context.Background(), userID, shelf)
		if err != nil {
			t.Fatalf("shelf: %v", err)
		}
		for i := range es {
			if es[i].TitleID == titleID {
				return &es[i]
			}
		}
	}
	t.Fatalf("entry for title %d not found", titleID)
	return nil
}

func TestSwapTitle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)

	info, err := s.SwapTitle(ctx, uid, seriesID)
	if err != nil || info.Kind != "series" || info.Runtime != 60 || info.PointerSeason != 1 {
		t.Fatalf("swap info = %+v, %v", info, err)
	}
	// A title not on rotation/watchlist is invalid.
	stray := seedTitle(t, s, "Stray")
	if _, err := s.SwapTitle(ctx, uid, stray); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("stray swap err = %v", err)
	}
}
```

Also add JSON field note: `Entry.Pointer` from #12 is reused for asserts.

- [ ] **Step 2: FAIL run** — with `TEST_DATABASE_URL` (lineup-pg on :5433): undefined symbols; without: must still compile after implementation and skip.

- [ ] **Step 3: Implement `api/internal/store/guides.go`**

```go
// api/internal/store/guides.go
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shottah/lineup/api/internal/guide"
)

// ErrGuideNotFound is returned for guides/items that don't exist or belong
// to another user (indistinguishable by design).
var ErrGuideNotFound = errors.New("store: guide not found")

// Guide mirrors a guides row plus its items.
type Guide struct {
	ID        int64       `json:"id"`
	StartDate string      `json:"start_date"`
	EndDate   string      `json:"end_date"`
	Seed      int64       `json:"seed"`
	Items     []GuideItem `json:"items"`
}

// GuideItem mirrors a guide_items row.
type GuideItem struct {
	ID         int64  `json:"id"`
	Date       string `json:"date"`
	StartMin   int    `json:"start_min"`
	EndMin     int    `json:"end_min"`
	TitleID    int64  `json:"title_id"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	ProviderID int64  `json:"provider_id"`
	IsPlan     bool   `json:"is_plan"`
	Pinned     bool   `json:"pinned"`
	Edited     bool   `json:"edited"`
	Watched    bool   `json:"watched"`
}

// GuideItemUpdate carries PATCH semantics; nil = unchanged. DurationMin
// resizes the item (end_min = start_min + duration); when nil the current
// duration is preserved across moves. SetEdited ORs into edited.
type GuideItemUpdate struct {
	Date        *string
	StartMin    *int
	DurationMin *int
	TitleID     *int64
	Season      *int
	Episode     *int
	Pinned      *bool
	SetEdited   bool
}

// SwapInfo describes a swap-eligible title (rotation or watchlist).
type SwapInfo struct {
	TitleID        int64
	Kind           string
	Runtime        int
	PointerSeason  int
	PointerEpisode int
}

// nextPointer returns the pointer following the watched episode, rolling
// seasons via counts; pastFinale reports rolling off the last known season
// (the returned pointer then parks on the watched episode itself).
func nextPointer(itemSeason, itemEpisode int, counts map[int]int) (Pointer, bool) {
	cnt, ok := counts[itemSeason]
	if ok && itemEpisode < cnt {
		return Pointer{Season: itemSeason, Episode: itemEpisode + 1}, false
	}
	if _, next := counts[itemSeason+1]; next {
		return Pointer{Season: itemSeason + 1, Episode: 1}, false
	}
	return Pointer{Season: itemSeason, Episode: itemEpisode}, true
}

const guideItemCols = `gi.id, to_char(gi.date,'YYYY-MM-DD'), gi.start_min, gi.end_min, gi.title_id,
       gi.season, gi.episode, gi.provider_id, gi.is_plan, gi.pinned, gi.edited, gi.watched`

func scanGuideItem(row pgx.Row) (*GuideItem, error) {
	it := &GuideItem{}
	err := row.Scan(&it.ID, &it.Date, &it.StartMin, &it.EndMin, &it.TitleID,
		&it.Season, &it.Episode, &it.ProviderID, &it.IsPlan, &it.Pinned, &it.Edited, &it.Watched)
	if err != nil {
		return nil, err
	}
	return it, nil
}

// GuideInputTitles hydrates the user's rotation into engine titles with
// region-filtered providers, season counts, and airings.
func (s *Store) GuideInputTitles(ctx context.Context, userID int64, region string) ([]guide.Title, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT t.id, t.kind, t.name, t.runtime_minutes, t.airing, ut.pointer_season, ut.pointer_episode
FROM user_titles ut JOIN titles t ON t.id = ut.title_id
WHERE ut.user_id = $1 AND ut.status = 'rotation'
ORDER BY t.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: guide input titles: %w", err)
	}
	defer rows.Close()

	titles := []guide.Title{}
	idx := map[int64]int{}
	var ids []int64
	for rows.Next() {
		var t guide.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.Runtime, &t.Airing, &t.Pointer.Season, &t.Pointer.Episode); err != nil {
			return nil, fmt.Errorf("store: guide input titles: scan: %w", err)
		}
		t.SeasonEpisodes = map[int]int{}
		idx[t.ID] = len(titles)
		ids = append(ids, t.ID)
		titles = append(titles, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input titles: rows: %w", err)
	}
	if len(titles) == 0 {
		return titles, nil
	}

	prows, err := s.Pool.Query(ctx, `
SELECT title_id, provider_id FROM title_providers
WHERE region = $1 AND title_id = ANY($2) ORDER BY title_id, provider_id`, region, ids)
	if err != nil {
		return nil, fmt.Errorf("store: guide input providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var tid, pid int64
		if err := prows.Scan(&tid, &pid); err != nil {
			return nil, fmt.Errorf("store: guide input providers: scan: %w", err)
		}
		i := idx[tid]
		titles[i].Providers = append(titles[i].Providers, pid)
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input providers: rows: %w", err)
	}

	srows, err := s.Pool.Query(ctx, `
SELECT title_id, season_number, episode_count FROM title_seasons WHERE title_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: guide input seasons: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var tid int64
		var sn, ec int
		if err := srows.Scan(&tid, &sn, &ec); err != nil {
			return nil, fmt.Errorf("store: guide input seasons: scan: %w", err)
		}
		titles[idx[tid]].SeasonEpisodes[sn] = ec
	}
	if err := srows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input seasons: rows: %w", err)
	}

	arows, err := s.Pool.Query(ctx, `
SELECT title_id, season, episode, to_char(air_date,'YYYY-MM-DD')
FROM title_airings WHERE title_id = ANY($1) ORDER BY title_id, season, episode`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: guide input airings: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var tid int64
		var a guide.AiredEpisode
		if err := arows.Scan(&tid, &a.Season, &a.Episode, &a.Date); err != nil {
			return nil, fmt.Errorf("store: guide input airings: scan: %w", err)
		}
		i := idx[tid]
		titles[i].AirDates = append(titles[i].AirDates, a)
	}
	if err := arows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input airings: rows: %w", err)
	}
	return titles, nil
}

func insertGuideItems(ctx context.Context, tx pgx.Tx, guideID int64, items []guide.Item) error {
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
INSERT INTO guide_items (guide_id, date, start_min, end_min, title_id, season, episode, provider_id, is_plan, pinned, edited, watched)
VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			guideID, it.Date, it.StartMin, it.EndMin, it.TitleID, it.Season, it.Episode, it.Provider,
			it.IsPlan, it.Pinned, it.Edited, it.Watched); err != nil {
			return fmt.Errorf("store: insert guide item: %w", err)
		}
	}
	return nil
}

// CreateGuideReplacingOverlaps atomically replaces any of the user's
// guides overlapping [startDate, endDate] with a fresh one.
func (s *Store) CreateGuideReplacingOverlaps(ctx context.Context, userID int64, startDate, endDate string, seed int64, items []guide.Item) (*Guide, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: create guide: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
DELETE FROM guides WHERE user_id = $1 AND start_date <= $3::date AND end_date >= $2::date`,
		userID, startDate, endDate); err != nil {
		return nil, fmt.Errorf("store: create guide: replace overlaps: %w", err)
	}
	var gid int64
	if err := tx.QueryRow(ctx, `
INSERT INTO guides (user_id, start_date, end_date, seed) VALUES ($1, $2::date, $3::date, $4) RETURNING id`,
		userID, startDate, endDate, seed).Scan(&gid); err != nil {
		return nil, fmt.Errorf("store: create guide: insert: %w", err)
	}
	if err := insertGuideItems(ctx, tx, gid, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: create guide: commit: %w", err)
	}
	return s.GuideWithItems(ctx, userID, gid)
}

// GuideWithItems loads one of the user's guides with items in guide order.
func (s *Store) GuideWithItems(ctx context.Context, userID, guideID int64) (*Guide, error) {
	g := &Guide{}
	err := s.Pool.QueryRow(ctx, `
SELECT id, to_char(start_date,'YYYY-MM-DD'), to_char(end_date,'YYYY-MM-DD'), seed
FROM guides WHERE id = $2 AND user_id = $1`, userID, guideID).
		Scan(&g.ID, &g.StartDate, &g.EndDate, &g.Seed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: guide: %w", err)
	}
	rows, err := s.Pool.Query(ctx, `
SELECT `+guideItemCols+` FROM guide_items gi
WHERE gi.guide_id = $1 ORDER BY gi.date, gi.start_min, gi.is_plan DESC, gi.title_id`, guideID)
	if err != nil {
		return nil, fmt.Errorf("store: guide items: %w", err)
	}
	defer rows.Close()
	g.Items = []GuideItem{}
	for rows.Next() {
		it, serr := scanGuideItem(rows)
		if serr != nil {
			return nil, fmt.Errorf("store: guide items: scan: %w", serr)
		}
		g.Items = append(g.Items, *it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide items: rows: %w", err)
	}
	return g, nil
}

// CurrentGuide returns the newest guide still covering today.
func (s *Store) CurrentGuide(ctx context.Context, userID int64, today string) (*Guide, error) {
	var gid int64
	err := s.Pool.QueryRow(ctx, `
SELECT id FROM guides WHERE user_id = $1 AND end_date >= $2::date
ORDER BY created_at DESC, id DESC LIMIT 1`, userID, today).Scan(&gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: current guide: %w", err)
	}
	return s.GuideWithItems(ctx, userID, gid)
}

// ReplaceUnkeptItems deletes the guide's items not named in keepIDs and
// inserts newItems, atomically.
func (s *Store) ReplaceUnkeptItems(ctx context.Context, userID, guideID int64, keepIDs []int64, newItems []guide.Item) (*Guide, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: replace items: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var owned int64
	err = tx.QueryRow(ctx, `SELECT id FROM guides WHERE id = $2 AND user_id = $1 FOR UPDATE`, userID, guideID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: replace items: ownership: %w", err)
	}
	if keepIDs == nil {
		keepIDs = []int64{}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM guide_items WHERE guide_id = $1 AND NOT (id = ANY($2))`, guideID, keepIDs); err != nil {
		return nil, fmt.Errorf("store: replace items: delete: %w", err)
	}
	if err := insertGuideItems(ctx, tx, guideID, newItems); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: replace items: commit: %w", err)
	}
	return s.GuideWithItems(ctx, userID, guideID)
}

// UpdateGuideItem applies move/swap/pin semantics; moves preserve duration
// unless DurationMin overrides it.
func (s *Store) UpdateGuideItem(ctx context.Context, userID, guideID, itemID int64, upd GuideItemUpdate) (*GuideItem, error) {
	q := `
UPDATE guide_items gi SET
  date      = COALESCE($4::date, gi.date),
  start_min = COALESCE($5, gi.start_min),
  end_min   = COALESCE($5, gi.start_min) + COALESCE($6, gi.end_min - gi.start_min),
  title_id  = COALESCE($7, gi.title_id),
  season    = COALESCE($8, gi.season),
  episode   = COALESCE($9, gi.episode),
  pinned    = COALESCE($10, gi.pinned),
  edited    = gi.edited OR $11
FROM guides g
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.id = gi.guide_id AND g.user_id = $1
RETURNING ` + guideItemCols
	it, err := scanGuideItem(s.Pool.QueryRow(ctx, q, userID, guideID, itemID,
		upd.Date, upd.StartMin, upd.DurationMin, upd.TitleID, upd.Season, upd.Episode, upd.Pinned, upd.SetEdited))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: update guide item: %w", err)
	}
	return it, nil
}

// DeleteGuideItem removes one item, ownership-scoped.
func (s *Store) DeleteGuideItem(ctx context.Context, userID, guideID, itemID int64) error {
	tag, err := s.Pool.Exec(ctx, `
DELETE FROM guide_items gi USING guides g
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.id = gi.guide_id AND g.user_id = $1`, userID, guideID, itemID)
	if err != nil {
		return fmt.Errorf("store: delete guide item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGuideNotFound
	}
	return nil
}

// MarkItemWatched flags the item watched and advances the series pointer
// (season rollover via title_seasons; past-finale auto-completes the title
// to the watched shelf). Movies auto-complete immediately. The pointer
// never regresses when old episodes are re-watched.
func (s *Store) MarkItemWatched(ctx context.Context, userID, guideID, itemID int64) (*GuideItem, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: watch item: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var titleID int64
	var season, episode int
	var kind string
	err = tx.QueryRow(ctx, `
SELECT gi.title_id, gi.season, gi.episode, t.kind
FROM guide_items gi JOIN guides g ON g.id = gi.guide_id JOIN titles t ON t.id = gi.title_id
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.user_id = $1
FOR UPDATE OF gi`, userID, guideID, itemID).Scan(&titleID, &season, &episode, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: watch item: load: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE guide_items SET watched = true WHERE id = $1`, itemID); err != nil {
		return nil, fmt.Errorf("store: watch item: flag: %w", err)
	}

	if kind == "movie" {
		if _, err := tx.Exec(ctx, `
UPDATE user_titles SET status = 'watched', watched_at = now(), updated_at = now()
WHERE user_id = $1 AND title_id = $2`, userID, titleID); err != nil {
			return nil, fmt.Errorf("store: watch item: movie complete: %w", err)
		}
	} else {
		var ps, pe int
		err = tx.QueryRow(ctx, `
SELECT pointer_season, pointer_episode FROM user_titles
WHERE user_id = $1 AND title_id = $2 FOR UPDATE`, userID, titleID).Scan(&ps, &pe)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("store: watch item: pointer: %w", err)
		}
		if err == nil && (season > ps || (season == ps && episode >= pe)) {
			counts := map[int]int{}
			crows, cerr := tx.Query(ctx, `SELECT season_number, episode_count FROM title_seasons WHERE title_id = $1`, titleID)
			if cerr != nil {
				return nil, fmt.Errorf("store: watch item: seasons: %w", cerr)
			}
			for crows.Next() {
				var sn, ec int
				if cerr := crows.Scan(&sn, &ec); cerr != nil {
					crows.Close()
					return nil, fmt.Errorf("store: watch item: seasons scan: %w", cerr)
				}
				counts[sn] = ec
			}
			crows.Close()
			if cerr := crows.Err(); cerr != nil {
				return nil, fmt.Errorf("store: watch item: seasons rows: %w", cerr)
			}

			next, pastFinale := nextPointer(season, episode, counts)
			if pastFinale {
				if _, err := tx.Exec(ctx, `
UPDATE user_titles SET status = 'watched', watched_at = now(),
  pointer_season = $3, pointer_episode = $4, updated_at = now()
WHERE user_id = $1 AND title_id = $2`, userID, titleID, next.Season, next.Episode); err != nil {
					return nil, fmt.Errorf("store: watch item: series complete: %w", err)
				}
			} else {
				if _, err := tx.Exec(ctx, `
UPDATE user_titles SET pointer_season = $3, pointer_episode = $4, updated_at = now()
WHERE user_id = $1 AND title_id = $2`, userID, titleID, next.Season, next.Episode); err != nil {
					return nil, fmt.Errorf("store: watch item: advance: %w", err)
				}
			}
		}
	}

	it, err := scanGuideItem(tx.QueryRow(ctx, `SELECT `+guideItemCols+` FROM guide_items gi WHERE gi.id = $1`, itemID))
	if err != nil {
		return nil, fmt.Errorf("store: watch item: reload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: watch item: commit: %w", err)
	}
	return it, nil
}

// SwapTitle validates a swap target (rotation or watchlist) and returns
// what the handler needs to rewrite the item.
func (s *Store) SwapTitle(ctx context.Context, userID, titleID int64) (*SwapInfo, error) {
	info := &SwapInfo{}
	err := s.Pool.QueryRow(ctx, `
SELECT t.id, t.kind, t.runtime_minutes, ut.pointer_season, ut.pointer_episode
FROM user_titles ut JOIN titles t ON t.id = ut.title_id
WHERE ut.user_id = $1 AND ut.title_id = $2 AND ut.status IN ('rotation','watchlist')`,
		userID, titleID).Scan(&info.TitleID, &info.Kind, &info.Runtime, &info.PointerSeason, &info.PointerEpisode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTitleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: swap title: %w", err)
	}
	return info, nil
}
```

- [ ] **Step 4: PASS run (both modes)** — with TEST_DATABASE_URL: all guide store tests green; without: skip; `go vet ./...`.
- [ ] **Step 5: Commit** — `git add api/internal/store/ && git commit -m "feat(api): guide persistence, hydration, watched pointer rollover"`

### Task 3: handlers, routing, wiring

**Files:**
- Create: `api/internal/httpserver/guides.go`
- Create: `api/internal/httpserver/guides_test.go`
- Modify: `api/internal/httpserver/server.go` (Deps.Guides, Deps.Now, routes)
- Modify: `api/cmd/api/main.go` (Guides: st; Now stays default)

**Interfaces:**
- `GuideStore` interface: the nine Task 2 method signatures verbatim.
- `Deps` gains `Guides GuideStore` and `Now func() time.Time` (New defaults nil Now to time.Now).
- Routes (inside the `/v1` auth group, mounted when `d.Guides != nil`):
  `POST /guides`, `GET /guides/current`, `POST /guides/{id}/regenerate`,
  `PATCH /guides/{id}/items/{itemID}`, `DELETE /guides/{id}/items/{itemID}`,
  `POST /guides/{id}/items/{itemID}/watched`.

- [ ] **Step 1: Write the failing tests** — hermetic, fake GuideStore + fixed clock (`2026-01-08` mid-guide). The fake records calls and mirrors just enough semantics; full behavior lives in Task 2's integration tests. Cover:
  - create: 400 malformed; 422 bad date/days 0/days 15; happy path → guide JSON echoed, seed = fixed clock nanos, days built from the fake user's default prefs (all 7 enabled), CreateGuideReplacingOverlaps received start/end dates `start + days-1`.
  - current: found → 200; fake returns ErrGuideNotFound → 404.
  - regenerate: fake guide with 5 items (pinned / edited / watched / past-dated / future-unpinned); assert ReplaceUnkeptItems got exactly the first four as keepIDs and that newItems excludes echoed keeps; foreign guide → 404.
  - patch: pin-only → SetEdited false; move → SetEdited true + Date/StartMin passed; swap → SwapTitle consulted, DurationMin = swap runtime, Season/Episode = pointer (series case) and 0/0 (movie case), SetEdited true; swap target rejected (fake returns ErrTitleNotFound) → 422 `invalid title`; empty body object → 422 `nothing to update`; bad start_min (-1, 1441) → 422; unknown item → 404.
  - delete: 204; unknown → 404.
  - watched: 200 with the fake's updated item; unknown → 404.

Write the tests fully in the brief style of prior tasks (table-driven where natural). The fake's `Now` assertions use `time.Date(2026, 1, 8, 12, 0, 0, 0, time.UTC)`.

- [ ] **Step 2: FAIL run.**

- [ ] **Step 3: Implement `guides.go`** — handlers exactly per the spec:

```go
// api/internal/httpserver/guides.go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/guide"
	"github.com/shottah/lineup/api/internal/prefs"
	"github.com/shottah/lineup/api/internal/store"
)

// GuideStore is the slice of *store.Store the guide handlers need.
type GuideStore interface {
	GuideInputTitles(ctx context.Context, userID int64, region string) ([]guide.Title, error)
	CreateGuideReplacingOverlaps(ctx context.Context, userID int64, startDate, endDate string, seed int64, items []guide.Item) (*store.Guide, error)
	CurrentGuide(ctx context.Context, userID int64, today string) (*store.Guide, error)
	GuideWithItems(ctx context.Context, userID, guideID int64) (*store.Guide, error)
	ReplaceUnkeptItems(ctx context.Context, userID, guideID int64, keepIDs []int64, newItems []guide.Item) (*store.Guide, error)
	UpdateGuideItem(ctx context.Context, userID, guideID, itemID int64, upd store.GuideItemUpdate) (*store.GuideItem, error)
	DeleteGuideItem(ctx context.Context, userID, guideID, itemID int64) error
	MarkItemWatched(ctx context.Context, userID, guideID, itemID int64) (*store.GuideItem, error)
	SwapTitle(ctx context.Context, userID, titleID int64) (*store.SwapInfo, error)
}

const dateFmt = "2006-01-02"

// guideDays maps a start date + length onto the user's weekday windows.
func guideDays(start time.Time, days int, windows map[string]prefs.ParsedWindow) []guide.Day {
	out := make([]guide.Day, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		key := strings.ToLower(d.Weekday().String()[:3])
		w := guide.Window{}
		if pw, ok := windows[key]; ok && pw.Enabled {
			w = guide.Window{StartMin: pw.StartMin, EndMin: pw.EndMin}
		}
		out[i] = guide.Day{Date: d.Format(dateFmt), Window: w}
	}
	return out
}

func (d Deps) buildInput(ctx context.Context, u *store.User, start time.Time, days int, seed int64, keep []guide.Item) (guide.Input, error) {
	windows, err := prefs.Windows(u.SchedulePrefs)
	if err != nil {
		return guide.Input{}, err
	}
	titles, err := d.Guides.GuideInputTitles(ctx, u.ID, u.Region)
	if err != nil {
		return guide.Input{}, err
	}
	return guide.Input{Seed: seed, Days: guideDays(start, days, windows), Titles: titles, Keep: keep}, nil
}

func handleCreateGuide(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			StartDate string `json:"start_date"`
			Days      int    `json:"days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		start, err := time.Parse(dateFmt, body.StartDate)
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid start_date")
			return
		}
		if body.Days < 1 || body.Days > 14 {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid days")
			return
		}
		u := userFrom(r.Context())
		seed := d.Now().UnixNano()
		in, err := d.buildInput(r.Context(), u, start, body.Days, seed, nil)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		items := guide.Generate(in)
		end := start.AddDate(0, 0, body.Days-1).Format(dateFmt)
		g, err := d.Guides.CreateGuideReplacingOverlaps(r.Context(), u.ID, body.StartDate, end, seed, items)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(g)
	}
}

func handleCurrentGuide(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		g, err := d.Guides.CurrentGuide(r.Context(), u.ID, d.Now().UTC().Format(dateFmt))
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g)
	}
}

func handleRegenerate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok := pathID(r, "id")
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		g, err := d.Guides.GuideWithItems(r.Context(), u.ID, gid)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}

		today := d.Now().UTC().Format(dateFmt)
		var keepIDs []int64
		var keep []guide.Item
		for _, it := range g.Items {
			if it.Pinned || it.Edited || it.Watched || it.Date < today {
				keepIDs = append(keepIDs, it.ID)
				keep = append(keep, guide.Item{Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin,
					TitleID: it.TitleID, Season: it.Season, Episode: it.Episode, Provider: it.ProviderID,
					IsPlan: it.IsPlan, Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched})
			}
		}

		start, err := time.Parse(dateFmt, g.StartDate)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		end, err := time.Parse(dateFmt, g.EndDate)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		days := int(end.Sub(start).Hours()/24) + 1

		in, err := d.buildInput(r.Context(), u, start, days, g.Seed, keep)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		out := guide.Generate(in)

		kept := map[guide.Item]bool{}
		for _, k := range keep {
			kept[k] = true
		}
		newItems := []guide.Item{}
		for _, it := range out {
			if !kept[it] {
				newItems = append(newItems, it)
			}
		}

		refreshed, err := d.Guides.ReplaceUnkeptItems(r.Context(), u.ID, gid, keepIDs, newItems)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(refreshed)
	}
}

func handlePatchItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		var body struct {
			Date     *string `json:"date"`
			StartMin *int    `json:"start_min"`
			TitleID  *int64  `json:"title_id"`
			Pinned   *bool   `json:"pinned"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Date == nil && body.StartMin == nil && body.TitleID == nil && body.Pinned == nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "nothing to update")
			return
		}
		if body.Date != nil {
			if _, err := time.Parse(dateFmt, *body.Date); err != nil {
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid date")
				return
			}
		}
		if body.StartMin != nil && (*body.StartMin < 0 || *body.StartMin > 1440) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid start_min")
			return
		}

		u := userFrom(r.Context())
		upd := store.GuideItemUpdate{Date: body.Date, StartMin: body.StartMin, Pinned: body.Pinned,
			SetEdited: body.Date != nil || body.StartMin != nil || body.TitleID != nil}

		if body.TitleID != nil {
			info, err := d.Guides.SwapTitle(r.Context(), u.ID, *body.TitleID)
			switch {
			case errors.Is(err, store.ErrTitleNotFound):
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid title")
				return
			case err != nil:
				writeJSONError(w, http.StatusInternalServerError, "internal")
				return
			}
			upd.TitleID = &info.TitleID
			upd.DurationMin = &info.Runtime
			season, episode := 0, 0
			if info.Kind == "series" {
				season, episode = info.PointerSeason, info.PointerEpisode
			}
			upd.Season, upd.Episode = &season, &episode
		}

		it, err := d.Guides.UpdateGuideItem(r.Context(), u.ID, gid, itemID, upd)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(it)
	}
}

func handleDeleteItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		err := d.Guides.DeleteGuideItem(r.Context(), u.ID, gid, itemID)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleWatchItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		it, err := d.Guides.MarkItemWatched(r.Context(), u.ID, gid, itemID)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(it)
	}
}

// pathID parses a positive int64 chi URL param.
func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil && id >= 1
}
```

`server.go`: add `Guides GuideStore` and `Now func() time.Time` to Deps; in `New`, `if d.Now == nil { d.Now = time.Now }`; inside the `/v1` group:

```go
			if d.Guides != nil {
				v1.Post("/guides", handleCreateGuide(d))
				v1.Get("/guides/current", handleCurrentGuide(d))
				v1.Route("/guides/{id}", func(gr chi.Router) {
					gr.Post("/regenerate", handleRegenerate(d))
					gr.Patch("/items/{itemID}", handlePatchItem(d))
					gr.Delete("/items/{itemID}", handleDeleteItem(d))
					gr.Post("/items/{itemID}/watched", handleWatchItem(d))
				})
			}
```

`main.go`: `Guides: st` added to Deps.

- [ ] **Step 4: Full suite both modes** — `cd api && go vet ./... && go test ./...`.
- [ ] **Step 5: Commit** — `git add api/ && git commit -m "feat(api): guide endpoints"`

### Task 4: PR (controller-inline)

- [ ] Clean-state vet + full suite (+ integration with TEST_DATABASE_URL, `-race` on new packages); push; PR `feat(api): guide endpoints` closing #14, noting the (†) decisions (store→guide import, clock seam, pointer-no-regress, pre-#11 empty guides, UTC today); CI; user merges.

---

## Self-review notes

- Acceptance ↔ tests: ownership (store TestGuideLifecycle/handler 404 cases), regenerate keep-set (handler keep assembly + store ReplaceUnkeptItems), pointer rollover + auto-complete (TestNextPointer pure + TestMarkItemWatched integration), swap end_min recompute (TestUpdateDeleteGuideItem + handler swap case).
- Signatures consistent: GuideStore mirrors Task 2 exactly; guideID threaded through all item ops so the path's {id} is enforced.
- `guide.Item` comparability relied on for keep filtering — all fields are comparable (strings/ints/bools).
- UpdateGuideItem end_min expression: `COALESCE(new_start, old_start) + COALESCE(duration, old_duration)` covers move (preserve), swap (override), and combined move+swap in one statement.
