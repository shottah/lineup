package docstore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/guide"
)

// seedGuideWorld creates a user with default prefs and two rotation titles:
// a series (runtime 60, airing, US:901 and GB:902, seasons 1:2 and 2:2,
// airings S1E1 2026-01-01 and S1E2 2026-01-06) and a movie (runtime 120,
// US:902). Providers 901/902 are named P901/P902.
func seedGuideWorld(t *testing.T, s *Store) (userID, seriesID, movieID string) {
	t.Helper()
	ctx := context.Background()
	userID = seedUser(t, s)
	ser, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Guide Series",
		PosterPath: "/guide-series.jpg", RuntimeMinutes: 60, Airing: true})
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	mov, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Guide Movie",
		PosterPath: "/guide-movie.jpg", RuntimeMinutes: 120})
	if err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	seriesID, movieID = ser.ID, mov.ID
	p901 := ProviderRow{ID: 901, Name: "P901"}
	p902 := ProviderRow{ID: 902, Name: "P902"}
	for _, r := range []struct {
		title, region string
		p             ProviderRow
	}{{seriesID, "US", p901}, {movieID, "US", p902}, {seriesID, "GB", p902}} {
		if err := s.ReplaceProviders(ctx, r.title, r.region, []ProviderRow{r.p}); err != nil {
			t.Fatalf("seed providers: %v", err)
		}
	}
	if err := s.ReplaceSeasons(ctx, seriesID, []SeasonRow{{1, 2}, {2, 2}}); err != nil {
		t.Fatalf("seed seasons: %v", err)
	}
	if err := s.ReplaceFutureAirings(ctx, seriesID, "2026-01-01", []AiringRow{
		{1, 1, "2026-01-01"}, {1, 2, "2026-01-06"}}); err != nil {
		t.Fatalf("seed airings: %v", err)
	}
	for _, tid := range []string{seriesID, movieID} {
		if _, err := s.UpsertEntry(ctx, userID, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}
	return userID, seriesID, movieID
}

