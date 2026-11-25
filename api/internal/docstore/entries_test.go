package docstore

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func strp(s string) *string   { return &s }
func f64p(f float64) *float64 { return &f }
func boolp(b bool) *bool      { return &b }

func seedTitle(t *testing.T, s *Store, name string) string {
	t.Helper()
	ti, err := s.UpsertTitle(context.Background(), TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: name})
	if err != nil {
		t.Fatalf("seed title: %v", err)
	}
	return ti.ID
}

func seedUser(t *testing.T, s *Store) string {
	t.Helper()
	u, err := s.UpsertUserByFirebaseUID(context.Background(), uniqueUID(t), "entries@example.com", "E", []byte(`{"windows":{}}`))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func TestUpsertEntryLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	tid := seedTitle(t, s, "Entry Lifecycle Show")

	// First touch: rating only; status defaults to none, pointer 1/1.
	e, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Rating: f64p(3.5)})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if e.TitleID != tid || e.Status != "none" || e.Rating == nil || *e.Rating != 3.5 ||
		e.Pointer.Season != 1 || e.Pointer.Episode != 1 || e.WatchedAt != nil || e.Name != "Entry Lifecycle Show" {
		t.Fatalf("insert entry = %+v", e)
	}
	if e.TMDBID == 0 || e.Kind != "series" || e.AddedAt.IsZero() {
		t.Fatalf("entry title fields = %+v", e)
	}
	added := e.AddedAt

	// Partial update: status only; rating survives, addedAt unchanged.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watchlist")})
	if err != nil {
		t.Fatalf("status update: %v", err)
	}
	if e.Status != "watchlist" || e.Rating == nil || *e.Rating != 3.5 || !e.AddedAt.Equal(added) {
		t.Fatalf("status update entry = %+v", e)
	}

	// watched stamps watchedAt; moving off watched preserves it.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watched")})
	if err != nil || e.WatchedAt == nil {
		t.Fatalf("watched: err=%v entry=%+v", err, e)
	}
	stamp := *e.WatchedAt
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watchlist")})
	if err != nil || e.WatchedAt == nil || !e.WatchedAt.Equal(stamp) {
		t.Fatalf("off-watched: err=%v entry=%+v want watchedAt preserved (%v)", err, e, stamp)
	}

	// Clear rating via ClearRating; pointer advance.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{ClearRating: true, Pointer: &Pointer{Season: 2, Episode: 5}})
	if err != nil || e.Rating != nil || e.Pointer.Season != 2 || e.Pointer.Episode != 5 {
		t.Fatalf("clear+pointer: err=%v entry=%+v", err, e)
	}
	// ClearRating wins over Rating.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Rating: f64p(4), ClearRating: true})
	if err != nil || e.Rating != nil {
		t.Fatalf("clear wins: err=%v entry=%+v", err, e)
	}
}

func TestUpsertEntryUnknownTitle(t *testing.T) {
	s := testStore(t)
	uid := seedUser(t, s)
	for _, id := range []string{"series-999999999", "a/b", ""} {
		if _, err := s.UpsertEntry(context.Background(), uid, id, EntryUpdate{Favorite: boolp(true)}); !errors.Is(err, ErrTitleNotFound) {
			t.Fatalf("UpsertEntry(%q) err = %v, want ErrTitleNotFound", id, err)
		}
	}
}

func TestCountRotationExcludes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	a := seedTitle(t, s, "Rot A")
	b := seedTitle(t, s, "Rot B")
	c := seedTitle(t, s, "Not rotating")
	for _, tid := range []string{a, b} {
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}
	if _, err := s.UpsertEntry(ctx, uid, c, EntryUpdate{Status: strp("watchlist")}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	n, err := s.CountRotation(ctx, uid, "series-0") // excludes nothing that exists
	if err != nil || n != 2 {
		t.Fatalf("count = %d err=%v, want 2", n, err)
	}
	n, err = s.CountRotation(ctx, uid, a)
	if err != nil || n != 1 {
		t.Fatalf("count excluding rotating = %d err=%v, want 1", n, err)
	}
	n, err = s.CountRotation(ctx, uid, c) // excluded title not in rotation
	if err != nil || n != 2 {
		t.Fatalf("count excluding non-rotating = %d err=%v, want 2", n, err)
	}
}

