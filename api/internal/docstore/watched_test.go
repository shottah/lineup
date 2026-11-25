package docstore

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

// entryOf fetches one entry via the shelf queries (any shelf) for asserts.
func entryOf(t *testing.T, s *Store, userID, titleID string) *Entry {
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
	t.Fatalf("entry for title %s not found", titleID)
	return nil
}

// entryExists reports whether users/{uid}/entries/{titleID} exists.
func entryExists(t *testing.T, s *Store, userID, titleID string) bool {
	t.Helper()
	_, err := s.entries(userID).Doc(titleID).Get(context.Background())
	if isNotFound(err) {
		return false
	}
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	return true
}

func TestMarkItemWatched(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // series: S1=2 eps, S2=2 eps

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-07", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// S1E2 watched: rollover to S2E1.
	it, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
	if err != nil || !it.Watched {
		t.Fatalf("watch rollover = %+v, %v", it, err)
	}
	me := entryOf(t, s, uid, seriesID)
	if me.Pointer != (Pointer{Season: 2, Episode: 1}) || me.Status != "rotation" {
		t.Fatalf("after rollover entry = %+v", me)
	}
	// The flag persisted, not just the returned value.
	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil || !got.Items[0].Watched || got.Items[1].Watched {
		t.Fatalf("reload after watch = %+v, %v", got, err)
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
	if me = entryOf(t, s, uid, movieID); me.Status != "watched" || me.WatchedAt == nil {
		t.Fatalf("movie entry = %+v", me)
	}

	if _, err := s.MarkItemWatched(ctx, seedUser(t, s), g.ID, g.Items[0].ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign watch err = %v", err)
	}
	if _, err := s.MarkItemWatched(ctx, uid, g.ID, "nosuchitem"); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("missing item watch err = %v", err)
	}
}

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

