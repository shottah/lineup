package docstore

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/shottah/lineup/api/internal/guide"
)

func TestUpdateDeleteGuideItem(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
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

	// The update persisted, not just the returned value.
	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil || len(got.Items) != 1 || got.Items[0] != *it {
		t.Fatalf("reload = %+v, %v, want item %+v", got, err, it)
	}

	if _, err := s.UpdateGuideItem(ctx, seedUser(t, s), g.ID, itemID, GuideItemUpdate{Pinned: &pt}); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign update err = %v", err)
	}
	if _, err := s.UpdateGuideItem(ctx, uid, g.ID, "nosuchitem", GuideItemUpdate{Pinned: &pt}); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("missing item update err = %v", err)
	}
	if err := s.DeleteGuideItem(ctx, seedUser(t, s), g.ID, itemID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign delete err = %v", err)
	}
	if err := s.DeleteGuideItem(ctx, uid, g.ID, itemID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got, err := s.GuideWithItems(ctx, uid, g.ID); err != nil || len(got.Items) != 0 {
		t.Fatalf("after delete = %+v, %v, want no items", got, err)
	}
	if err := s.DeleteGuideItem(ctx, uid, g.ID, itemID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("re-delete err = %v", err)
	}
}

func TestSwapTitle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)

	info, err := s.SwapTitle(ctx, uid, seriesID, "US")
	if err != nil || info.TitleID != seriesID || info.Kind != "series" || info.Runtime != 60 || info.PointerSeason != 1 {
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
	// No providers in the region: empty, not nil.
	frInfo, err := s.SwapTitle(ctx, uid, seriesID, "FR")
	if err != nil || frInfo.Providers == nil || len(frInfo.Providers) != 0 {
		t.Fatalf("FR swap providers = %#v, %v, want empty non-nil", frInfo, err)
	}
	// A title not on rotation/watchlist is invalid.
	stray := seedTitle(t, s, "Stray")
	if _, err := s.SwapTitle(ctx, uid, stray, "US"); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("stray swap err = %v", err)
	}
	watched := seedTitle(t, s, "Watched")
	if _, err := s.UpsertEntry(ctx, uid, watched, EntryUpdate{Status: strp("watched")}); err != nil {
		t.Fatalf("seed watched: %v", err)
	}
	if _, err := s.SwapTitle(ctx, uid, watched, "US"); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("watched swap err = %v", err)
	}
	wl := seedTitle(t, s, "Watchlisted")
	if _, err := s.UpsertEntry(ctx, uid, wl, EntryUpdate{Status: strp("watchlist")}); err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}
	if _, err := s.SwapTitle(ctx, uid, wl, "US"); err != nil {
		t.Fatalf("watchlist swap err = %v", err)
	}
	if _, err := s.SwapTitle(ctx, uid, "movie-0", "US"); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("missing title swap err = %v", err)
	}
}

// TestSwapTitleAppliesRuntimeDefault mirrors TestGuideInputTitlesDefaultRuntime
// for the swap path: a title whose runtime is 0 swaps in at the 45/120
// default, not at 0 (which would collapse the slot).
func TestSwapTitleAppliesRuntimeDefault(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ser, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Zero RT Swap Series"})
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	mov, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Zero RT Swap Movie"})
	if err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	for _, tid := range []string{ser.ID, mov.ID} {
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}

	si, err := s.SwapTitle(ctx, uid, ser.ID, "US")
	if err != nil {
		t.Fatalf("series swap: %v", err)
	}
	if si.Runtime != defaultSeriesRuntimeMin {
		t.Fatalf("series swap runtime = %d, want %d", si.Runtime, defaultSeriesRuntimeMin)
	}
	mi, err := s.SwapTitle(ctx, uid, mov.ID, "US")
	if err != nil {
		t.Fatalf("movie swap: %v", err)
	}
	if mi.Runtime != defaultMovieRuntimeMin {
		t.Fatalf("movie swap runtime = %d, want %d", mi.Runtime, defaultMovieRuntimeMin)
	}
}

