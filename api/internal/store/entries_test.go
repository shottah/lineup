package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// seedTitle inserts a minimal titles row and returns its id. Ingestion
// (#11) is not needed for these tests — only that titles exist.
func seedTitle(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	var id int64
	err := s.Pool.QueryRow(context.Background(),
		`INSERT INTO titles (tmdb_id, kind, name) VALUES ($1, 'series', $2) RETURNING id`,
		time.Now().UnixNano(), name).Scan(&id)
	if err != nil {
		t.Fatalf("seed title: %v", err)
	}
	return id
}

func seedUser(t *testing.T, s *Store) int64 {
	t.Helper()
	u, err := s.UpsertUserByFirebaseUID(context.Background(), uniqueUID(t), "entries@example.com", "E", []byte(`{"windows":{}}`))
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u.ID
}

func strp(s string) *string   { return &s }
func f64p(f float64) *float64 { return &f }
func boolp(b bool) *bool      { return &b }

func TestUpsertEntryLifecycle(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	tid := seedTitle(t, s, "Entry Lifecycle Show")

	// First touch: rating only — status defaults to none, pointer 1/1.
	e, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Rating: f64p(3.5)})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if e.TitleID != tid || e.Status != "none" || e.Rating == nil || *e.Rating != 3.5 ||
		e.Pointer.Season != 1 || e.Pointer.Episode != 1 || e.WatchedAt != nil || e.Name != "Entry Lifecycle Show" {
		t.Fatalf("insert entry = %+v", e)
	}

	// Partial update: status only — rating survives.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watchlist")})
	if err != nil {
		t.Fatalf("status update: %v", err)
	}
	if e.Status != "watchlist" || e.Rating == nil || *e.Rating != 3.5 {
		t.Fatalf("status update entry = %+v", e)
	}

	// watched stamps watched_at; moving off watched preserves it.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watched")})
	if err != nil || e.WatchedAt == nil {
		t.Fatalf("watched: err=%v entry=%+v", err, e)
	}
	stamp := *e.WatchedAt
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("watchlist")})
	if err != nil || e.WatchedAt == nil || !e.WatchedAt.Equal(stamp) {
		t.Fatalf("off-watched: err=%v entry=%+v want watched_at preserved (%v)", err, e, stamp)
	}

	// Clear rating via ClearRating; pointer advance.
	e, err = s.UpsertEntry(ctx, uid, tid, EntryUpdate{ClearRating: true, Pointer: &Pointer{Season: 2, Episode: 5}})
	if err != nil || e.Rating != nil || e.Pointer.Season != 2 || e.Pointer.Episode != 5 {
		t.Fatalf("clear+pointer: err=%v entry=%+v", err, e)
	}

	// Unknown title -> ErrTitleNotFound.
	if _, err := s.UpsertEntry(ctx, uid, 999999999, EntryUpdate{Favorite: boolp(true)}); !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("unknown title err = %v, want ErrTitleNotFound", err)
	}
}

func TestCountRotationExcludes(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	a := seedTitle(t, s, "Rot A")
	b := seedTitle(t, s, "Rot B")
	for _, tid := range []int64{a, b} {
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp("rotation")}); err != nil {
			t.Fatalf("seed rotation: %v", err)
		}
	}
	n, err := s.CountRotation(ctx, uid, 0) // exclude nothing that exists
	if err != nil || n != 2 {
		t.Fatalf("count = %d err=%v, want 2", n, err)
	}
	n, err = s.CountRotation(ctx, uid, a) // excluding a
	if err != nil || n != 1 {
		t.Fatalf("count excluding = %d err=%v, want 1", n, err)
	}
}

func TestShelfFiltersAndOrdering(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)

	mk := func(name, status string, rating *float64, fav bool) int64 {
		tid := seedTitle(t, s, name)
		if _, err := s.UpsertEntry(ctx, uid, tid, EntryUpdate{Status: strp(status), Rating: rating, Favorite: boolp(fav)}); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
		return tid
	}
	wl := mk("Shelf WL", "watchlist", nil, false)
	rot := mk("Shelf Rot", "rotation", f64p(2.0), true)
	wa := mk("Shelf Watched", "watched", f64p(4.5), false)

	cases := []struct {
		shelf string
		want  []int64 // expected title ids in order
	}{
		{"watchlist", []int64{wl}},
		{"rotation", []int64{rot}},
		{"watched", []int64{wa}},
		{"favorites", []int64{rot}},
		{"ratings", []int64{wa, rot}}, // rating DESC: 4.5 before 2.0
	}
	for _, tc := range cases {
		t.Run(tc.shelf, func(t *testing.T) {
			got, err := s.Shelf(ctx, uid, tc.shelf)
			if err != nil {
				t.Fatalf("Shelf(%s): %v", tc.shelf, err)
			}
			ids := make([]int64, len(got))
			for i, e := range got {
				ids[i] = e.TitleID
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("Shelf(%s) ids = %v, want %v", tc.shelf, ids, tc.want)
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