// TestUnmarkItemWatched covers the reversal contract: an item's watched
// flag always clears, but the entry pointer/status rollback is conditional
// on this mark's effects still being the latest.
func TestUnmarkItemWatched(t *testing.T) {
	t.Run("round trip restores pointer and status", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s) // S1=2 eps, S2=2 eps; pointer starts 1/1

		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			// Season/episode ahead of the pointer's start (1/1) triggers the
			// advance branch directly and lands on the finale (pastFinale),
			// so this single mark/unmark exercises the status flip too.
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 2, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}

		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
			t.Fatalf("watch: %v", err)
		}
		me := entryOf(t, s, uid, seriesID)
		if me.Status != "watched" || me.WatchedAt == nil || me.Pointer != (Pointer{Season: 2, Episode: 2}) {
			t.Fatalf("after watch (finale) = %+v", me)
		}

		it, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
		if err != nil {
			t.Fatalf("unwatch: %v", err)
		}
		if it.Watched {
			t.Fatalf("unwatched item still flagged watched: %+v", it)
		}
		me = entryOf(t, s, uid, seriesID)
		if me.Status != "rotation" || me.WatchedAt != nil || me.Pointer != (Pointer{Season: 2, Episode: 2}) {
			t.Fatalf("after unwatch = %+v, want status=rotation watched_at=nil pointer=2/2", me)
		}
		got, err := s.GuideWithItems(ctx, uid, g.ID)
		if err != nil || got.Items[0].Watched {
			t.Fatalf("reload after unwatch = %+v, %v", got, err)
		}
	})

	t.Run("pointer moved on since is left untouched", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s) // S1=2 eps, S2=2 eps; pointer starts 1/1

		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
			{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil { // -> pointer 1/2
			t.Fatalf("watch A: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[1].ID); err != nil { // -> pointer 2/1 (rollover)
			t.Fatalf("watch B: %v", err)
		}
		me := entryOf(t, s, uid, seriesID)
		if me.Pointer != (Pointer{Season: 2, Episode: 1}) {
			t.Fatalf("pointer before unmark = %+v, want 2/1", me.Pointer)
		}

		// Unmark A: nextPointer(1,1) = 1/2, which no longer matches the
		// current pointer (2/1) since B's mark advanced past it, so the
		// pointer (and status) must be left alone.
		it, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
		if err != nil {
			t.Fatalf("unmark A: %v", err)
		}
		if it.Watched {
			t.Fatalf("unmarked item A still flagged watched: %+v", it)
		}
		me = entryOf(t, s, uid, seriesID)
		if me.Pointer != (Pointer{Season: 2, Episode: 1}) {
			t.Fatalf("pointer after stale unmark = %+v, want untouched 2/1", me.Pointer)
		}
		if me.Status != "rotation" {
			t.Fatalf("status after stale unmark = %q, want rotation", me.Status)
		}
	})

	t.Run("stale pointer guard from re-ingest still reverts status", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s) // S1=2 eps, S2=2 eps; pointer starts 1/1

		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 2, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
			t.Fatalf("watch finale: %v", err)
		}
		me := entryOf(t, s, uid, seriesID)
		if me.Status != "watched" || me.WatchedAt == nil || me.Pointer != (Pointer{Season: 2, Episode: 2}) {
			t.Fatalf("after watch finale = %+v", me)
		}

		// Simulate a re-ingest widening season 2 between mark and unmark.
		// nextPointer(item.season, item.episode) now recomputes to 2/3
		// instead of the pointer this mark actually stored (2/2), so the
		// pointer-equality guard fails even though status is still
		// 'watched' from this exact mark.
		if err := s.ReplaceSeasons(ctx, seriesID, []SeasonRow{{1, 2}, {2, 3}}); err != nil {
			t.Fatalf("widen season: %v", err)
		}

		it, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
		if err != nil {
			t.Fatalf("unwatch: %v", err)
		}
		if it.Watched {
			t.Fatalf("unwatched item still flagged watched: %+v", it)
		}
		me = entryOf(t, s, uid, seriesID)
		if me.Status != "rotation" || me.WatchedAt != nil {
			t.Fatalf("after unwatch with stale pointer guard = %+v, want status=rotation watched_at=nil (status reverts independent of the pointer match)", me)
		}
		if me.Pointer != (Pointer{Season: 2, Episode: 2}) {
			t.Fatalf("pointer after stale-guard unwatch = %+v, want untouched 2/2", me.Pointer)
		}
	})

	t.Run("finale completed via a different item still reverts status on earlier unmark", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s) // S1=2 eps, S2=2 eps; pointer starts 1/1

		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
			{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 2, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil { // A: S1E1 -> pointer 1/2
			t.Fatalf("watch A: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[1].ID); err != nil { // B: S2E2 finale -> status watched, pointer 2/2
			t.Fatalf("watch finale B: %v", err)
		}
		me := entryOf(t, s, uid, seriesID)
		if me.Status != "watched" || me.WatchedAt == nil || me.Pointer != (Pointer{Season: 2, Episode: 2}) {
			t.Fatalf("after finale via B = %+v", me)
		}

		// Unmark the EARLIER item A. nextPointer(1,1) = 1/2, which does not
		// match the current pointer (2/2), so the pointer stays put -- but
		// status must still revert.
		it, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
		if err != nil {
			t.Fatalf("unmark A: %v", err)
		}
		if it.Watched {
			t.Fatalf("unmarked item A still flagged watched: %+v", it)
		}
		me = entryOf(t, s, uid, seriesID)
		if me.Pointer != (Pointer{Season: 2, Episode: 2}) {
			t.Fatalf("pointer after unmark A = %+v, want untouched 2/2", me.Pointer)
		}
		if me.Status != "rotation" || me.WatchedAt != nil {
			t.Fatalf("status after unmark A = %+v, want rotation/nil (status reverts independent of the stale pointer guard)", me)
		}
	})

	t.Run("movie auto-complete reversal", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, _, movieID := seedGuideWorld(t, s)

		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
			t.Fatalf("watch movie: %v", err)
		}
		me := entryOf(t, s, uid, movieID)
		if me.Status != "watched" || me.WatchedAt == nil {
			t.Fatalf("after watch movie = %+v", me)
		}

		it, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
		if err != nil {
			t.Fatalf("unwatch movie: %v", err)
		}
		if it.Watched {
			t.Fatalf("unwatched movie item still flagged watched: %+v", it)
		}
		me = entryOf(t, s, uid, movieID)
		if me.Status != "rotation" || me.WatchedAt != nil {
			t.Fatalf("after unwatch movie = %+v, want status=rotation watched_at=nil", me)
		}
	})

	t.Run("idempotent double unmark", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)

		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
			t.Fatalf("watch: %v", err)
		}
		if _, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
			t.Fatalf("first unmark: %v", err)
		}
		me := entryOf(t, s, uid, seriesID)
		if me.Pointer != (Pointer{Season: 1, Episode: 1}) || me.Status != "rotation" {
			t.Fatalf("state after first unmark = %+v, want pointer=1/1 status=rotation", me)
		}

		// Second unmark of an already-unwatched item must be a no-op.
		it, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID)
		if err != nil {
			t.Fatalf("second unmark: %v", err)
		}
		if it.Watched {
			t.Fatalf("idempotent unmark result still flagged watched: %+v", it)
		}
		me = entryOf(t, s, uid, seriesID)
		if me.Pointer != (Pointer{Season: 1, Episode: 1}) || me.Status != "rotation" {
			t.Fatalf("state after idempotent unmark = %+v, want unchanged pointer=1/1 status=rotation", me)
		}
	})

	t.Run("foreign guide not found", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); err != nil {
			t.Fatalf("watch: %v", err)
		}
		if _, err := s.UnmarkItemWatched(ctx, seedUser(t, s), g.ID, g.Items[0].ID); !errors.Is(err, ErrGuideNotFound) {
			t.Fatalf("foreign unmark err = %v, want ErrGuideNotFound", err)
		}
	})
}

