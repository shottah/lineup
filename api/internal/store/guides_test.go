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
