# Shelves and Rotation Entry Endpoints (issue #12) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `PATCH /v1/titles/{id}/entry` (partial entry upsert with rotation cap, rating validation, watched stamping) and `GET /v1/me/shelves/{shelf}`, on the existing `user_titles` schema.

**Architecture:** Store layer does single-round-trip CTE upserts returning entry+title joined rows; handlers own validation and the cap check; `EntryStore` narrow interface keeps handler tests hermetic (fake mirroring SQL semantics), same pattern as `UserStore`.

**Tech Stack:** Go 1.25, chi v5, pgx v5, existing RequireAuth middleware.

## Global Constraints

- Branch `feat/12-entries-shelves` (off `main`), squash-merge. Commit per task.
- No migrations: `user_titles` already has every column (status CHECK none|watchlist|rotation|watched, rating NUMERIC(2,1) CHECK 0.5–5.0, favorite, pointer_season/episode DEFAULT 1, added_at, watched_at nullable).
- Error bodies `{"error":"<code>"}` via existing `writeJSONError`. Codes used here: 400 `malformed json`; 404 `not_found`; 409 `rotation_full`; 422 `nothing to update` / `invalid status` / `invalid rating` / `invalid pointer`; 500 `internal`.
- Rotation cap constant 8. Cap check counts rotation rows EXCLUDING the target title (idempotent re-set never 409s). Count-then-upsert race accepted for v1 (documented in spec).
- Rating: half-steps in [0.5,5.0] — valid iff `v >= 0.5 && v <= 5.0 && v*2 == math.Trunc(v*2)`. RawMessage presence: absent=unchanged, `null`=clear, number=validate+set.
- `status=watched` stamps `watched_at=now()` (re-stamp on re-mark); other statuses preserve existing watched_at.
- Shelf names exactly `watchlist|rotation|watched|favorites|ratings`; anything else 404. Ordering: ratings → `rating DESC, added_at DESC`; all others → `added_at DESC`. Shelf response shape: `{"entries":[...]}`; empty shelf returns `{"entries":[]}` (JSON array, never null).
- Entry JSON shape (exact keys): `title_id, kind, name, poster_path, runtime_minutes, airing, status, rating, favorite, pointer{season,episode}, added_at, watched_at`.
- Store integration tests gated on `TEST_DATABASE_URL` (`testStore` helper from users_test.go is package-level — reuse it); handler tests hermetic. `go test ./...` green in both modes.
- TDD per task.

---

### Task 1: store layer — Entry types, UpsertEntry, CountRotation, Shelf

**Files:**
- Create: `api/internal/store/entries.go`
- Create: `api/internal/store/entries_test.go`

**Interfaces:**
- Produces (Task 2 consumes verbatim): `store.Pointer{Season,Episode int}` (json `season/episode`); `store.Entry` (fields per Global Constraints, `Rating *float64`, `WatchedAt *time.Time`, `Pointer Pointer` embedded under json key `pointer`); `store.EntryUpdate{Status *string; Rating *float64; ClearRating bool; Favorite *bool; Pointer *Pointer}`; `ErrTitleNotFound`; methods `UpsertEntry(ctx, userID, titleID int64, u EntryUpdate) (*Entry, error)`, `CountRotation(ctx, userID, excludeTitleID int64) (int, error)`, `Shelf(ctx, userID int64, shelf string) ([]Entry, error)`.

- [ ] **Step 1: Write the failing integration tests**

```go
// api/internal/store/entries_test.go
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

func strp(s string) *string    { return &s }
func f64p(f float64) *float64  { return &f }
func boolp(b bool) *bool       { return &b }

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
```

Note: shelf tests deliberately filter by ids they seeded; other tests'
rows for the same user cannot collide because each test seeds its own user.

- [ ] **Step 2: Run to verify failure** — `cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/ -run 'Entry|Rotation|Shelf'` → FAIL (undefined). Without the env var → compile error is still a failure; after implementing, both modes must pass. (Postgres: `docker start lineup-pg` if stopped.)

- [ ] **Step 3: Implement**