// TestUpdateGuideItemSwapProvider: a swap recomputes providerId from the new
// title's region providers rather than leaving the old title's in place.
func TestUpdateGuideItemSwapProvider(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // series on US:901, movie on US:902

	// A second series sharing the movie's provider (902) plus a lower id
	// (500), to distinguish "keep the current provider" from "lowest id".
	altSeries := seedTitle(t, s, "Alt Series")
	if err := s.ReplaceProviders(ctx, altSeries, "US", []ProviderRow{{ID: 902, Name: "P902"}, {ID: 500, Name: "P500"}}); err != nil {
		t.Fatalf("seed alt providers: %v", err)
	}
	if _, err := s.UpsertEntry(ctx, uid, altSeries, EntryUpdate{Status: strp("rotation")}); err != nil {
		t.Fatalf("seed alt rotation: %v", err)
	}
	// A title with no region providers yet.
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

	// Movie has only 902; item is on 901, so it falls back to the lowest.
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

	// altSeries has [500, 902]; item is on 902, so it is kept over the lower id.
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

	// Unsorted SwapProviders: the fallback is the lowest id, not the first.
	it, err = s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{
		TitleID: &altSeries, SwapProviders: []int64{700, 600, 800},
	})
	if err != nil || it.ProviderID != 600 {
		t.Fatalf("swap with unsorted providers = %+v, %v, want provider_id 600", it, err)
	}

	// No region providers: provider left unchanged.
	info, err = s.SwapTitle(ctx, uid, bareTitle, "US")
	if err != nil || len(info.Providers) != 0 {
		t.Fatalf("swap info (bare) = %+v, %v, want empty providers", info, err)
	}
	it, err = s.UpdateGuideItem(ctx, uid, g.ID, itemID, GuideItemUpdate{
		TitleID: &bareTitle, Season: &sz, Episode: &ez, SwapProviders: info.Providers, SetEdited: true,
	})
	if err != nil || it.ProviderID != 600 {
		t.Fatalf("swap to bare title = %+v, %v, want provider_id unchanged at 600", it, err)
	}
}

func planItemID(t *testing.T, g *Guide) string {
	t.Helper()
	for _, it := range g.Items {
		if it.IsPlan {
			return it.ID
		}
	}
	t.Fatalf("no plan item seeded: %+v", g.Items)
	return ""
}

// TestUpdateGuideItemSwapClearsShadowedAlternate: swapping a plan item to a
// title that is an alternate on the same date deletes that alternate, while
// alternates for other titles and other dates survive.
func TestUpdateGuideItemSwapClearsShadowedAlternate(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)

	altSeries := seedTitle(t, s, "Alt Series")
	if _, err := s.UpsertEntry(ctx, uid, altSeries, EntryUpdate{Status: strp("rotation")}); err != nil {
		t.Fatalf("seed alt rotation: %v", err)
	}

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		// Shadowed alternate: same date, same title as the swap target -> deleted.
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: altSeries, Provider: 902, IsPlan: false},
		// Different title, same date -> survives.
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: movieID, Provider: 902, IsPlan: false},
		// Same title, different date -> survives.
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: altSeries, Provider: 902, IsPlan: false},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := planItemID(t, g)

	sz, ez := 0, 0
	if _, err := s.UpdateGuideItem(ctx, uid, g.ID, planID, GuideItemUpdate{
		TitleID: &altSeries, Season: &sz, Episode: &ez, SetEdited: true,
	}); err != nil {
		t.Fatalf("swap: %v", err)
	}

	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got.Items) != 3 {
		t.Fatalf("items after swap = %d, want 3 (plan + 2 surviving alternates): %+v", len(got.Items), got.Items)
	}
	var sawOtherTitle, sawOtherDate bool
	for _, it := range got.Items {
		if !it.IsPlan && it.Date == "2026-01-05" && it.TitleID == altSeries {
			t.Fatalf("shadowed alternate survived: %+v", it)
		}
		if !it.IsPlan && it.Date == "2026-01-05" && it.TitleID == movieID {
			sawOtherTitle = true
		}
		if !it.IsPlan && it.Date == "2026-01-06" && it.TitleID == altSeries {
			sawOtherDate = true
		}
	}
	if !sawOtherTitle {
		t.Fatal("different-title alternate on the same date was wrongly deleted")
	}
	if !sawOtherDate {
		t.Fatal("same-title alternate on a different date was wrongly deleted")
	}
}