// TestMarkItemWatchedWithoutEntry: with no entry doc the item still flips
// both ways, and no entry is ever created.
func TestMarkItemWatchedWithoutEntry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)
	for _, tid := range []string{seriesID, movieID} {
		if _, err := s.entries(uid).Doc(tid).Delete(ctx); err != nil {
			t.Fatalf("delete entry: %v", err)
		}
	}
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1260, TitleID: movieID, Provider: 902, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i, tid := range []string{seriesID, movieID} {
		it, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[i].ID)
		if err != nil || !it.Watched {
			t.Fatalf("watch %s = %+v, %v", tid, it, err)
		}
		if entryExists(t, s, uid, tid) {
			t.Fatalf("watch %s created an entry", tid)
		}
		got, err := s.GuideWithItems(ctx, uid, g.ID)
		if err != nil || !got.Items[i].Watched {
			t.Fatalf("reload after watch %s = %+v, %v", tid, got, err)
		}
		it, err = s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[i].ID)
		if err != nil || it.Watched {
			t.Fatalf("unwatch %s = %+v, %v", tid, it, err)
		}
		if entryExists(t, s, uid, tid) {
			t.Fatalf("unwatch %s created an entry", tid)
		}
		got, err = s.GuideWithItems(ctx, uid, g.ID)
		if err != nil || got.Items[i].Watched {
			t.Fatalf("reload after unwatch %s = %+v, %v", tid, got, err)
		}
	}
}

// TestMarkItemWatchedMissingTitle matches the SQL JOIN on titles: an item
// whose title doc is gone is not found, and nothing is written.
func TestMarkItemWatchedMissingTitle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, _, _ := seedGuideWorld(t, s)
	ghost := TitleID("series", uniqueTMDBID())
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: ghost, Season: 1, Episode: 1, Provider: 901, IsPlan: true, Watched: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: ghost, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.UnmarkItemWatched(ctx, uid, g.ID, g.Items[0].ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("unwatch missing title err = %v, want ErrGuideNotFound", err)
	}
	if _, err := s.MarkItemWatched(ctx, uid, g.ID, g.Items[1].ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("watch missing title err = %v, want ErrGuideNotFound", err)
	}
	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil || !got.Items[0].Watched || got.Items[1].Watched {
		t.Fatalf("items changed = %+v, %v", got, err)
	}
}