```go
// api/internal/store/entries.go
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrTitleNotFound is returned when an entry references a title id that
// does not exist (mapped from the FK violation, not pre-queried).
var ErrTitleNotFound = errors.New("store: title not found")

// Pointer is a user's position in a series.
type Pointer struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

// Entry is a user's relationship to a title, joined with the title columns
// shelf views render.
type Entry struct {
	TitleID        int64      `json:"title_id"`
	Kind           string     `json:"kind"`
	Name           string     `json:"name"`
	PosterPath     string     `json:"poster_path"`
	RuntimeMinutes int        `json:"runtime_minutes"`
	Airing         bool       `json:"airing"`
	Status         string     `json:"status"`
	Rating         *float64   `json:"rating"`
	Favorite       bool       `json:"favorite"`
	Pointer        Pointer    `json:"pointer"`
	AddedAt        time.Time  `json:"added_at"`
	WatchedAt      *time.Time `json:"watched_at"`
}

// EntryUpdate carries PATCH semantics: nil pointer fields are unchanged;
// ClearRating true sets rating NULL (wins over Rating).
type EntryUpdate struct {
	Status      *string
	Rating      *float64
	ClearRating bool
	Favorite    *bool
	Pointer     *Pointer
}

const entryColumns = `t.id, t.kind, t.name, t.poster_path, t.runtime_minutes, t.airing,
       %[1]s.status, %[1]s.rating, %[1]s.favorite, %[1]s.pointer_season, %[1]s.pointer_episode, %[1]s.added_at, %[1]s.watched_at`

func scanEntry(row pgx.Row) (*Entry, error) {
	e := &Entry{}
	err := row.Scan(&e.TitleID, &e.Kind, &e.Name, &e.PosterPath, &e.RuntimeMinutes, &e.Airing,
		&e.Status, &e.Rating, &e.Favorite, &e.Pointer.Season, &e.Pointer.Episode, &e.AddedAt, &e.WatchedAt)
	if err != nil {
		return nil, err
	}
	return e, nil
}

// UpsertEntry creates or partially updates the caller's entry for a title
// in one round trip, returning the entry joined with title data.
// status=watched stamps watched_at; other statuses preserve it.
func (s *Store) UpsertEntry(ctx context.Context, userID, titleID int64, u EntryUpdate) (*Entry, error) {
	var ps, pe *int
	if u.Pointer != nil {
		ps, pe = &u.Pointer.Season, &u.Pointer.Episode
	}
	q := `
WITH up AS (
  INSERT INTO user_titles (user_id, title_id, status, rating, favorite, pointer_season, pointer_episode, watched_at)
  VALUES ($1, $2, COALESCE($3, 'none'),
          CASE WHEN $5 THEN NULL ELSE $4::numeric END,
          COALESCE($6, false), COALESCE($7, 1), COALESCE($8, 1),
          CASE WHEN $3 = 'watched' THEN now() END)
  ON CONFLICT (user_id, title_id) DO UPDATE SET
    status          = COALESCE($3, user_titles.status),
    rating          = CASE WHEN $5 THEN NULL ELSE COALESCE($4::numeric, user_titles.rating) END,
    favorite        = COALESCE($6, user_titles.favorite),
    pointer_season  = COALESCE($7, user_titles.pointer_season),
    pointer_episode = COALESCE($8, user_titles.pointer_episode),
    watched_at      = CASE WHEN $3 = 'watched' THEN now() ELSE user_titles.watched_at END
  RETURNING title_id, status, rating, favorite, pointer_season, pointer_episode, added_at, watched_at
)
SELECT ` + fmt.Sprintf(entryColumns, "up") + `
FROM up JOIN titles t ON t.id = up.title_id`
	e, err := scanEntry(s.Pool.QueryRow(ctx, q, userID, titleID, u.Status, u.Rating, u.ClearRating, u.Favorite, ps, pe))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return nil, ErrTitleNotFound
		}
		return nil, fmt.Errorf("store: upsert entry: %w", err)
	}
	return e, nil
}

// CountRotation counts the user's rotation entries, excluding
// excludeTitleID so re-setting an already-rotating title stays idempotent.
func (s *Store) CountRotation(ctx context.Context, userID, excludeTitleID int64) (int, error) {
	const q = `SELECT count(*) FROM user_titles WHERE user_id = $1 AND status = 'rotation' AND title_id <> $2`
	var n int
	if err := s.Pool.QueryRow(ctx, q, userID, excludeTitleID).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count rotation: %w", err)
	}
	return n, nil
}

// Shelf lists a user's entries for one shelf view, joined with title data.
// shelf must be pre-validated by the caller; unknown values error.
func (s *Store) Shelf(ctx context.Context, userID int64, shelf string) ([]Entry, error) {
	base := `SELECT ` + fmt.Sprintf(entryColumns, "ut") + `