func TestSortItems(t *testing.T) {
	items := []GuideItem{
		{ID: "a", Date: "2026-01-06", StartMin: 1140, TitleID: "series-1", IsPlan: true},
		{ID: "b", Date: "2026-01-05", StartMin: 1200, TitleID: "series-1", IsPlan: true},
		{ID: "c", Date: "2026-01-05", StartMin: 1140, TitleID: "movie-9", IsPlan: false},
		{ID: "d", Date: "2026-01-05", StartMin: 1140, TitleID: "series-2", IsPlan: true},
		{ID: "e", Date: "2026-01-05", StartMin: 1140, TitleID: "movie-3", IsPlan: false},
	}
	sortItems(items)
	var got []string
	for _, it := range items {
		got = append(got, it.ID)
	}
	if want := []string{"d", "e", "c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
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
	// Sorted by title ID: "movie-..." < "series-...".
	if titles[0].ID != movieID || titles[1].ID != seriesID {
		t.Fatalf("order = [%s %s], want [%s %s]", titles[0].ID, titles[1].ID, movieID, seriesID)
	}
	mov, ser := &titles[0], &titles[1]
	if ser.Kind != "series" || ser.Name != "Guide Series" || !ser.Airing || ser.Runtime != 60 ||
		len(ser.Providers) != 1 || ser.Providers[0] != 901 ||
		ser.SeasonEpisodes[1] != 2 || ser.SeasonEpisodes[2] != 2 ||
		ser.Pointer != (guide.Pointer{Season: 1, Episode: 1}) {
		t.Fatalf("series hydration = %+v", ser)
	}
	wantAir := []guide.AiredEpisode{{Season: 1, Episode: 1, Date: "2026-01-01"}, {Season: 1, Episode: 2, Date: "2026-01-06"}}
	if !reflect.DeepEqual(ser.AirDates, wantAir) {
		t.Fatalf("series airdates = %+v, want %+v", ser.AirDates, wantAir)
	}
	if mov.Kind != "movie" || mov.Runtime != 120 || len(mov.Providers) != 1 || mov.Providers[0] != 902 || len(mov.AirDates) != 0 {
		t.Fatalf("movie hydration = %+v", mov)
	}

	// Region filter: GB drops the movie's provider and swaps the series'.
	gb, err := s.GuideInputTitles(context.Background(), uid, "GB")
	if err != nil {
		t.Fatalf("GB: %v", err)
	}
	if len(gb) != 2 {
		t.Fatalf("GB titles = %d, want 2", len(gb))
	}
	for _, tt := range gb {
		if tt.ID == movieID && len(tt.Providers) != 0 {
			t.Fatalf("GB movie providers = %v, want none", tt.Providers)
		}
		if tt.ID == seriesID && (len(tt.Providers) != 1 || tt.Providers[0] != 902) {
			t.Fatalf("GB series providers = %v, want [902]", tt.Providers)
		}
	}

	// Non-rotation entries are excluded.
	if _, err := s.UpsertEntry(context.Background(), uid, movieID, EntryUpdate{Status: strp("watchlist")}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	only, err := s.GuideInputTitles(context.Background(), uid, "US")
	if err != nil || len(only) != 1 || only[0].ID != seriesID {
		t.Fatalf("after demote = %+v, %v; want only series", only, err)
	}
}

// TestGuideInputTitlesDefaultRuntime: a zero runtimeMinutes is hydrated to
// a sane default rather than passed through as 0, which would collapse the
// scheduler onto the window start.
func TestGuideInputTitlesDefaultRuntime(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ser, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Zero Runtime Series"})
	if err != nil {
		t.Fatalf("seed series: %v", err)
	}
	mov, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Zero Runtime Movie"})
	if err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	for _, tid := range []string{ser.ID, mov.ID} {
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}

	titles, err := s.GuideInputTitles(ctx, uid, "US")
	if err != nil {
		t.Fatalf("GuideInputTitles: %v", err)
	}
	got := map[string]int{}
	for _, tt := range titles {
		got[tt.ID] = tt.Runtime
	}
	if got[ser.ID] != 45 {
		t.Fatalf("series Runtime = %d, want 45", got[ser.ID])
	}
	if got[mov.ID] != 120 {
		t.Fatalf("movie Runtime = %d, want 120", got[mov.ID])
	}
}

func TestGuideLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)

	// Deliberately out of order: the stored guide comes back sorted.
	items := []guide.Item{
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: true, Edited: true, Watched: true},
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true, Pinned: true},
	}
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 42, items)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.ID == "" || g.Seed != 42 || g.StartDate != "2026-01-05" || g.EndDate != "2026-01-11" || len(g.Items) != 2 {
		t.Fatalf("created guide = %+v", g)
	}
	want0 := GuideItem{ID: g.Items[0].ID, Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID,
		Season: 1, Episode: 1, ProviderID: 901, IsPlan: true, Pinned: true}
	want1 := GuideItem{ID: g.Items[1].ID, Date: "2026-01-06", StartMin: 1140, EndMin: 1300, TitleID: movieID,
		ProviderID: 902, IsPlan: true, Edited: true, Watched: true}
	if g.Items[0] != want0 || g.Items[1] != want1 {
		t.Fatalf("items = %+v, want %+v / %+v", g.Items, want0, want1)
	}
	for _, it := range g.Items {
		if len(it.ID) != 10 {
			t.Fatalf("item id %q, want 10 chars", it.ID)
		}
	}
	if g.Items[0].ID == g.Items[1].ID {
		t.Fatalf("duplicate item ids %q", g.Items[0].ID)
	}

	// Reading it back gives the same guide.
	rb, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !reflect.DeepEqual(rb, g) {
		t.Fatalf("read back = %+v, want %+v", rb, g)
	}

	// Overlapping create replaces the first guide; nil items read as [].
	g2, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-08", "2026-01-14", 43, nil)
	if err != nil {
		t.Fatalf("overlap create: %v", err)
	}
	if g2.Items == nil || len(g2.Items) != 0 {
		t.Fatalf("empty guide items = %#v, want []", g2.Items)
	}
	if _, err := s.GuideWithItems(ctx, uid, g.ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("old guide err = %v, want ErrGuideNotFound", err)
	}
	rb2, err := s.GuideWithItems(ctx, uid, g2.ID)
	if err != nil || rb2.Items == nil || len(rb2.Items) != 0 {
		t.Fatalf("read back empty guide = %#v, %v", rb2, err)
	}

	// Current: today inside g2's range finds it; after endDate, not found.
	cur, err := s.CurrentGuide(ctx, uid, "2026-01-10")
	if err != nil || cur.ID != g2.ID || cur.Seed != 43 {
		t.Fatalf("current = %+v, %v", cur, err)
	}
	if _, err := s.CurrentGuide(ctx, uid, "2026-02-01"); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("stale current err = %v", err)
	}

	// Ownership isolation and invalid IDs.
	other := seedUser(t, s)
	if _, err := s.GuideWithItems(ctx, other, g2.ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign guide err = %v", err)
	}
	if _, err := s.CurrentGuide(ctx, other, "2026-01-10"); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign current err = %v", err)
	}
	for _, bad := range []string{"", "a/b", "__x__"} {
		if _, err := s.GuideWithItems(ctx, uid, bad); !errors.Is(err, ErrGuideNotFound) {
			t.Fatalf("GuideWithItems(%q) err = %v, want ErrGuideNotFound", bad, err)
		}
	}
}

