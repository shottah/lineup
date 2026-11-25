package docstore

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/shottah/lineup/api/internal/guide"
)

// rawEntry reads an entry doc as stored, for before/after comparisons.
func rawEntry(t *testing.T, s *Store, userID, titleID string) *entryDoc {
	t.Helper()
	snap, err := s.entries(userID).Doc(titleID).Get(context.Background())
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("get entry: %v", err)
	}
	var d entryDoc
	if err := snap.DataTo(&d); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	return &d
}

func TestGuideCrossUserIsolation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	userA, seriesID, movieID := seedGuideWorld(t, s)
	userB := seedUser(t, s)

	g, err := s.CreateGuideReplacingOverlaps(ctx, userA, "2026-03-02", "2026-03-08", 7, []guide.Item{
		{Date: "2026-03-02", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		{Date: "2026-03-03", StartMin: 1140, EndMin: 1260, TitleID: movieID, Provider: 902, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create A's guide: %v", err)
	}
	seriesItem, movieItem := g.Items[0].ID, g.Items[1].ID
	// A's series item is watched, so a leaked unmark would have something
	// to revert and a leaked mark on the movie would complete it.
	if _, err := s.MarkItemWatched(ctx, userA, g.ID, seriesItem); err != nil {
		t.Fatalf("A watch: %v", err)
	}
	before, err := s.GuideWithItems(ctx, userA, g.ID)
	if err != nil {
		t.Fatalf("load A's guide: %v", err)
	}
	beforeSeries, beforeMovie := rawEntry(t, s, userA, seriesID), rawEntry(t, s, userA, movieID)

	pt, nd, ns, dur := true, "2026-03-05", 600, 120
	guideChecks := map[string]error{}
	_, guideChecks["GuideWithItems"] = s.GuideWithItems(ctx, userB, g.ID)
	_, guideChecks["UpdateGuideItem/pin"] = s.UpdateGuideItem(ctx, userB, g.ID, seriesItem, GuideItemUpdate{Pinned: &pt})
	_, guideChecks["UpdateGuideItem/move"] = s.UpdateGuideItem(ctx, userB, g.ID, seriesItem, GuideItemUpdate{Date: &nd, StartMin: &ns, SetEdited: true})
	_, guideChecks["UpdateGuideItem/swap"] = s.UpdateGuideItem(ctx, userB, g.ID, seriesItem, GuideItemUpdate{TitleID: &movieID, DurationMin: &dur, SetEdited: true})
	_, guideChecks["MarkItemWatched"] = s.MarkItemWatched(ctx, userB, g.ID, movieItem)
	_, guideChecks["UnmarkItemWatched"] = s.UnmarkItemWatched(ctx, userB, g.ID, seriesItem)
	_, guideChecks["ReplaceUnkeptItems"] = s.ReplaceUnkeptItems(ctx, userB, g.ID, nil, nil, 99)
	guideChecks["DeleteGuideItem"] = s.DeleteGuideItem(ctx, userB, g.ID, seriesItem)
	// B's "current guide" for a date inside A's range must not be A's guide.
	_, guideChecks["CurrentGuide"] = s.CurrentGuide(ctx, userB, "2026-03-04")
	for op, err := range guideChecks {
		if !errors.Is(err, ErrGuideNotFound) {
			t.Errorf("%s as user B: err = %v, want ErrGuideNotFound", op, err)
		}
	}

	// Title-keyed reads by B must not see A's entries.
	for _, tid := range []string{seriesID, movieID} {
		if _, err := s.SwapTitle(ctx, userB, tid, "US"); !errors.Is(err, ErrTitleNotFound) {
			t.Errorf("SwapTitle(%s) as user B: err = %v, want ErrTitleNotFound", tid, err)
		}
		full, err := s.GetTitleFull(ctx, userB, tid, "US")
		if err != nil || full.Entry != nil {
			t.Errorf("GetTitleFull(%s) as user B = %+v, %v; want title with nil entry", tid, full, err)
		}
	}
	if n, err := s.CountRotation(ctx, userB, seriesID); err != nil || n != 0 {
		t.Errorf("CountRotation as user B = %d, %v; want 0", n, err)
	}
	if es, err := s.Shelf(ctx, userB, "rotation"); err != nil || len(es) != 0 {
		t.Errorf("Shelf as user B = %+v, %v; want empty", es, err)
	}
	if ts, err := s.GuideInputTitles(ctx, userB, "US"); err != nil || len(ts) != 0 {
		t.Errorf("GuideInputTitles as user B = %+v, %v; want empty", ts, err)
	}
	// Titles are shared, so B may shelve A's title; that writes B's own
	// entry and must not touch A's.
	e, err := s.UpsertEntry(ctx, userB, seriesID, EntryUpdate{Status: strp("watched")})
	if err != nil || e.Status != "watched" {
		t.Errorf("UpsertEntry as user B = %+v, %v", e, err)
	}

	after, err := s.GuideWithItems(ctx, userA, g.ID)
	if err != nil {
		t.Fatalf("reload A's guide: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("A's guide changed after B's attempts:\nbefore=%+v\nafter=%+v", before, after)
	}
	if got := rawEntry(t, s, userA, seriesID); !reflect.DeepEqual(beforeSeries, got) {
		t.Fatalf("A's series entry changed:\nbefore=%+v\nafter=%+v", beforeSeries, got)
	}
	if got := rawEntry(t, s, userA, movieID); !reflect.DeepEqual(beforeMovie, got) {
		t.Fatalf("A's movie entry changed:\nbefore=%+v\nafter=%+v", beforeMovie, got)
	}
}

// TestInvalidIDsAreNotFound: IDs that can't be Firestore document IDs map
// to the not-found sentinels (or a plain error for user-only methods) and
// never panic.
func TestInvalidIDsAreNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	itemID := g.Items[0].ID
	pt := true

	for _, bad := range []string{"", "a/b", "__x__"} {
		t.Run("id="+bad, func(t *testing.T) {
			guideKeyed := map[string]error{}
			_, guideKeyed["GuideWithItems/guide"] = s.GuideWithItems(ctx, uid, bad)
			_, guideKeyed["GuideWithItems/user"] = s.GuideWithItems(ctx, bad, g.ID)
			_, guideKeyed["UpdateGuideItem/guide"] = s.UpdateGuideItem(ctx, uid, bad, itemID, GuideItemUpdate{Pinned: &pt})
			_, guideKeyed["UpdateGuideItem/item"] = s.UpdateGuideItem(ctx, uid, g.ID, bad, GuideItemUpdate{Pinned: &pt})
			_, guideKeyed["UpdateGuideItem/user"] = s.UpdateGuideItem(ctx, bad, g.ID, itemID, GuideItemUpdate{Pinned: &pt})
			guideKeyed["DeleteGuideItem/guide"] = s.DeleteGuideItem(ctx, uid, bad, itemID)
			guideKeyed["DeleteGuideItem/item"] = s.DeleteGuideItem(ctx, uid, g.ID, bad)
			guideKeyed["DeleteGuideItem/user"] = s.DeleteGuideItem(ctx, bad, g.ID, itemID)
			_, guideKeyed["ReplaceUnkeptItems/guide"] = s.ReplaceUnkeptItems(ctx, uid, bad, nil, nil, 1)
			_, guideKeyed["ReplaceUnkeptItems/user"] = s.ReplaceUnkeptItems(ctx, bad, g.ID, nil, nil, 1)
			_, guideKeyed["MarkItemWatched/guide"] = s.MarkItemWatched(ctx, uid, bad, itemID)
			_, guideKeyed["MarkItemWatched/item"] = s.MarkItemWatched(ctx, uid, g.ID, bad)
			_, guideKeyed["MarkItemWatched/user"] = s.MarkItemWatched(ctx, bad, g.ID, itemID)
			_, guideKeyed["UnmarkItemWatched/guide"] = s.UnmarkItemWatched(ctx, uid, bad, itemID)
			_, guideKeyed["UnmarkItemWatched/item"] = s.UnmarkItemWatched(ctx, uid, g.ID, bad)
			_, guideKeyed["UnmarkItemWatched/user"] = s.UnmarkItemWatched(ctx, bad, g.ID, itemID)
			_, guideKeyed["CurrentGuide/user"] = s.CurrentGuide(ctx, bad, "2026-01-01")
			for op, err := range guideKeyed {
				if !errors.Is(err, ErrGuideNotFound) {
					t.Errorf("%s: err = %v, want ErrGuideNotFound", op, err)
				}
			}

			titleKeyed := map[string]error{}
			_, titleKeyed["UpsertEntry/title"] = s.UpsertEntry(ctx, uid, bad, EntryUpdate{Status: strp("rotation")})
			_, titleKeyed["UpsertEntry/user"] = s.UpsertEntry(ctx, bad, seriesID, EntryUpdate{Status: strp("rotation")})
			_, titleKeyed["SwapTitle/title"] = s.SwapTitle(ctx, uid, bad, "US")
			_, titleKeyed["SwapTitle/user"] = s.SwapTitle(ctx, bad, seriesID, "US")
			_, titleKeyed["GetTitleFull/title"] = s.GetTitleFull(ctx, uid, bad, "US")
			titleKeyed["ReplaceSeasons"] = s.ReplaceSeasons(ctx, bad, nil)
			titleKeyed["ReplaceProviders"] = s.ReplaceProviders(ctx, bad, "US", nil)
			titleKeyed["ReplaceFutureAirings"] = s.ReplaceFutureAirings(ctx, bad, "2026-01-01", nil)
			for op, err := range titleKeyed {
				if !errors.Is(err, ErrTitleNotFound) {
					t.Errorf("%s: err = %v, want ErrTitleNotFound", op, err)
				}
			}

			// GetTitleFull with a bad user still returns the shared title,
			// without an entry.
			if full, err := s.GetTitleFull(ctx, bad, seriesID, "US"); err != nil || full.Entry != nil {
				t.Errorf("GetTitleFull/user = %+v, %v; want title with nil entry", full, err)
			}

			// User-only methods: a plain error, never a panic or a write.
			userOnly := map[string]error{}
			_, userOnly["Shelf"] = s.Shelf(ctx, bad, "rotation")
			_, userOnly["CountRotation"] = s.CountRotation(ctx, bad, seriesID)
			_, userOnly["GuideInputTitles"] = s.GuideInputTitles(ctx, bad, "US")
			_, userOnly["CreateGuideReplacingOverlaps"] = s.CreateGuideReplacingOverlaps(ctx, bad, "2026-01-05", "2026-01-11", 1, nil)
			_, userOnly["UpdateUserPrefs"] = s.UpdateUserPrefs(ctx, bad, strp("GB"), nil)
			_, userOnly["UpsertUserByFirebaseUID"] = s.UpsertUserByFirebaseUID(ctx, bad, "", "", nil)
			for op, err := range userOnly {
				if err == nil {
					t.Errorf("%s: want error for invalid user id %q", op, bad)
				}
			}

			// A bad exclude ID is simply ignored.
			if n, err := s.CountRotation(ctx, uid, bad); err != nil || n != 2 {
				t.Errorf("CountRotation exclude=%q = %d, %v; want 2", bad, n, err)
			}
		})
	}

	// None of the above touched the real guide.
	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil || len(got.Items) != 1 || got.Items[0] != g.Items[0] {
		t.Fatalf("guide changed = %+v, %v", got, err)
	}
}