FROM user_titles ut JOIN titles t ON t.id = ut.title_id
WHERE ut.user_id = $1 AND `
	var rows pgx.Rows
	var err error
	switch shelf {
	case "watchlist", "rotation", "watched":
		rows, err = s.Pool.Query(ctx, base+`ut.status = $2 ORDER BY ut.added_at DESC`, userID, shelf)
	case "favorites":
		rows, err = s.Pool.Query(ctx, base+`ut.favorite ORDER BY ut.added_at DESC`, userID)
	case "ratings":
		rows, err = s.Pool.Query(ctx, base+`ut.rating IS NOT NULL ORDER BY ut.rating DESC, ut.added_at DESC`, userID)
	default:
		return nil, fmt.Errorf("store: unknown shelf %q", shelf)
	}
	if err != nil {
		return nil, fmt.Errorf("store: shelf %s: %w", shelf, err)
	}
	defer rows.Close()

	entries := []Entry{}
	for rows.Next() {
		e, serr := scanEntry(rows)
		if serr != nil {
			return nil, fmt.Errorf("store: shelf %s: scan: %w", shelf, serr)
		}
		entries = append(entries, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: shelf %s: rows: %w", shelf, err)
	}
	return entries, nil
}
```

- [ ] **Step 4: Run both modes** — with TEST_DATABASE_URL: the three new tests PASS; without: whole suite passes with skips. `go vet ./...` clean.
- [ ] **Step 5: Commit**

```bash
git add api/internal/store/entries.go api/internal/store/entries_test.go
git commit -m "feat(api): entry upsert, rotation count, shelf queries"
```

### Task 2: handlers, routing, wiring

**Files:**
- Create: `api/internal/httpserver/entries.go`
- Create: `api/internal/httpserver/entries_test.go`
- Modify: `api/internal/httpserver/server.go` (Deps.Entries + two routes)
- Modify: `api/internal/httpserver/me_test.go` (testServer helper wires the fake entries store)
- Modify: `api/cmd/api/main.go` (Entries: st)