// TestUpdateGuideItemSwapClearsAlternateDateWide: the clear is date-wide, so
// alternates for the swapped-in title at any startMin on that date go, while
// one on a different date and the patched plan item survive.
func TestUpdateGuideItemSwapClearsAlternateDateWide(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)

	altSeries := seedTitle(t, s, "Alt Series Date-Wide")
	if _, err := s.UpsertEntry(ctx, uid, altSeries, EntryUpdate{Status: strp("rotation")}); err != nil {
		t.Fatalf("seed alt rotation: %v", err)
	}

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: altSeries, Provider: 902, IsPlan: false},
		{Date: "2026-01-05", StartMin: 600, EndMin: 660, TitleID: altSeries, Provider: 902, IsPlan: false},
		{Date: "2026-01-06", StartMin: 600, EndMin: 660, TitleID: altSeries, Provider: 902, IsPlan: false},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	planID := planItemID(t, g)

	sz, ez := 0, 0
	if _, err := s.UpdateGuideItem(ctx, uid, g.ID, planID, GuideItemUpdate{
		TitleID: &altSeries, Season: &sz, Episode: &ez, SetEdited: true,
	}); err != nil {
		t.Fatalf("swap: %v", err)
	}

	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items after swap = %d, want 2 (patched plan + other-date alternate): %+v", len(got.Items), got.Items)
	}
	var sawPatchedPlan, sawOtherDate bool
	for _, it := range got.Items {
		if it.IsPlan && it.Date == "2026-01-05" && it.StartMin == 1140 {
			if it.TitleID != altSeries {
				t.Fatalf("patched plan item title = %s, want %s: %+v", it.TitleID, altSeries, it)
			}
			sawPatchedPlan = true
		}
		if !it.IsPlan && it.Date == "2026-01-05" {
			t.Fatalf("same-date alternate survived: %+v", it)
		}
		if !it.IsPlan && it.Date == "2026-01-06" && it.TitleID == altSeries {
			sawOtherDate = true
		}
	}
	if !sawPatchedPlan {
		t.Fatal("patched plan row missing after swap")
	}
	if !sawOtherDate {
		t.Fatal("same-title alternate on a different date was wrongly deleted")
	}
}