// TestCurrentGuidePicksNewest: of several guides still covering today, the
// newest createdAt wins regardless of ID order; equal createdAt falls back
// to the greater guide ID.
func TestCurrentGuidePicksNewest(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)

	// Non-overlapping guides that both end on or after today.
	older, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-12", "2026-01-18", 1, nil)
	if err != nil {
		t.Fatalf("older: %v", err)
	}
	newer, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 2, nil)
	if err != nil {
		t.Fatalf("newer: %v", err)
	}
	cur, err := s.CurrentGuide(ctx, uid, "2026-01-06")
	if err != nil || cur.ID != newer.ID {
		t.Fatalf("current = %+v, %v; want newer %s (older %s)", cur, err, newer.ID, older.ID)
	}

	// Fixture docs: createdAt beats ID order; equal createdAt -> ID desc.
	uid2 := seedUser(t, s)
	col := s.c.Collection("users").Doc(uid2).Collection("guides")
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	put := func(id string, created time.Time, start, end string) {
		t.Helper()
		if _, err := col.Doc(id).Set(ctx, map[string]any{
			"startDate": start, "endDate": end, "seed": 7, "createdAt": created, "items": []any{},
		}); err != nil {
			t.Fatalf("fixture %s: %v", id, err)
		}
	}
	put("zzz", t0, "2026-01-01", "2026-01-07")
	put("aaa", t0.Add(time.Second), "2026-01-08", "2026-01-14")
	cur, err = s.CurrentGuide(ctx, uid2, "2026-01-03")
	if err != nil || cur.ID != "aaa" {
		t.Fatalf("current = %+v, %v; want aaa (newest createdAt)", cur, err)
	}
	put("bbb", t0.Add(time.Second), "2026-01-15", "2026-01-21")
	cur, err = s.CurrentGuide(ctx, uid2, "2026-01-03")
	if err != nil || cur.ID != "bbb" {
		t.Fatalf("current = %+v, %v; want bbb (createdAt tie -> ID desc)", cur, err)
	}
	// Guides ending before today are ignored even when newer.
	put("ccc", t0.Add(time.Hour), "2025-12-01", "2026-01-02")
	cur, err = s.CurrentGuide(ctx, uid2, "2026-01-03")
	if err != nil || cur.ID != "bbb" {
		t.Fatalf("current = %+v, %v; want bbb (ccc ended)", cur, err)
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
	keep := g.Items[0]
	dropID := g.Items[1].ID
	got, err := s.ReplaceUnkeptItems(ctx, uid, g.ID, []string{keep.ID}, []guide.Item{
		{Date: "2026-01-07", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 2, Episode: 1, Provider: 901, IsPlan: true},
	}, 777)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2 (keep + new)", len(got.Items))
	}
	if got.Items[0] != keep || got.Items[1].Season != 2 || got.Items[1].Date != "2026-01-07" {
		t.Fatalf("replace result = %+v", got.Items)
	}
	if id := got.Items[1].ID; len(id) != 10 || id == keep.ID || id == dropID {
		t.Fatalf("new item id %q not fresh", id)
	}
	if got.ID != g.ID || got.StartDate != g.StartDate || got.EndDate != g.EndDate {
		t.Fatalf("guide header changed: %+v vs %+v", got, g)
	}
	// The fresh seed persists, so a later regenerate chains from what
	// actually produced these items rather than the original seed (1).
	if got.Seed != 777 {
		t.Fatalf("seed = %d, want 777", got.Seed)
	}
	reloaded, err := s.GuideWithItems(ctx, uid, g.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(reloaded, got) {
		t.Fatalf("reloaded = %+v, want %+v", reloaded, got)
	}
	if _, err := s.ReplaceUnkeptItems(ctx, seedUser(t, s), g.ID, nil, nil, 1); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("foreign replace err = %v", err)
	}
	if _, err := s.ReplaceUnkeptItems(ctx, uid, "a/b", nil, nil, 1); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("invalid guide id err = %v", err)
	}
}

