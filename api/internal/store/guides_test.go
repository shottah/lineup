package store

import (
	"context"
	"errors"
	"reflect"
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

// TestGuideInputTitlesDefaultRuntime covers titles TMDB reports without a
// runtime (episode_run_time omitted for many current shows): a zero
// runtime_minutes must be hydrated to a sane default rather than passed
// through as 0, which would collapse the scheduler onto the window start.
func TestGuideInputTitlesDefaultRuntime(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	seriesID := seedTitle(t, s, "Zero Runtime Series")
	movieID := seedTitle(t, s, "Zero Runtime Movie")
	if _, err := s.Pool.Exec(ctx, `UPDATE titles SET kind='movie', runtime_minutes=0 WHERE id=$1`, movieID); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE titles SET runtime_minutes=0 WHERE id=$1`, seriesID); err != nil {
		t.Fatalf("seed series: %v", err)
	}
	for _, tid := range []int64{seriesID, movieID} {
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}

	titles, err := s.GuideInputTitles(ctx, uid, "US")
	if err != nil {
		t.Fatalf("GuideInputTitles: %v", err)
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
	if ser.Runtime != 45 {
		t.Fatalf("series Runtime = %d, want 45", ser.Runtime)
	}
	if mov.Runtime != 120 {
		t.Fatalf("movie Runtime = %d, want 120", mov.Runtime)
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

	info, err := s.SwapTitle(ctx, uid, seriesID, "US")
	if err != nil || info.Kind != "series" || info.Runtime != 60 || info.PointerSeason != 1 {
		t.Fatalf("swap info = %+v, %v", info, err)
	}
	if !reflect.DeepEqual(info.Providers, []int64{901}) {
		t.Fatalf("swap info providers = %v, want [901]", info.Providers)
	}
	// Region filter applies to swap providers too: GB has 902, not 901.
	gbInfo, err := s.SwapTitle(ctx, uid, seriesID, "GB")
	if err != nil || !reflect.DeepEqual(gbInfo.Providers, []int64{902}) {
		t.Fatalf("GB swap providers = %v, %v, want [902]", gbInfo, err)
	}
	// A title not on rotation/watchlist is invalid.
	stray := seedTitle(t, s, "Stray")
	if _, err := s.SwapTitle(ctx, uid, stray, "US"); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("stray swap err = %v", err)
	}
}

// TestUpdateGuideItemSwapProvider ports the final-review probe reproducing
// the swap-provider-staleness bug: a swap must recompute provider_id from
// the new title's region providers rather than leaving the old title's
// provider in place.
func TestUpdateGuideItemSwapProvider(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // series on US:901, movie on US:902

	// A second series sharing the movie's provider (902) plus a lower id
	// (500), to distinguish "keep the current provider" from "lowest id".
	altSeries := seedTitle(t, s, "Alt Series")
	if _, err := s.Pool.Exec(ctx, `INSERT INTO providers (id, name) VALUES (500,'P500') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed provider 500: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO title_providers (title_id, region, provider_id) VALUES ($1,'US',500),($1,'US',902)`, altSeries); err != nil {
		t.Fatalf("seed alt providers: %v", err)
	}
	if _, err := s.UpsertEntry(ctx, uid, altSeries, EntryUpdate{Status: strp("rotation")}); err != nil {
		t.Fatalf("seed alt rotation: %v", err)
	}
	// A title with no region providers yet: pre-ingestion (#11) reality.
	bareTitle := seedTitle(t, s, "Bare Title")
	if _, err := s.UpsertEntry(ctx, uid, bareTitle, EntryUpdate{Status: strp("rotation")}); err != nil {
		t.Fatalf("seed bare rotation: %v", err)
	}

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	itemID := g.Items[0].ID
	sz, ez := 0, 0

	// Swap to the movie (only provider 902; item currently on 901): the
	// current provider isn't among the movie's, so it falls back to the
	// (only, so also lowest) new provider.
	info, err := s.SwapTitle(ctx, uid, movieID, "US")
	if err != nil || !reflect.DeepEqual(info.Providers, []int64{902}) {
		t.Fatalf("swap info (movie) = %+v, %v, want providers [902]", info, err)
	}
	it, err := s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{
		TitleID: &movieID, Season: &sz, Episode: &ez, SwapProviders: info.Providers, SetEdited: true,
	})
	if err != nil || it.ProviderID != 902 {
		t.Fatalf("swap to movie = %+v, %v, want provider_id 902", it, err)
	}

	// Swap to altSeries (providers [500, 902]; item currently on 902): the
	// current provider streams there too, so it's KEPT over the lower id.
	info, err = s.SwapTitle(ctx, uid, altSeries, "US")
	if err != nil || !reflect.DeepEqual(info.Providers, []int64{500, 902}) {
		t.Fatalf("swap info (altSeries) = %+v, %v, want providers [500 902]", info, err)
	}
	it, err = s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{
		TitleID: &altSeries, Season: &sz, Episode: &ez, SwapProviders: info.Providers, SetEdited: true,
	})
	if err != nil || it.ProviderID != 902 {
		t.Fatalf("swap to altSeries = %+v, %v, want provider_id kept at 902", it, err)
	}

	// Swap to a title with no region providers: provider left unchanged,
	// best effort until #11 populates title_providers.
	info, err = s.SwapTitle(ctx, uid, bareTitle, "US")
	if err != nil || len(info.Providers) != 0 {
		t.Fatalf("swap info (bare) = %+v, %v, want empty providers", info, err)
	}
	it, err = s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{
		TitleID: &bareTitle, Season: &sz, Episode: &ez, SwapProviders: info.Providers, SetEdited: true,
	})
	if err != nil || it.ProviderID != 902 {
		t.Fatalf("swap to bare title = %+v, %v, want provider_id unchanged at 902", it, err)
	}
}

// TestCreateGuideOverlapTouchAdjacency ports the final-review probe on the
// overlap-replacement boundary: a guide starting the day AFTER another ends
// doesn't overlap it (both survive), but one starting ON the other's end
// date touches it and is treated as an overlap (replaced). See
// CreateGuideReplacingOverlaps' `start_date <= end AND end_date >= start`.
func TestCreateGuideOverlapTouchAdjacency(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)

	g1, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, nil)
	if err != nil {
		t.Fatalf("g1: %v", err)
	}
	// Adjacent: starts the day after g1 ends -> no overlap, both survive.
	if _, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-12", "2026-01-18", 2, nil); err != nil {
		t.Fatalf("g2: %v", err)
	}
	if _, err := s.GuideWithItems(ctx, uid, g1.ID); err != nil {
		t.Fatalf("adjacent guide must survive, got %v", err)
	}
	// Touching: starts ON g1's end date -> overlap, g1 replaced.
	if _, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-11", "2026-01-12", 3, nil); err != nil {
		t.Fatalf("g3: %v", err)
	}
	if _, err := s.GuideWithItems(ctx, uid, g1.ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("boundary-touching guide must replace g1, err = %v", err)
	}
}

// TestMarkItemWatchedExactPointerAdvance ports the final-review probe on
// the pointer-advance boundary: watching the episode the pointer sits on
// EXACTLY (not one beyond it) must still advance the pointer by one, and
// re-watching the same item must not advance it again.
func TestMarkItemWatchedExactPointerAdvance(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s) // S1=2 eps, S2=2 eps; pointer starts 1/1

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
		t.Fatalf("watch: %v", err)
	}
	e := entryOf(t, s, uid, seriesID)
	if e.Pointer != (Pointer{Season: 1, Episode: 2}) {
		t.Fatalf("pointer after exact-episode watch = %+v, want 1/2", e.Pointer)
	}
	// Re-watch the same item: pointer must not advance again.
	if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
		t.Fatalf("re-watch: %v", err)
	}
	e = entryOf(t, s, uid, seriesID)
	if e.Pointer != (Pointer{Season: 1, Episode: 2}) {
		t.Fatalf("pointer after re-watch = %+v, want unchanged 1/2", e.Pointer)
	}
}

// TestGuideLookups guards #18: the sidecar dictionaries must resolve every
// distinct title/provider id referenced by a guide's items, with the right
// name/kind/tmdb_id (titles) and name/logo_path (providers) — the data the
// web guide views render without extra round trips.
func TestGuideLookups(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // series on US:901, movie on US:902

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	titles, provs, err := s.GuideLookups(ctx, g.ID)
	if err != nil {
		t.Fatalf("GuideLookups: %v", err)
	}
	if len(titles) != 2 {
		t.Fatalf("titles = %d, want 2: %+v", len(titles), titles)
	}
	if len(provs) != 2 {
		t.Fatalf("providers = %d, want 2: %+v", len(provs), provs)
	}

	var seriesRow Title
	if err := s.Pool.QueryRow(ctx, `SELECT tmdb_id FROM titles WHERE id = $1`, seriesID).Scan(&seriesRow.TMDBID); err != nil {
		t.Fatalf("seeded series tmdb_id: %v", err)
	}
	ser, ok := titles[seriesID]
	if !ok || ser.Name != "Guide Series" || ser.Kind != "series" || ser.TMDBID != seriesRow.TMDBID {
		t.Fatalf("titles[seriesID] = %+v, want name=Guide Series kind=series tmdb_id=%d", ser, seriesRow.TMDBID)
	}
	var movieRow Title
	if err := s.Pool.QueryRow(ctx, `SELECT tmdb_id FROM titles WHERE id = $1`, movieID).Scan(&movieRow.TMDBID); err != nil {
		t.Fatalf("seeded movie tmdb_id: %v", err)
	}
	mov, ok := titles[movieID]
	if !ok || mov.Name != "Guide Movie" || mov.Kind != "movie" || mov.TMDBID != movieRow.TMDBID {
		t.Fatalf("titles[movieID] = %+v, want name=Guide Movie kind=movie tmdb_id=%d", mov, movieRow.TMDBID)
	}

	p901, ok := provs[901]
	if !ok || p901.Name != "P901" {
		t.Fatalf("providers[901] = %+v, want name=P901", p901)
	}
	p902, ok := provs[902]
	if !ok || p902.Name != "P902" {
		t.Fatalf("providers[902] = %+v, want name=P902", p902)
	}

	// An empty/unknown guide id resolves to empty (not nil) maps.
	emptyTitles, emptyProvs, err := s.GuideLookups(ctx, g.ID+999999)
	if err != nil {
		t.Fatalf("GuideLookups (unknown guide): %v", err)
	}
	if len(emptyTitles) != 0 || len(emptyProvs) != 0 {
		t.Fatalf("unknown guide lookups = %+v/%+v, want empty", emptyTitles, emptyProvs)
	}
}