**Interfaces:**
- Consumes: Task 1's types/methods verbatim; `requireAuth`/`userFrom`/`writeJSONError`.
- Produces: `EntryStore` interface (Task 1's three method signatures); routes `PATCH /v1/titles/{id}/entry`, `GET /v1/me/shelves/{shelf}`; `Deps{..., Entries EntryStore}`.

- [ ] **Step 1: Write the failing tests**

```go
// api/internal/httpserver/entries_test.go
package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/store"
)

// fakeEntries implements EntryStore in memory for one user, mirroring the
// SQL semantics (COALESCE partials, watched stamping, count exclusion).
type fakeEntries struct {
	titles  map[int64]string // known title ids -> name
	entries map[int64]*store.Entry
}

func newFakeEntries(titleIDs ...int64) *fakeEntries {
	f := &fakeEntries{titles: map[int64]string{}, entries: map[int64]*store.Entry{}}
	for _, id := range titleIDs {
		f.titles[id] = fmt.Sprintf("Title %d", id)
	}
	return f
}

func (f *fakeEntries) UpsertEntry(_ context.Context, _ int64, titleID int64, u store.EntryUpdate) (*store.Entry, error) {
	if _, ok := f.titles[titleID]; !ok {
		return nil, store.ErrTitleNotFound
	}
	e, ok := f.entries[titleID]
	if !ok {
		e = &store.Entry{TitleID: titleID, Kind: "series", Name: f.titles[titleID], Status: "none",
			Pointer: store.Pointer{Season: 1, Episode: 1}, AddedAt: time.Now()}
		f.entries[titleID] = e
	}
	if u.Status != nil {
		e.Status = *u.Status
		if *u.Status == "watched" {
			now := time.Now()
			e.WatchedAt = &now
		}
	}
	if u.ClearRating {
		e.Rating = nil
	} else if u.Rating != nil {
		e.Rating = u.Rating
	}
	if u.Favorite != nil {
		e.Favorite = *u.Favorite
	}
	if u.Pointer != nil {
		e.Pointer = *u.Pointer
	}
	cp := *e
	return &cp, nil
}

func (f *fakeEntries) CountRotation(_ context.Context, _ int64, excludeTitleID int64) (int, error) {
	n := 0
	for id, e := range f.entries {
		if e.Status == "rotation" && id != excludeTitleID {
			n++
		}
	}
	return n, nil
}

func (f *fakeEntries) Shelf(_ context.Context, _ int64, shelf string) ([]store.Entry, error) {
	out := []store.Entry{}
	for _, e := range f.entries {
		switch shelf {
		case "watchlist", "rotation", "watched":
			if e.Status == shelf {
				out = append(out, *e)
			}
		case "favorites":
			if e.Favorite {
				out = append(out, *e)
			}
		case "ratings":
			if e.Rating != nil {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func entriesServer(t *testing.T, titleIDs ...int64) (http.Handler, *fakeEntries) {
	t.Helper()
	fe := newFakeEntries(titleIDs...)
	users := newFakeUsers()
	verifier := fakeVerifierWithTok1()
	srv := New(Deps{Users: users, Verifier: verifier, Entries: fe})
	return srv.Handler, fe
}

func TestPatchEntryValidation(t *testing.T) {
	h, _ := entriesServer(t, 1)
	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{"no auth handled by middleware", "", "", 0}, // covered in auth_test.go; placeholder removed below
		{"unknown title", "/v1/titles/99/entry", `{"favorite":true}`, http.StatusNotFound},
		{"non-numeric id", "/v1/titles/abc/entry", `{"favorite":true}`, http.StatusNotFound},
		{"malformed json", "/v1/titles/1/entry", `{`, http.StatusBadRequest},
		{"empty body object", "/v1/titles/1/entry", `{}`, http.StatusUnprocessableEntity},
		{"bad status", "/v1/titles/1/entry", `{"status":"queued"}`, http.StatusUnprocessableEntity},
		{"rating too low", "/v1/titles/1/entry", `{"rating":0.4}`, http.StatusUnprocessableEntity},
		{"rating not half step", "/v1/titles/1/entry", `{"rating":3.7}`, http.StatusUnprocessableEntity},
		{"rating too high", "/v1/titles/1/entry", `{"rating":5.5}`, http.StatusUnprocessableEntity},
		{"rating wrong type", "/v1/titles/1/entry", `{"rating":"five"}`, http.StatusUnprocessableEntity},
		{"pointer zero season", "/v1/titles/1/entry", `{"pointer":{"season":0,"episode":1}}`, http.StatusUnprocessableEntity},
		{"pointer zero episode", "/v1/titles/1/entry", `{"pointer":{"season":1,"episode":0}}`, http.StatusUnprocessableEntity},
		{"valid rating", "/v1/titles/1/entry", `{"rating":4.5}`, http.StatusOK},
		{"valid status", "/v1/titles/1/entry", `{"status":"watchlist"}`, http.StatusOK},
	}
	for _, tc := range cases {
		if tc.path == "" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, tc.path, "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d, want %d (body %s)", tc.name, tc.body, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPatchEntryRotationCap(t *testing.T) {
	ids := make([]int64, 9)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	h, fe := entriesServer(t, ids...)

	for i := int64(1); i <= 8; i++ {
		rec := do(t, h, http.MethodPatch, fmt.Sprintf("/v1/titles/%d/entry", i), "tok-1", `{"status":"rotation"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("adding %d = %d (body %s)", i, rec.Code, rec.Body.String())
		}
	}
	// 9th title: cap hit.
	rec := do(t, h, http.MethodPatch, "/v1/titles/9/entry", "tok-1", `{"status":"rotation"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "rotation_full") {
		t.Fatalf("9th rotation = %d body %s, want 409 rotation_full", rec.Code, rec.Body.String())
	}
	// Re-setting an existing rotation title at cap: idempotent, 200.
	rec = do(t, h, http.MethodPatch, "/v1/titles/3/entry", "tok-1", `{"status":"rotation"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent re-set = %d, want 200", rec.Code)
	}
	// Other fields untouched by cap: rating a 9th title is fine.
	rec = do(t, h, http.MethodPatch, "/v1/titles/9/entry", "tok-1", `{"rating":5.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rating at cap = %d, want 200", rec.Code)
	}
	if fe.entries[9] != nil && fe.entries[9].Status == "rotation" {
		t.Fatal("9th title must not have entered rotation")
	}
}

func TestPatchEntryWatchedAndClear(t *testing.T) {
	h, fe := entriesServer(t, 1)

	rec := do(t, h, http.MethodPatch, "/v1/titles/1/entry", "tok-1", `{"status":"watched","rating":4.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("watched = %d", rec.Code)
	}
	var got store.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body: %v", err)
	}
	if got.WatchedAt == nil || got.Rating == nil || *got.Rating != 4.0 {
		t.Fatalf("watched entry = %+v, want watched_at + rating set", got)
	}

	rec = do(t, h, http.MethodPatch, "/v1/titles/1/entry", "tok-1", `{"rating":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear rating = %d", rec.Code)
	}
	if fe.entries[1].Rating != nil {
		t.Fatal("rating not cleared")
	}
	if fe.entries[1].WatchedAt == nil {
		t.Fatal("watched_at lost on unrelated update")
	}
}

func TestGetShelf(t *testing.T) {
	h, _ := entriesServer(t, 1, 2, 3)
	seed := func(id int64, body string) {
		if rec := do(t, h, http.MethodPatch, fmt.Sprintf("/v1/titles/%d/entry", id), "tok-1", body); rec.Code != http.StatusOK {
			t.Fatalf("seed %d: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	seed(1, `{"status":"watchlist"}`)
	seed(2, `{"status":"rotation","favorite":true}`)
	seed(3, `{"status":"watched","rating":4.5}`)

	cases := []struct {
		shelf string
		want  []int64
	}{
		{"watchlist", []int64{1}},
		{"rotation", []int64{2}},
		{"watched", []int64{3}},
		{"favorites", []int64{2}},
		{"ratings", []int64{3}},
	}
	for _, tc := range cases {
		t.Run(tc.shelf, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/v1/me/shelves/"+tc.shelf, "tok-1", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("shelf %s = %d", tc.shelf, rec.Code)
			}
			var body struct {
				Entries []store.Entry `json:"entries"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body: %v", err)
			}
			ids := []int64{}
			for _, e := range body.Entries {
				ids = append(ids, e.TitleID)
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("shelf %s ids = %v, want %v", tc.shelf, ids, tc.want)
			}
		})
	}

	if rec := do(t, h, http.MethodGet, "/v1/me/shelves/bogus", "tok-1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bogus shelf = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/v1/me/shelves/watchlist", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated shelf = %d, want 401", rec.Code)
	}

	// Empty shelf serializes as [] not null.
	h2, _ := entriesServer(t, 1)
	rec := do(t, h2, http.MethodGet, "/v1/me/shelves/watchlist", "tok-1", "")
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("empty shelf body = %s, want entries:[]", rec.Body.String())
	}
}
```

Also in this step: extract the fake-verifier construction used by
`testServer` (me_test.go) into a helper `fakeVerifierWithTok1()` so both
test files share it, and remove the placeholder first case from
`TestPatchEntryValidation`'s table (the `tc.path == "" { continue }` guard
exists only if you keep it — prefer deleting that row entirely).

- [ ] **Step 2: Run to verify failure** — `cd api && go test ./internal/httpserver/` → FAIL (undefined EntryStore, Deps.Entries, routes 404).

- [ ] **Step 3: Implement**

```go
// api/internal/httpserver/entries.go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/store"
)