func TestReplaceUnkeptItemsIgnoresUnknownKeepIDs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s)

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.ReplaceUnkeptItems(ctx, uid, g.ID, []string{"nope"}, []guide.Item{
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-07", StartMin: 1140, EndMin: 1260, TitleID: movieID, Provider: 902, IsPlan: true},
	}, 5)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(got.Items) != 2 || got.Items[0].Date != "2026-01-06" || got.Items[1].Date != "2026-01-07" {
		t.Fatalf("items = %+v, want exactly the 2 new items", got.Items)
	}
	for _, it := range got.Items {
		if it.ID == "nope" || it.ID == g.Items[0].ID {
			t.Fatalf("unexpected item id %q", it.ID)
		}
	}
}

// TestNewItemIDsRegenerateOnCollision forces the ID generator to repeat
// itself, both within the new items and against a kept item.
func TestNewItemIDsRegenerateOnCollision(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, _ := seedGuideWorld(t, s)

	seq := []string{"AAAAAAAAAA", "AAAAAAAAAA", "BBBBBBBBBB", "AAAAAAAAAA", "BBBBBBBBBB", "CCCCCCCCCC"}
	orig := genItemID
	genItemID = func() string {
		id := seq[0]
		seq = seq[1:]
		return id
	}
	t.Cleanup(func() { genItemID = orig })

	mk := func(date string) guide.Item {
		return guide.Item{Date: date, StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true}
	}
	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{mk("2026-01-05"), mk("2026-01-06")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.Items[0].ID != "AAAAAAAAAA" || g.Items[1].ID != "BBBBBBBBBB" {
		t.Fatalf("create ids = %q, %q", g.Items[0].ID, g.Items[1].ID)
	}
	got, err := s.ReplaceUnkeptItems(ctx, uid, g.ID, []string{"AAAAAAAAAA", "BBBBBBBBBB"}, []guide.Item{mk("2026-01-07")}, 2)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(got.Items) != 3 || got.Items[2].ID != "CCCCCCCCCC" {
		t.Fatalf("replace items = %+v, want new id CCCCCCCCCC", got.Items)
	}
}

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
	// Adjacent before: ends the day before g1 starts -> both survive.
	g0, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2025-12-29", "2026-01-04", 0, nil)
	if err != nil {
		t.Fatalf("g0: %v", err)
	}
	if _, err := s.GuideWithItems(ctx, uid, g1.ID); err != nil {
		t.Fatalf("adjacent guide must survive, got %v", err)
	}
	// Touching: starts ON g1's end date -> overlap, g1 replaced; g0 survives.
	if _, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-11", "2026-01-12", 3, nil); err != nil {
		t.Fatalf("g3: %v", err)
	}
	if _, err := s.GuideWithItems(ctx, uid, g1.ID); !errors.Is(err, ErrGuideNotFound) {
		t.Fatalf("boundary-touching guide must replace g1, err = %v", err)
	}
	if _, err := s.GuideWithItems(ctx, uid, g0.ID); err != nil {
		t.Fatalf("earlier non-overlapping guide must survive, got %v", err)
	}
}