func TestShelfFiltersAndOrdering(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)

	mk := func(name, status string, rating *float64, fav bool) string {
		tid := seedTitle(t, s, name)
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp(status), Rating: rating, Favorite: boolp(fav)}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		time.Sleep(2 * time.Millisecond) // distinct addedAt
		return tid
	}
	wl := mk("Shelf WL", "watchlist", nil, false)
	rot := mk("Shelf Rot", "rotation", f64p(2.0), true)
	wa := mk("Shelf Watched", "watched", f64p(4.5), false)
	wl2 := mk("Shelf WL2", "watchlist", nil, false)

	cases := []struct {
		shelf string
		want  []string
	}{
		{"watchlist", []string{wl2, wl}}, // addedAt DESC
		{"rotation", []string{rot}},
		{"watched", []string{wa}},
		{"favorites", []string{rot}},
		{"ratings", []string{wa, rot}}, // rating DESC
	}
	for _, tc := range cases {
		t.Run(tc.shelf, func(t *testing.T) {
			got, err := s.Shelf(ctx, uid, tc.shelf)
			if err != nil {
				t.Fatalf("Shelf(%s): %v", tc.shelf, err)
			}
			ids := make([]string, len(got))
			for i, e := range got {
				ids[i] = e.TitleID
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("Shelf(%s) ids = %v, want %v", tc.shelf, ids, tc.want)
			}
			for _, e := range got {
				if e.Name == "" || e.TMDBID == 0 || e.Kind == "" {
					t.Fatalf("Shelf(%s) entry missing title fields: %+v", tc.shelf, e)
				}
			}
		})
	}

	if _, err := s.Shelf(ctx, uid, "bogus"); err == nil {
		t.Fatal("Shelf(bogus) want error")
	}
	empty, err := s.Shelf(ctx, seedUser(t, s), "watchlist")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty shelf = %v err=%v, want empty non-nil slice", empty, err)
	}
}

func TestShelfSkipsMissingTitle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	tid := seedTitle(t, s, "Doomed")
	if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watchlist")}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.c.Collection("titles").Doc(tid).Delete(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := s.Shelf(ctx, uid, "watchlist")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("shelf = %v err=%v, want empty", got, err)
	}
}

func TestGetTitleFullEntryAndEmptySlices(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Full Movie"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	full, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full (no entry): %v", err)
	}
	if full.Title.ID != ti.ID || full.Title.Name != "Full Movie" {
		t.Fatalf("title = %+v", full.Title)
	}
	if full.Entry != nil {
		t.Fatalf("entry = %+v, want nil before any user interaction", full.Entry)
	}
	if full.Seasons == nil || len(full.Seasons) != 0 || full.Providers == nil || len(full.Providers) != 0 {
		t.Fatalf("empty slices must be non-nil: seasons=%v providers=%v", full.Seasons, full.Providers)
	}

	if _, err := s.UpsertEntry(ctx, uid, ti.ID, EntryUpdate{Status: strp("watchlist")}); err != nil {
		t.Fatalf("entry: %v", err)
	}
	full, err = s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full (entry): %v", err)
	}
	if full.Entry == nil || full.Entry.Status != "watchlist" || full.Entry.TitleID != ti.ID || full.Entry.Name != "Full Movie" {
		t.Fatalf("entry = %+v", full.Entry)
	}

	if _, err := s.GetTitleFull(ctx, uid, "movie--1", "US"); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("unknown title err = %v, want ErrTitleNotFound", err)
	}
	if _, err := s.GetTitleFull(ctx, uid, "a/b", "US"); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("invalid title err = %v, want ErrTitleNotFound", err)
	}
}

func TestGetTitleFullSeasonsAndProviders(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Sorted"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceSeasons(ctx, ti.ID, []SeasonRow{{10, 8}, {2, 6}, {1, 10}}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{
		{ID: 8, Name: "Netflix", LogoPath: "/n.png"}, {ID: 9, Name: "Apple TV", LogoPath: "/a.png"}, {ID: 3, Name: "Zed", LogoPath: "/z.png"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "GB", []ProviderRow{{ID: 5, Name: "Other", LogoPath: "/o.png"}}); err != nil {
		t.Fatal(err)
	}
	full, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(full.Seasons) != "[{1 10} {2 6} {10 8}]" {
		t.Fatalf("seasons = %v", full.Seasons)
	}
	want := "[{9 Apple TV /a.png} {8 Netflix /n.png} {3 Zed /z.png}]"
	if fmt.Sprint(full.Providers) != want {
		t.Fatalf("providers = %v, want %s", full.Providers, want)
	}
}