// EntryStore is the slice of *store.Store the shelf/entry handlers need.
type EntryStore interface {
	UpsertEntry(ctx context.Context, userID, titleID int64, u store.EntryUpdate) (*store.Entry, error)
	CountRotation(ctx context.Context, userID, excludeTitleID int64) (int, error)
	Shelf(ctx context.Context, userID int64, shelf string) ([]store.Entry, error)
}

// rotationCap is fixed at 8 in v1 (design spec).
const rotationCap = 8

var validStatuses = map[string]bool{"none": true, "watchlist": true, "rotation": true, "watched": true}

var validShelves = map[string]bool{"watchlist": true, "rotation": true, "watched": true, "favorites": true, "ratings": true}

// validRating reports whether v is a half-step in [0.5, 5.0]. The DB CHECK
// covers the range; half-step granularity is API policy.
func validRating(v float64) bool {
	d := v * 2
	return v >= 0.5 && v <= 5.0 && d == math.Trunc(d)
}

func handlePatchEntry(entries EntryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		titleID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || titleID < 1 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		var body struct {
			Status   *string         `json:"status"`
			Rating   json.RawMessage `json:"rating"`
			Favorite *bool           `json:"favorite"`
			Pointer  *store.Pointer  `json:"pointer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Status == nil && body.Rating == nil && body.Favorite == nil && body.Pointer == nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "nothing to update")
			return
		}
		if body.Status != nil && !validStatuses[*body.Status] {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid status")
			return
		}
		u := store.EntryUpdate{Status: body.Status, Favorite: body.Favorite, Pointer: body.Pointer}
		if body.Rating != nil {
			if string(body.Rating) == "null" {
				u.ClearRating = true
			} else {
				var v float64
				if err := json.Unmarshal(body.Rating, &v); err != nil || !validRating(v) {
					writeJSONError(w, http.StatusUnprocessableEntity, "invalid rating")
					return
				}
				u.Rating = &v
			}
		}
		if body.Pointer != nil && (body.Pointer.Season < 1 || body.Pointer.Episode < 1) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid pointer")
			return
		}

		user := userFrom(r.Context())
		if body.Status != nil && *body.Status == "rotation" {
			n, cerr := entries.CountRotation(r.Context(), user.ID, titleID)
			if cerr != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal")
				return
			}
			if n >= rotationCap {
				writeJSONError(w, http.StatusConflict, "rotation_full")
				return
			}
		}

		e, err := entries.UpsertEntry(r.Context(), user.ID, titleID, u)
		switch {
		case errors.Is(err, store.ErrTitleNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(e)
	}
}

func handleGetShelf(entries EntryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shelf := chi.URLParam(r, "shelf")
		if !validShelves[shelf] {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		user := userFrom(r.Context())
		list, err := entries.Shelf(r.Context(), user.ID, shelf)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]store.Entry{"entries": list})
	}
}
```

`server.go`: `Deps` gains `Entries EntryStore`; inside the `/v1` group add:

```go
			if d.Entries != nil {
				v1.Patch("/titles/{id}/entry", handlePatchEntry(d.Entries))
				v1.Get("/me/shelves/{shelf}", handleGetShelf(d.Entries))
			}
```

`main.go`: `httpserver.Deps{Store: st, Users: st, Verifier: verifier, Entries: st}`.

`me_test.go`: extract `fakeVerifierWithTok1()` (returns the existing
`&fbauth.Fake{Tokens: map[string]fbauth.Identity{"tok-1": …}}`) and use it
in `testServer` too.

- [ ] **Step 4: Full suite** — `cd api && go vet ./... && go test ./...` (both env modes) → green.
- [ ] **Step 5: Commit**

```bash
git add api/
git commit -m "feat(api): entry PATCH and shelf endpoints with rotation cap"
```

### Task 3: PR (controller-inline)

- [ ] **Step 1:** Clean-state `go vet ./... && go test ./...`; with TEST_DATABASE_URL run the store integration suite once more.
- [ ] **Step 2:** Push; PR `feat(api): shelves and rotation entry endpoints` — closes #12; notes the v1 cap race acceptance and the count-exclusion idempotency design.
- [ ] **Step 3:** CI green → user squash-merges.

---

## Self-review notes

- Acceptance coverage: cap 409 (+idempotent re-set + rating-at-cap), every 422 branch, watched stamp + preservation, rating clear, every shelf filter incl. empty-shelf `[]`, 404s (title, non-numeric id, bogus shelf), 401. Store: partial semantics, count exclusion, ordering (rating DESC proven with 4.5 > 2.0), FK→ErrTitleNotFound.
- Type consistency: `EntryStore` signatures match Task 1 methods verbatim; fake mirrors watched/clear/exclusion semantics; `rotationCap` defined once.
- `entryColumns`/`scanEntry` shared between UpsertEntry and Shelf keeps the JSON shape single-sourced.
- seedTitle uses `time.Now().UnixNano()` as tmdb_id to dodge the UNIQUE(kind,tmdb_id) constraint across reruns.