func TestGuideLookups(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid, seriesID, movieID := seedGuideWorld(t, s) // series on US:901, movie on US:902

	g, err := s.CreateGuideReplacingOverlaps(ctx, uid, "2026-01-05", "2026-01-11", 1, []guide.Item{
		{Date: "2026-01-05", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 1, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1200, TitleID: seriesID, Season: 1, Episode: 2, Provider: 901, IsPlan: true},
		{Date: "2026-01-06", StartMin: 1140, EndMin: 1300, TitleID: movieID, Provider: 902, IsPlan: false},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	titles, provs, err := s.GuideLookups(ctx, g.Items)
	if err != nil {
		t.Fatalf("GuideLookups: %v", err)
	}
	if len(titles) != 2 {
		t.Fatalf("titles = %d, want 2: %+v", len(titles), titles)
	}
	if len(provs) != 2 {
		t.Fatalf("providers = %d, want 2: %+v", len(provs), provs)
	}
	serDoc, movDoc := mustTitleDoc(t, s, seriesID), mustTitleDoc(t, s, movieID)
	if got, want := titles[seriesID], (TitleLookup{Name: "Guide Series", Kind: "series", TMDBID: serDoc.TMDBID, PosterPath: "/guide-series.jpg"}); got != want {
		t.Fatalf("titles[series] = %+v, want %+v", got, want)
	}
	if got, want := titles[movieID], (TitleLookup{Name: "Guide Movie", Kind: "movie", TMDBID: movDoc.TMDBID, PosterPath: "/guide-movie.jpg"}); got != want {
		t.Fatalf("titles[movie] = %+v, want %+v", got, want)
	}
	if p := provs[901]; p != (ProviderRow{ID: 901, Name: "P901"}) {
		t.Fatalf("providers[901] = %+v", p)
	}
	if p := provs[902]; p != (ProviderRow{ID: 902, Name: "P902"}) {
		t.Fatalf("providers[902] = %+v", p)
	}

	// Missing titles/providers are simply absent.
	partial, pprovs, err := s.GuideLookups(ctx, []GuideItem{
		{ID: "x", TitleID: seriesID, ProviderID: 901},
		{ID: "y", TitleID: "series-0", ProviderID: 123456789},
		{ID: "z", TitleID: "a/b", ProviderID: 0},
	})
	if err != nil {
		t.Fatalf("GuideLookups (partial): %v", err)
	}
	if len(partial) != 1 || len(pprovs) != 1 {
		t.Fatalf("partial lookups = %+v / %+v, want only series and 901", partial, pprovs)
	}

	// No items resolve to empty, non-nil maps.
	et, ep, err := s.GuideLookups(ctx, nil)
	if err != nil {
		t.Fatalf("GuideLookups (empty): %v", err)
	}
	if et == nil || ep == nil || len(et) != 0 || len(ep) != 0 {
		t.Fatalf("empty lookups = %#v / %#v, want empty non-nil", et, ep)
	}
}