func TestUpdateGuideItemPastMove(t *testing.T) {
	const today = "2026-01-08"

	// newPastFutureGuide seeds one guide with a past-dated item (2026-01-05)
	// and a future-dated item (2026-01-10).
	newPastFutureGuide := func(t *testing.T, s *Store, uid, seriesID string) *Guide {
		t.Helper()
		g, err := s.CreateGuideReplacingOverlaps(context.Background(), uid, "2026-01-01", "2026-01-14", 1, []guide.Item{
			{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
			{Date: "2026-01-10", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
		})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		return g
	}
	itemOn := func(g *Guide, date string) string {
		for _, it := range g.Items {
			if it.Date == date {
				return it.ID
			}
		}
		return ""
	}

	t.Run("moving the past item by StartMin only is rejected", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		ns := 100
		_, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-05"), GuideItemUpdate{StartMin: &ns, Today: today})
		if !errors.Is(err, ErrPastMove) {
			t.Fatalf("err = %v, want ErrPastMove", err)
		}
		// Rejected moves write nothing.
		got, err := s.GuideWithItems(ctx, uid, g.ID)
		if err != nil || !reflect.DeepEqual(got.Items, g.Items) {
			t.Fatalf("after rejected move = %+v, %v, want unchanged %+v", got, err, g.Items)
		}
	})

	t.Run("moving the past item to any target date is still ErrPastMove", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		nd := "2026-01-12"
		_, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-05"), GuideItemUpdate{Date: &nd, Today: today})
		if !errors.Is(err, ErrPastMove) {
			t.Fatalf("err = %v, want ErrPastMove", err)
		}
	})

	t.Run("moving the future item to a past date is ErrPastTarget", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		nd := "2026-01-03"
		_, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-10"), GuideItemUpdate{Date: &nd, Today: today})
		if !errors.Is(err, ErrPastTarget) {
			t.Fatalf("err = %v, want ErrPastTarget", err)
		}
	})

	t.Run("moving the future item to another future date and time succeeds", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		nd := "2026-01-11"
		ns := 1200
		it, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-10"), GuideItemUpdate{Date: &nd, StartMin: &ns, Today: today})
		if err != nil {
			t.Fatalf("move: %v", err)
		}
		if it.Date != nd || it.StartMin != ns {
			t.Fatalf("moved item = %+v, want date=%s start_min=%d", it, nd, ns)
		}
	})

	t.Run("window is not enforced: a StartMin outside typical hours on a future date succeeds", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		ns := 300
		it, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-10"), GuideItemUpdate{StartMin: &ns, Today: today})
		if err != nil {
			t.Fatalf("move outside window: %v", err)
		}
		if it.StartMin != ns {
			t.Fatalf("start_min = %d, want %d", it.StartMin, ns)
		}
	})

	t.Run("a pin-only patch on the past item skips enforcement", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		pt := true
		it, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-05"), GuideItemUpdate{Pinned: &pt, Today: today})
		if err != nil {
			t.Fatalf("pin-only: %v", err)
		}
		if !it.Pinned {
			t.Fatal("pinned not applied")
		}
	})

	t.Run("an empty Today disables enforcement", func(t *testing.T) {
		s := testStore(t)
		ctx := context.Background()
		uid, seriesID, _ := seedGuideWorld(t, s)
		g := newPastFutureGuide(t, s, uid, seriesID)
		nd := "2026-01-02"
		it, err := s.UpdateGuideItem(ctx, uid, g.ID, itemOn(g, "2026-01-05"), GuideItemUpdate{Date: &nd})
		if err != nil || it.Date != nd {
			t.Fatalf("unenforced move = %+v, %v", it, err)
		}
	})
}

// TestUpdateGuideItemMoveNotFound: a move (Date/StartMin set, Today set) for a
// missing item is ErrGuideNotFound.
func TestUpdateGuideItemMoveNotFound(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-10", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	ns := 100
	_, err = s.UpdateGuideItem(ctx, uid, g.ID, "missingItem", GuideItemUpdate{StartMin: &ns, Today: "2026-01-08"})
	if !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("err = %v, want ErrGuideNotFound", err)
	}
	if _, err := s.UpdateGuideItem(ctx, uid, "noSuchGuide", g.Items[0].ID, GuideItemUpdate{StartMin: &ns, Today: "2026-01-08"}); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("missing guide err = %v, want ErrGuideNotFound", err)
	}
}

// TestConcurrentItemUpdatesBothApply: concurrent updates to different items
// in one guide doc both land (the transaction retries rather than one
// read-modify-write clobbering the other).
func TestConcurrentItemUpdatesBothApply(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1260, TitleID: movieID, Provider: 902, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	a, b := g.Items[0], g.Items[1]

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		pt := true
		_, errs[0] = s.UpdateGuideItem(ctx, uid, g.ID, a.ID, GuideItemUpdate{Pinned: &pt})
	}()
	go func() {
		defer wg.Done()
		ns := b.StartMin + 30
		_, errs[1] = s.UpdateGuideItem(ctx, uid, g.ID, b.ID, GuideItemUpdate{StartMin: &ns})
	}()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("update %d: %v", i, err)
		}
	}

	got, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	byID := map[string]GuideItem{}
	for _, it := range got.Items {
		byID[it.ID] = it
	}
	if ga := byID[a.ID]; !ga.Pinned {
		t.Fatalf("item A = %+v, want pinned", ga)
	}
	if gb := byID[b.ID]; gb.StartMin != b.StartMin+30 || gb.EndMin != b.EndMin+30 {
		t.Fatalf("item B = %+v, want start %d end %d", gb, b.StartMin+30, b.EndMin+30)
	}
}
