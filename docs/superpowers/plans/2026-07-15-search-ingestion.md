# Search + Title Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /v1/search` (thin TMDB proxy) and `GET /v1/titles/{kind}/{tmdbID}` (ingest-if-absent title page payload), backed by `ingest.EnsureTitle` with per-class TTL refresh and stale-cache fallback, per issue #11 and the approved spec `docs/superpowers/specs/2026-07-15-search-ingestion-design.md`.

**Architecture:** Three layers on existing seams: `store/titles.go` (SQL, live-PG tests), new `internal/ingest` package orchestrating TMDB/TVMaze/store behind ingest-side interfaces (fake-driven tests), and thin chi handlers (httptest with fakes). TTL stamps are written inside store methods; staleness decisions live in ingest.

**Tech Stack:** Go (api/go.mod), pgx v5, chi v5, existing `internal/tmdb` + `internal/tvmaze` clients. No new dependencies.

## Global Constraints

- Branch: `feat/11-search-ingestion` (exists). Never touch main.
- No new Go dependencies. `go test ./...` must pass offline (store tests self-skip without `TEST_DATABASE_URL`).
- Error strings prefixed by package (`store:`, `ingest:`). API error envelope is `writeJSONError` (`{"error":"<code>"}`).
- Kind vocabulary: `"movie"`|`"series"`. API JSON is snake_case.
- TTLs: metadata 30d, providers 7d, airings 1d. Timestamps stamped inside store methods; epoch default means "immediately stale".
- Store tests run against local Postgres: `docker start lineup-pg` (or the `docker run` in `infra/local-dev.md` §1), then
  `TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable'`.
- Before every commit: `cd api && gofmt -l ./... ` prints nothing, `go vet ./...` clean.
- No infra/gcloud work anywhere in this issue (local-only posture, 2026-07-14).

---

### Task 1: Store layer — `titles.go`

**Files:**
- Create: `api/internal/store/titles.go`
- Test: `api/internal/store/titles_test.go`

**Interfaces:**
- Consumes: `Store.Pool` (store.go:20), `entryColumns`/`scanEntry`/`Entry` (entries.go:50-61), `ErrTitleNotFound` (entries.go:15), test helpers `testStore(t)`/`seedUser(t, s)` (users_test.go:15, entries_test.go:25).
- Produces (Task 2 and 3 rely on these exact signatures):
  - `store.Title{ID, TMDBID int64; Kind string; TVMazeID *int64; Name, Overview, PosterPath string; RuntimeMinutes int; Airing bool; RefreshedAt, ProvidersRefreshedAt, AiringsRefreshedAt time.Time}`
  - `store.TitleUpsert{TMDBID int64; Kind string; TVMazeID *int64; Name, Overview, PosterPath string; RuntimeMinutes int; Airing bool}`
  - `store.SeasonRow{Number, EpisodeCount int}`, `store.ProviderRow{ID int64; Name, LogoPath string}`, `store.AiringRow{Season, Episode int; AirDate string}`
  - `store.TitleFull{Title Title; Seasons []SeasonRow; Providers []ProviderRow; Entry *Entry}`
  - `(*Store).UpsertTitle(ctx, TitleUpsert) (*Title, error)`
  - `(*Store).GetTitleByTMDB(ctx, kind string, tmdbID int64) (*Title, error)` — `(nil, nil)` when absent
  - `(*Store).ReplaceSeasons(ctx, titleID int64, seasons []SeasonRow) error`
  - `(*Store).ReplaceProviders(ctx, titleID int64, region string, provs []ProviderRow) error`
  - `(*Store).ReplaceFutureAirings(ctx, titleID int64, today string, airs []AiringRow) error`
  - `(*Store).GetTitleFull(ctx, userID, titleID int64, region string) (*TitleFull, error)`

- [ ] **Step 1: Write the failing tests**

Create `api/internal/store/titles_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func int64p(v int64) *int64 { return &v }

// uniqueTMDBID returns a per-test-run unique fake TMDB id so tests never
// collide on the (kind, tmdb_id) unique key across runs.
func uniqueTMDBID() int64 { return time.Now().UnixNano() }

func TestUpsertTitleInsertAndRefresh(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tmdbID := uniqueTMDBID()

	ti, err := s.UpsertTitle(ctx, TitleUpsert{
		TMDBID: tmdbID, Kind: "series", TVMazeID: int64p(82),
		Name: "Upsert Show", Overview: "o", PosterPath: "/p.jpg",
		RuntimeMinutes: 60, Airing: true,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if ti.ID == 0 || ti.TMDBID != tmdbID || ti.Kind != "series" || ti.TVMazeID == nil || *ti.TVMazeID != 82 ||
		ti.Name != "Upsert Show" || ti.RuntimeMinutes != 60 || !ti.Airing {
		t.Fatalf("insert title = %+v", ti)
	}
	if time.Since(ti.RefreshedAt) > time.Minute {
		t.Fatalf("refreshed_at not stamped on insert: %v", ti.RefreshedAt)
	}
	// Provider/airing stamps default to epoch = immediately stale.
	if ti.ProvidersRefreshedAt.Year() != 1970 || ti.AiringsRefreshedAt.Year() != 1970 {
		t.Fatalf("class stamps not epoch: %v / %v", ti.ProvidersRefreshedAt, ti.AiringsRefreshedAt)
	}

	// Refresh without TVMazeID: metadata updates, tvmaze_id survives
	// (COALESCE), refreshed_at re-stamps, other stamps untouched.
	first := ti.RefreshedAt
	ti2, err := s.UpsertTitle(ctx, TitleUpsert{
		TMDBID: tmdbID, Kind: "series",
		Name: "Upsert Show S2", Overview: "o2", PosterPath: "/p2.jpg",
		RuntimeMinutes: 55, Airing: false,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if ti2.ID != ti.ID || ti2.Name != "Upsert Show S2" || ti2.RuntimeMinutes != 55 || ti2.Airing {
		t.Fatalf("refresh title = %+v", ti2)
	}
	if ti2.TVMazeID == nil || *ti2.TVMazeID != 82 {
		t.Fatalf("tvmaze_id clobbered on refresh: %v", ti2.TVMazeID)
	}
	if ti2.RefreshedAt.Before(first) {
		t.Fatalf("refreshed_at not re-stamped: %v < %v", ti2.RefreshedAt, first)
	}
}

func TestGetTitleByTMDBAbsent(t *testing.T) {
	s := testStore(t)
	ti, err := s.GetTitleByTMDB(context.Background(), "movie", uniqueTMDBID())
	if err != nil || ti != nil {
		t.Fatalf("absent title = %+v, err %v; want nil, nil", ti, err)
	}
}

func TestReplaceSeasons(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Seasons"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.ReplaceSeasons(ctx, ti.ID, []SeasonRow{{Number: 1, EpisodeCount: 10}, {Number: 2, EpisodeCount: 8}}); err != nil {
		t.Fatalf("replace 2: %v", err)
	}
	if err := s.ReplaceSeasons(ctx, ti.ID, []SeasonRow{{Number: 1, EpisodeCount: 11}}); err != nil {
		t.Fatalf("replace 1: %v", err)
	}
	full, err := s.GetTitleFull(ctx, seedUser(t, s), ti.ID, "US")
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if len(full.Seasons) != 1 || full.Seasons[0].Number != 1 || full.Seasons[0].EpisodeCount != 11 {
		t.Fatalf("seasons after replace = %+v", full.Seasons)
	}
}

func TestReplaceProvidersRegionScoped(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "movie", Name: "Providers"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ti.ProvidersRefreshedAt

	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}, {ID: 9, Name: "Max", LogoPath: "/m.jpg"}}); err != nil {
		t.Fatalf("US: %v", err)
	}
	if err := s.ReplaceProviders(ctx, ti.ID, "GB", []ProviderRow{{ID: 9, Name: "Max", LogoPath: "/m.jpg"}}); err != nil {
		t.Fatalf("GB: %v", err)
	}
	// Re-replace US; GB must be untouched, dictionary row updated.
	if err := s.ReplaceProviders(ctx, ti.ID, "US", []ProviderRow{{ID: 8, Name: "Netflix Renamed", LogoPath: "/n2.jpg"}}); err != nil {
		t.Fatalf("US re-replace: %v", err)
	}

	us, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full US: %v", err)
	}
	if len(us.Providers) != 1 || us.Providers[0].ID != 8 || us.Providers[0].Name != "Netflix Renamed" || us.Providers[0].LogoPath != "/n2.jpg" {
		t.Fatalf("US providers = %+v", us.Providers)
	}
	gb, err := s.GetTitleFull(ctx, uid, ti.ID, "GB")
	if err != nil {
		t.Fatalf("full GB: %v", err)
	}
	if len(gb.Providers) != 1 || gb.Providers[0].ID != 9 {
		t.Fatalf("GB providers = %+v", gb.Providers)
	}
	if !us.Title.ProvidersRefreshedAt.After(before) {
		t.Fatalf("providers_refreshed_at not stamped: %v", us.Title.ProvidersRefreshedAt)
	}
}

func TestReplaceFutureAirings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	uid := seedUser(t, s)
	ti, err := s.UpsertTitle(ctx, TitleUpsert{TMDBID: uniqueTMDBID(), Kind: "series", Name: "Airings"})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := ti.AiringsRefreshedAt

	// Two future rows relative to a fixed "today" far in the future so the
	// test never rots.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{
		{Season: 1, Episode: 1, AirDate: "2100-01-02"},
		{Season: 1, Episode: 2, AirDate: "2100-01-03"},
	}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	// Later "today": S1E1 (2100-01-02) is now past and must survive the
	// delete; S1E2 (>= today) is deleted; S1E3 inserted.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-03", []AiringRow{
		{Season: 1, Episode: 3, AirDate: "2100-01-04"},
	}); err != nil {
		t.Fatalf("second replace: %v", err)
	}
	rows, err := s.Pool.Query(ctx,
		`SELECT season, episode, to_char(air_date, 'YYYY-MM-DD') FROM title_airings WHERE title_id = $1 ORDER BY season, episode`, ti.ID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	type air struct {
		s, e int
		d    string
	}
	var got []air
	for rows.Next() {
		var a air
		if err := rows.Scan(&a.s, &a.e, &a.d); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, a)
	}
	want := []air{{1, 1, "2100-01-02"}, {1, 3, "2100-01-04"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("airings = %+v, want %+v", got, want)
	}

	// A rescheduled episode (same PK, new future date) upserts, not dupes.
	if err := s.ReplaceFutureAirings(ctx, ti.ID, "2100-01-01", []AiringRow{
		{Season: 1, Episode: 1, AirDate: "2100-02-01"},
	}); err != nil {
		t.Fatalf("reschedule: %v", err)
	}
	var d string
	if err := s.Pool.QueryRow(ctx,
		`SELECT to_char(air_date, 'YYYY-MM-DD') FROM title_airings WHERE title_id = $1 AND season = 1 AND episode = 1`, ti.ID).Scan(&d); err != nil {
		t.Fatalf("reschedule query: %v", err)
	}
	if d != "2100-02-01" {
		t.Fatalf("rescheduled air_date = %s, want 2100-02-01", d)
	}

	full, err := s.GetTitleFull(ctx, uid, ti.ID, "US")
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if !full.Title.AiringsRefreshedAt.After(before) {
		t.Fatalf("airings_refreshed_at not stamped")
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
	if full.Entry == nil || full.Entry.Status != "watchlist" || full.Entry.TitleID != ti.ID {
		t.Fatalf("entry = %+v", full.Entry)
	}

	if _, err := s.GetTitleFull(ctx, uid, -1, "US"); err == nil {
		t.Fatal("unknown title id: want error")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `docker start lineup-pg 2>/dev/null || docker run -d --name lineup-pg -p 127.0.0.1:5433:5432 -e POSTGRES_USER=lineup -e POSTGRES_PASSWORD=lineup -e POSTGRES_DB=lineup postgres:16`
Then: `cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/ -run 'TestUpsertTitle|TestGetTitleBy|TestReplace|TestGetTitleFull'`
Expected: FAIL — compile errors (`undefined: TitleUpsert` etc.).

- [ ] **Step 3: Implement `titles.go`**

Create `api/internal/store/titles.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Title mirrors one titles row. The refresh stamps drive ingest's TTL
// decisions and are internal — they never appear in API JSON.
type Title struct {
	ID                   int64     `json:"id"`
	TMDBID               int64     `json:"tmdb_id"`
	Kind                 string    `json:"kind"`
	TVMazeID             *int64    `json:"-"`
	Name                 string    `json:"name"`
	Overview             string    `json:"overview"`
	PosterPath           string    `json:"poster_path"`
	RuntimeMinutes       int       `json:"runtime_minutes"`
	Airing               bool      `json:"airing"`
	RefreshedAt          time.Time `json:"-"`
	ProvidersRefreshedAt time.Time `json:"-"`
	AiringsRefreshedAt   time.Time `json:"-"`
}

// TitleUpsert carries the metadata class of a title write.
type TitleUpsert struct {
	TMDBID         int64
	Kind           string
	TVMazeID       *int64
	Name           string
	Overview       string
	PosterPath     string
	RuntimeMinutes int
	Airing         bool
}

// SeasonRow is one non-specials season; EpisodeCount drives pointer
// advancement.
type SeasonRow struct {
	Number       int `json:"number"`
	EpisodeCount int `json:"episode_count"`
}

// ProviderRow is one streaming provider offering a title in some region.
type ProviderRow struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	LogoPath string `json:"logo_path"`
}

// AiringRow is one episode air date ("YYYY-MM-DD").
type AiringRow struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	AirDate string `json:"air_date"`
}

// TitleFull is the title-page payload: the title, its seasons, the
// region's providers, and the caller's entry (nil when the user has no
// row yet).
type TitleFull struct {
	Title     Title         `json:"title"`
	Seasons   []SeasonRow   `json:"seasons"`
	Providers []ProviderRow `json:"providers"`
	Entry     *Entry        `json:"entry"`
}

const titleColumns = `id, tmdb_id, kind, tvmaze_id, name, overview, poster_path, runtime_minutes, airing, refreshed_at, providers_refreshed_at, airings_refreshed_at`

func scanTitle(row pgx.Row) (*Title, error) {
	t := &Title{}
	err := row.Scan(&t.ID, &t.TMDBID, &t.Kind, &t.TVMazeID, &t.Name, &t.Overview, &t.PosterPath,
		&t.RuntimeMinutes, &t.Airing, &t.RefreshedAt, &t.ProvidersRefreshedAt, &t.AiringsRefreshedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// UpsertTitle inserts or refreshes the metadata class of a title, stamping
// refreshed_at. tvmaze_id is COALESCEd so a refresh that skipped the
// TVMaze lookup never clobbers a stored id. Provider/airing stamps are
// deliberately untouched — they belong to their Replace methods.
func (s *Store) UpsertTitle(ctx context.Context, u TitleUpsert) (*Title, error) {
	q := `
INSERT INTO titles (tmdb_id, kind, tvmaze_id, name, overview, poster_path, runtime_minutes, airing)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (kind, tmdb_id) DO UPDATE SET
  tvmaze_id       = COALESCE(EXCLUDED.tvmaze_id, titles.tvmaze_id),
  name            = EXCLUDED.name,
  overview        = EXCLUDED.overview,
  poster_path     = EXCLUDED.poster_path,
  runtime_minutes = EXCLUDED.runtime_minutes,
  airing          = EXCLUDED.airing,
  refreshed_at    = now()
RETURNING ` + titleColumns
	t, err := scanTitle(s.Pool.QueryRow(ctx, q,
		u.TMDBID, u.Kind, u.TVMazeID, u.Name, u.Overview, u.PosterPath, u.RuntimeMinutes, u.Airing))
	if err != nil {
		return nil, fmt.Errorf("store: upsert title: %w", err)
	}
	return t, nil
}

// GetTitleByTMDB returns the title for (kind, tmdbID), or (nil, nil) when
// no row exists — ingest-if-absent branches on nil rather than an error.
func (s *Store) GetTitleByTMDB(ctx context.Context, kind string, tmdbID int64) (*Title, error) {
	t, err := scanTitle(s.Pool.QueryRow(ctx,
		`SELECT `+titleColumns+` FROM titles WHERE kind = $1 AND tmdb_id = $2`, kind, tmdbID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get title by tmdb: %w", err)
	}
	return t, nil
}

// ReplaceSeasons replaces a title's season rows in one transaction.
func (s *Store) ReplaceSeasons(ctx context.Context, titleID int64, seasons []SeasonRow) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: replace seasons: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM title_seasons WHERE title_id = $1`, titleID); err != nil {
		return fmt.Errorf("store: replace seasons: delete: %w", err)
	}
	for _, se := range seasons {
		if _, err := tx.Exec(ctx,
			`INSERT INTO title_seasons (title_id, season_number, episode_count) VALUES ($1, $2, $3)`,
			titleID, se.Number, se.EpisodeCount); err != nil {
			return fmt.Errorf("store: replace seasons: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: replace seasons: commit: %w", err)
	}
	return nil
}

// ReplaceProviders upserts the provider dictionary rows and replaces the
// title's availability for one region, stamping providers_refreshed_at.
// Other regions' availability is untouched.
func (s *Store) ReplaceProviders(ctx context.Context, titleID int64, region string, provs []ProviderRow) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: replace providers: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, p := range provs {
		if _, err := tx.Exec(ctx, `
INSERT INTO providers (id, name, logo_path) VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, logo_path = EXCLUDED.logo_path`,
			p.ID, p.Name, p.LogoPath); err != nil {
			return fmt.Errorf("store: replace providers: dictionary: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM title_providers WHERE title_id = $1 AND region = $2`, titleID, region); err != nil {
		return fmt.Errorf("store: replace providers: delete: %w", err)
	}
	for _, p := range provs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO title_providers (title_id, region, provider_id) VALUES ($1, $2, $3)`,
			titleID, region, p.ID); err != nil {
			return fmt.Errorf("store: replace providers: insert: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE titles SET providers_refreshed_at = now() WHERE id = $1`, titleID); err != nil {
		return fmt.Errorf("store: replace providers: stamp: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: replace providers: commit: %w", err)
	}
	return nil
}

// ReplaceFutureAirings deletes airings on/after today and inserts the
// given (caller-pre-filtered) future set, stamping airings_refreshed_at.
// Past airings are never touched; a rescheduled episode upserts its date.
func (s *Store) ReplaceFutureAirings(ctx context.Context, titleID int64, today string, airs []AiringRow) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: replace airings: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM title_airings WHERE title_id = $1 AND air_date >= $2`, titleID, today); err != nil {
		return fmt.Errorf("store: replace airings: delete: %w", err)
	}
	for _, a := range airs {
		if _, err := tx.Exec(ctx, `
INSERT INTO title_airings (title_id, season, episode, air_date) VALUES ($1, $2, $3, $4)
ON CONFLICT (title_id, season, episode) DO UPDATE SET air_date = EXCLUDED.air_date`,
			titleID, a.Season, a.Episode, a.AirDate); err != nil {
			return fmt.Errorf("store: replace airings: insert: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE titles SET airings_refreshed_at = now() WHERE id = $1`, titleID); err != nil {
		return fmt.Errorf("store: replace airings: stamp: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: replace airings: commit: %w", err)
	}
	return nil
}

// GetTitleFull assembles the title-page payload: title row, seasons, the
// region's providers, and the caller's entry (nil when absent). Unknown
// titleID returns ErrTitleNotFound.
func (s *Store) GetTitleFull(ctx context.Context, userID, titleID int64, region string) (*TitleFull, error) {
	t, err := scanTitle(s.Pool.QueryRow(ctx,
		`SELECT `+titleColumns+` FROM titles WHERE id = $1`, titleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTitleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: title full: title: %w", err)
	}
	full := &TitleFull{Title: *t, Seasons: []SeasonRow{}, Providers: []ProviderRow{}}

	rows, err := s.Pool.Query(ctx,
		`SELECT season_number, episode_count FROM title_seasons WHERE title_id = $1 ORDER BY season_number`, titleID)
	if err != nil {
		return nil, fmt.Errorf("store: title full: seasons: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var se SeasonRow
		if err := rows.Scan(&se.Number, &se.EpisodeCount); err != nil {
			return nil, fmt.Errorf("store: title full: seasons scan: %w", err)
		}
		full.Seasons = append(full.Seasons, se)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: title full: seasons rows: %w", err)
	}

	prows, err := s.Pool.Query(ctx, `
SELECT p.id, p.name, p.logo_path
FROM title_providers tp JOIN providers p ON p.id = tp.provider_id
WHERE tp.title_id = $1 AND tp.region = $2 ORDER BY p.name`, titleID, region)
	if err != nil {
		return nil, fmt.Errorf("store: title full: providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var p ProviderRow
		if err := prows.Scan(&p.ID, &p.Name, &p.LogoPath); err != nil {
			return nil, fmt.Errorf("store: title full: providers scan: %w", err)
		}
		full.Providers = append(full.Providers, p)
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("store: title full: providers rows: %w", err)
	}

	e, err := scanEntry(s.Pool.QueryRow(ctx, `SELECT `+fmt.Sprintf(entryColumns, "ut")+`
FROM user_titles ut JOIN titles t ON t.id = ut.title_id
WHERE ut.user_id = $1 AND ut.title_id = $2`, userID, titleID))
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No entry yet — a normal state, rendered as null.
	case err != nil:
		return nil, fmt.Errorf("store: title full: entry: %w", err)
	default:
		full.Entry = e
	}
	return full, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./internal/store/ && go vet ./internal/store/ && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/`
Expected: gofmt prints nothing; ALL store tests PASS (new and pre-existing). Then run `go test ./internal/store/` WITHOUT the env var — everything skips, proving hermetic CI stays green.

- [ ] **Step 5: Commit**

```bash
git add api/internal/store/titles.go api/internal/store/titles_test.go
git commit -m "feat(api): title store methods with TTL stamps"
```

---

### Task 2: `internal/ingest` — EnsureTitle

**Files:**
- Create: `api/internal/ingest/ingest.go`
- Test: `api/internal/ingest/ingest_test.go`

**Interfaces:**
- Consumes (exact, from Task 1 and existing clients): every `store` symbol in Task 1's Produces; `tmdb.Movie{TMDBID int64; Name, Overview, PosterPath string; RuntimeMinutes int}`, `tmdb.TV{TMDBID int64; Name, Overview, PosterPath string; RuntimeMinutes int; Airing bool; IMDBID string; Seasons []tmdb.Season}`, `tmdb.Season{Number, EpisodeCount int}`, `tmdb.Provider{ID int64; Name, LogoPath string}`, `tmdb.ErrNotFound`; `tvmaze.Show{ID int; Name, Status string}`, `tvmaze.Episode{Season, Number int; AirDate string}`.
- Produces (Task 3 relies on):
  - `ingest.Service{TMDB MetadataClient; TVMaze AiringsClient; Store TitleStore; Now func() time.Time}`
  - `(*Service).EnsureTitle(ctx context.Context, kind string, tmdbID int64, region string) (*store.Title, error)`
  - `ingest.ErrBadKind`, `ingest.ErrTitleNotFound`

- [ ] **Step 1: Write the failing tests**

Create `api/internal/ingest/ingest_test.go`:

```go
package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
	"github.com/shottah/lineup/api/internal/tvmaze"
)

// now is the fixed test clock: 2026-07-15 noon UTC.
var now = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

type fakeTMDB struct {
	movie                          tmdb.Movie
	tv                             tmdb.TV
	provs                          []tmdb.Provider
	movieErr, tvErr, provErr       error
	movieCalls, tvCalls, provCalls int
	provRegion                     string
}

func (f *fakeTMDB) MovieDetails(_ context.Context, _ int64) (tmdb.Movie, error) {
	f.movieCalls++
	return f.movie, f.movieErr
}
func (f *fakeTMDB) TVDetails(_ context.Context, _ int64) (tmdb.TV, error) {
	f.tvCalls++
	return f.tv, f.tvErr
}
func (f *fakeTMDB) WatchProviders(_ context.Context, _ string, _ int64, region string) ([]tmdb.Provider, error) {
	f.provCalls++
	f.provRegion = region
	return f.provs, f.provErr
}

type fakeTVMaze struct {
	show                   tvmaze.Show
	eps                    []tvmaze.Episode
	lookupErr, epsErr      error
	lookupCalls, epsCalls  int
}

func (f *fakeTVMaze) LookupByIMDB(_ context.Context, _ string) (tvmaze.Show, error) {
	f.lookupCalls++
	if f.lookupErr != nil {
		return tvmaze.Show{}, f.lookupErr
	}
	return f.show, nil
}
func (f *fakeTVMaze) Episodes(_ context.Context, _ int) ([]tvmaze.Episode, error) {
	f.epsCalls++
	if f.epsErr != nil {
		return nil, f.epsErr
	}
	return f.eps, nil
}

// fakeStore mimics the SQL semantics ingest depends on: COALESCE'd
// tvmaze_id, refreshed_at stamping on upsert, class stamps on replaces.
type fakeStore struct {
	title                                       *store.Title
	seasons                                     []store.SeasonRow
	provsByRegion                               map[string][]store.ProviderRow
	airings                                     []store.AiringRow
	airToday                                    string
	getErr, upsertErr, seasonsErr, provsErr, airErr error
	upserts, seasonCalls, provCalls, airCalls   int
}

func (f *fakeStore) GetTitleByTMDB(_ context.Context, _ string, _ int64) (*store.Title, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.title == nil {
		return nil, nil
	}
	cp := *f.title
	return &cp, nil
}

func (f *fakeStore) UpsertTitle(_ context.Context, u store.TitleUpsert) (*store.Title, error) {
	f.upserts++
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if f.title == nil {
		f.title = &store.Title{ID: 1}
	}
	f.title.TMDBID, f.title.Kind = u.TMDBID, u.Kind
	f.title.Name, f.title.Overview, f.title.PosterPath = u.Name, u.Overview, u.PosterPath
	f.title.RuntimeMinutes, f.title.Airing = u.RuntimeMinutes, u.Airing
	if u.TVMazeID != nil {
		f.title.TVMazeID = u.TVMazeID
	}
	f.title.RefreshedAt = now
	cp := *f.title
	return &cp, nil
}

func (f *fakeStore) ReplaceSeasons(_ context.Context, _ int64, seasons []store.SeasonRow) error {
	f.seasonCalls++
	if f.seasonsErr != nil {
		return f.seasonsErr
	}
	f.seasons = seasons
	return nil
}

func (f *fakeStore) ReplaceProviders(_ context.Context, _ int64, region string, provs []store.ProviderRow) error {
	f.provCalls++
	if f.provsErr != nil {
		return f.provsErr
	}
	if f.provsByRegion == nil {
		f.provsByRegion = map[string][]store.ProviderRow{}
	}
	f.provsByRegion[region] = provs
	f.title.ProvidersRefreshedAt = now
	return nil
}

func (f *fakeStore) ReplaceFutureAirings(_ context.Context, _ int64, today string, airs []store.AiringRow) error {
	f.airCalls++
	if f.airErr != nil {
		return f.airErr
	}
	f.airToday, f.airings = today, airs
	f.title.AiringsRefreshedAt = now
	return nil
}

// svc builds a Service over the fakes with the fixed clock.
func svc(tm *fakeTMDB, tv *fakeTVMaze, st *fakeStore) *Service {
	return &Service{TMDB: tm, TVMaze: tv, Store: st, Now: func() time.Time { return now }}
}

// airingSeries is a cached series row with every class fresh as of `now`.
func airingSeries(tvmazeID *int64) *store.Title {
	return &store.Title{
		ID: 1, TMDBID: 1399, Kind: "series", TVMazeID: tvmazeID, Name: "GoT",
		Airing: true, RefreshedAt: now.Add(-time.Hour),
		ProvidersRefreshedAt: now.Add(-time.Hour), AiringsRefreshedAt: now.Add(-time.Hour),
	}
}

func TestEnsureTitleBadKind(t *testing.T) {
	tm, tv, st := &fakeTMDB{}, &fakeTVMaze{}, &fakeStore{}
	_, err := svc(tm, tv, st).EnsureTitle(context.Background(), "tv", 603, "US")
	if !errors.Is(err, ErrBadKind) {
		t.Fatalf("err = %v, want ErrBadKind", err)
	}
	if tm.movieCalls+tm.tvCalls+tm.provCalls+tv.lookupCalls+st.upserts != 0 {
		t.Fatal("bad kind must not touch upstreams or store")
	}
}

func TestFreshMovie(t *testing.T) {
	tm := &fakeTMDB{
		movie: tmdb.Movie{TMDBID: 603, Name: "The Matrix", Overview: "o", PosterPath: "/m.jpg", RuntimeMinutes: 136},
		provs: []tmdb.Provider{{ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}},
	}
	tv, st := &fakeTVMaze{}, &fakeStore{}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "movie", 603, "US")
	if err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if ti.Name != "The Matrix" || ti.Kind != "movie" || ti.RuntimeMinutes != 136 {
		t.Fatalf("title = %+v", ti)
	}
	if tm.movieCalls != 1 || tm.tvCalls != 0 || st.seasonCalls != 0 || tv.lookupCalls != 0 {
		t.Fatalf("calls: movie=%d tv=%d seasons=%d lookup=%d", tm.movieCalls, tm.tvCalls, st.seasonCalls, tv.lookupCalls)
	}
	if tm.provRegion != "US" || len(st.provsByRegion["US"]) != 1 || st.provsByRegion["US"][0].Name != "Netflix" {
		t.Fatalf("providers = %+v (region %q)", st.provsByRegion, tm.provRegion)
	}
}

func TestFreshAiringSeriesWithIMDB(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{
		TMDBID: 1399, Name: "GoT", Airing: true, IMDBID: "tt0944947",
		Seasons: []tmdb.Season{{Number: 1, EpisodeCount: 10}, {Number: 2, EpisodeCount: 10}},
	}}
	tv := &fakeTVMaze{show: tvmaze.Show{ID: 82}, eps: []tvmaze.Episode{
		{Season: 1, Number: 1, AirDate: "2020-01-01"}, // past — filtered
		{Season: 9, Number: 1, AirDate: ""},           // unscheduled — filtered
		{Season: 9, Number: 2, AirDate: "2026-07-15"}, // today — kept
		{Season: 9, Number: 3, AirDate: "2026-08-01"}, // future — kept
	}}
	st := &fakeStore{}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if ti.TVMazeID == nil || *ti.TVMazeID != 82 {
		t.Fatalf("tvmaze id = %v, want 82", ti.TVMazeID)
	}
	if len(st.seasons) != 2 || st.seasons[0].Number != 1 || st.seasons[0].EpisodeCount != 10 {
		t.Fatalf("seasons = %+v", st.seasons)
	}
	if st.airToday != "2026-07-15" {
		t.Fatalf("today = %q", st.airToday)
	}
	want := []store.AiringRow{{Season: 9, Episode: 2, AirDate: "2026-07-15"}, {Season: 9, Episode: 3, AirDate: "2026-08-01"}}
	if len(st.airings) != 2 || st.airings[0] != want[0] || st.airings[1] != want[1] {
		t.Fatalf("airings = %+v, want %+v", st.airings, want)
	}
}

func TestFreshSeriesWithoutIMDB(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 1, Name: "Obscure", Airing: true, IMDBID: ""}}
	tv, st := &fakeTVMaze{}, &fakeStore{}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.lookupCalls != 0 || st.airCalls != 0 {
		t.Fatalf("TVMaze touched without an IMDB id: lookups=%d airings=%d", tv.lookupCalls, st.airCalls)
	}
}

func TestFreshEndedSeriesSkipsTVMaze(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 2, Name: "Ended", Airing: false, IMDBID: "tt0000001"}}
	tv, st := &fakeTVMaze{}, &fakeStore{}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 2, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.lookupCalls != 0 || tv.epsCalls != 0 {
		t.Fatal("ended series must skip TVMaze entirely")
	}
}

func TestFreshTitleNotFound(t *testing.T) {
	tm := &fakeTMDB{movieErr: tmdb.ErrNotFound}
	_, err := svc(tm, &fakeTVMaze{}, &fakeStore{}).EnsureTitle(context.Background(), "movie", 999, "US")
	if !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("err = %v, want ErrTitleNotFound", err)
	}
}

func TestFreshTMDBDownErrors(t *testing.T) {
	tm := &fakeTMDB{movieErr: errors.New("boom")}
	st := &fakeStore{}
	_, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "movie", 603, "US")
	if err == nil {
		t.Fatal("want error on fresh ingest with TMDB down (nothing cached)")
	}
	if st.upserts != 0 {
		t.Fatal("no store writes on failed fresh ingest")
	}
}

func TestFreshProvidersDownDegrades(t *testing.T) {
	tm := &fakeTMDB{
		movie:   tmdb.Movie{TMDBID: 603, Name: "The Matrix"},
		provErr: errors.New("boom"),
	}
	st := &fakeStore{}
	ti, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "movie", 603, "US")
	if err != nil || ti == nil {
		t.Fatalf("providers-down fresh ingest must still land the title: %v", err)
	}
	if st.provCalls != 0 {
		t.Fatal("ReplaceProviders must not run when the fetch failed")
	}
}

func TestFreshTVMazeDownDegrades(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 1399, Name: "GoT", Airing: true, IMDBID: "tt0944947"}}
	tv := &fakeTVMaze{lookupErr: tvmaze.ErrNotFound}
	st := &fakeStore{}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil || ti == nil {
		t.Fatalf("TVMaze-down fresh ingest must still land the title: %v", err)
	}
	if st.airCalls != 0 {
		t.Fatal("no airings written when lookup failed")
	}
}

func TestMetadataRefreshAfterTTL(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.RefreshedAt = now.Add(-31 * 24 * time.Hour) // stale metadata
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 1399, Name: "GoT Renamed", Airing: true, IMDBID: "tt0944947",
		Seasons: []tmdb.Season{{Number: 1, EpisodeCount: 10}}}}
	tv, st := &fakeTVMaze{}, &fakeStore{title: cached}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tm.tvCalls != 1 || st.upserts != 1 || st.seasonCalls != 1 {
		t.Fatalf("calls: tv=%d upserts=%d seasons=%d, want 1/1/1", tm.tvCalls, st.upserts, st.seasonCalls)
	}
	if ti.Name != "GoT Renamed" {
		t.Fatalf("title = %+v", ti)
	}
	if tm.provCalls != 0 {
		t.Fatal("fresh providers class must not refresh")
	}
	if tv.lookupCalls != 0 {
		t.Fatal("stored tvmaze_id must not re-lookup")
	}
}

func TestProvidersRefreshOnly(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.ProvidersRefreshedAt = now.Add(-8 * 24 * time.Hour) // stale providers
	tm := &fakeTMDB{provs: []tmdb.Provider{{ID: 8, Name: "Netflix"}}}
	tv, st := &fakeTVMaze{}, &fakeStore{title: cached}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "GB"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tm.tvCalls != 0 || tm.provCalls != 1 || tv.epsCalls != 0 {
		t.Fatalf("calls: tv=%d prov=%d eps=%d, want 0/1/0", tm.tvCalls, tm.provCalls, tv.epsCalls)
	}
	if tm.provRegion != "GB" || len(st.provsByRegion["GB"]) != 1 {
		t.Fatalf("region = %q, provs = %+v", tm.provRegion, st.provsByRegion)
	}
}

func TestAiringsRefreshOnly(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.AiringsRefreshedAt = now.Add(-25 * time.Hour) // stale airings
	tm := &fakeTMDB{}
	tv := &fakeTVMaze{eps: []tvmaze.Episode{{Season: 9, Number: 4, AirDate: "2026-09-01"}}}
	st := &fakeStore{title: cached}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.lookupCalls != 0 || tv.epsCalls != 1 || tm.tvCalls != 0 || tm.provCalls != 0 {
		t.Fatalf("calls: lookup=%d eps=%d tv=%d prov=%d, want 0/1/0/0", tv.lookupCalls, tv.epsCalls, tm.tvCalls, tm.provCalls)
	}
	if st.airToday != "2026-07-15" || len(st.airings) != 1 {
		t.Fatalf("airings = %+v today=%q", st.airings, st.airToday)
	}
}

func TestAiringsSkippedWhenFreshOrNotAiring(t *testing.T) {
	// Fresh stamp: no TVMaze call.
	tv := &fakeTVMaze{}
	st := &fakeStore{title: airingSeries(int64p(82))}
	if _, err := svc(&fakeTMDB{}, tv, st).EnsureTitle(context.Background(), "series", 1399, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.epsCalls != 0 {
		t.Fatal("fresh airings must not refresh")
	}

	// Ended show with stale stamp: still no TVMaze call.
	ended := airingSeries(int64p(82))
	ended.Airing = false
	ended.AiringsRefreshedAt = now.Add(-48 * time.Hour)
	tv2 := &fakeTVMaze{}
	st2 := &fakeStore{title: ended}
	if _, err := svc(&fakeTMDB{}, tv2, st2).EnsureTitle(context.Background(), "series", 1399, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv2.epsCalls != 0 {
		t.Fatal("ended series must not refresh airings")
	}
}

func TestRefreshUpstreamDownServesCache(t *testing.T) {
	for name, tvErr := range map[string]error{"tmdb 500": errors.New("boom"), "tmdb 404": tmdb.ErrNotFound} {
		cached := airingSeries(int64p(82))
		cached.RefreshedAt = now.Add(-31 * 24 * time.Hour)
		tm := &fakeTMDB{tvErr: tvErr}
		st := &fakeStore{title: cached}
		ti, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "series", 1399, "US")
		if err != nil || ti == nil || ti.Name != "GoT" {
			t.Fatalf("%s: cached row must be served unchanged: ti=%+v err=%v", name, ti, err)
		}
		if st.upserts != 0 {
			t.Fatalf("%s: no writes on failed refresh", name)
		}
	}
}

func TestRefreshProvidersDownServesCache(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.ProvidersRefreshedAt = now.Add(-8 * 24 * time.Hour)
	tm := &fakeTMDB{provErr: errors.New("boom")}
	st := &fakeStore{title: cached}
	ti, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil || ti == nil {
		t.Fatalf("cached row must be served: %v", err)
	}
	if st.provCalls != 0 {
		t.Fatal("ReplaceProviders must not run when the fetch failed")
	}
}

func int64p(v int64) *int64 { return &v }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/ingest/`
Expected: FAIL — compile errors (`undefined: Service`, `undefined: ErrBadKind`).

- [ ] **Step 3: Implement `ingest.go`**

Create `api/internal/ingest/ingest.go`:

```go
// Package ingest owns title ingestion and TTL refresh: it pulls metadata,
// watch providers, and future air dates from TMDB/TVMaze into the store,
// and serves cached rows when upstreams fail (stale beats 500).
package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
	"github.com/shottah/lineup/api/internal/tvmaze"
)

// Per-class TTLs (v1 design spec).
const (
	metadataTTL  = 30 * 24 * time.Hour
	providersTTL = 7 * 24 * time.Hour
	airingsTTL   = 24 * time.Hour
)

// ErrBadKind rejects kinds other than movie|series before any work.
var ErrBadKind = errors.New("ingest: bad kind")

// ErrTitleNotFound means TMDB has no such title on first ingest.
var ErrTitleNotFound = errors.New("ingest: title not found")

// MetadataClient is the slice of *tmdb.Client ingestion needs.
type MetadataClient interface {
	MovieDetails(ctx context.Context, id int64) (tmdb.Movie, error)
	TVDetails(ctx context.Context, id int64) (tmdb.TV, error)
	WatchProviders(ctx context.Context, kind string, id int64, region string) ([]tmdb.Provider, error)
}

// AiringsClient is the slice of *tvmaze.Client ingestion needs.
type AiringsClient interface {
	LookupByIMDB(ctx context.Context, imdbID string) (tvmaze.Show, error)
	Episodes(ctx context.Context, showID int) ([]tvmaze.Episode, error)
}

// TitleStore is the slice of *store.Store ingestion needs.
type TitleStore interface {
	GetTitleByTMDB(ctx context.Context, kind string, tmdbID int64) (*store.Title, error)
	UpsertTitle(ctx context.Context, u store.TitleUpsert) (*store.Title, error)
	ReplaceSeasons(ctx context.Context, titleID int64, seasons []store.SeasonRow) error
	ReplaceProviders(ctx context.Context, titleID int64, region string, provs []store.ProviderRow) error
	ReplaceFutureAirings(ctx context.Context, titleID int64, today string, airs []store.AiringRow) error
}

// Service orchestrates EnsureTitle. Now is injectable for TTL tests; nil
// defaults to time.Now.
type Service struct {
	TMDB   MetadataClient
	TVMaze AiringsClient
	Store  TitleStore
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) stale(stamp time.Time, ttl time.Duration) bool {
	return s.now().Sub(stamp) > ttl
}

// EnsureTitle guarantees a fresh-enough titles row for (kind, tmdbID) and
// returns it. Missing rows are ingested; existing rows get independent
// per-class TTL refreshes (metadata 30d, providers 7d, airings 1d). Any
// upstream failure during a refresh — including a TMDB 404 — serves the
// cached row unchanged. Only a fresh ingest can fail on upstream errors,
// because there is nothing cached to fall back to.
func (s *Service) EnsureTitle(ctx context.Context, kind string, tmdbID int64, region string) (*store.Title, error) {
	if kind != "movie" && kind != "series" {
		return nil, ErrBadKind
	}
	title, err := s.Store.GetTitleByTMDB(ctx, kind, tmdbID)
	if err != nil {
		return nil, err
	}
	fresh := title == nil

	// Metadata class (mandatory on fresh ingest, best-effort on refresh).
	var imdbID string
	if fresh || s.stale(title.RefreshedAt, metadataTTL) {
		up, imdb, seasons, ferr := s.fetchMetadata(ctx, kind, tmdbID)
		switch {
		case ferr == nil:
			imdbID = imdb
			t2, uerr := s.Store.UpsertTitle(ctx, up)
			if uerr != nil {
				return nil, uerr
			}
			if kind == "series" {
				if serr := s.Store.ReplaceSeasons(ctx, t2.ID, seasons); serr != nil {
					return nil, serr
				}
			}
			title = t2
		case fresh && errors.Is(ferr, tmdb.ErrNotFound):
			return nil, ErrTitleNotFound
		case fresh:
			return nil, fmt.Errorf("ingest: metadata: %w", ferr)
			// Refresh failures fall through: cached metadata stays; the
			// stale stamp retries on the next hit.
		}
	}

	// Providers class. Fetch failures skip the write so the stale stamp
	// retries next hit; store failures are real errors (the DB is ours).
	if s.stale(title.ProvidersRefreshedAt, providersTTL) {
		if provs, perr := s.TMDB.WatchProviders(ctx, kind, tmdbID, region); perr == nil {
			rows := make([]store.ProviderRow, 0, len(provs))
			for _, p := range provs {
				rows = append(rows, store.ProviderRow{ID: p.ID, Name: p.Name, LogoPath: p.LogoPath})
			}
			if err := s.Store.ReplaceProviders(ctx, title.ID, region, rows); err != nil {
				return nil, err
			}
		}
	}

	return s.ensureAirings(ctx, title, imdbID), nil
}

// fetchMetadata pulls TMDB details, returning the upsert payload, the
// IMDB id (series only, "" when TMDB has none), and season rows (series
// only).
func (s *Service) fetchMetadata(ctx context.Context, kind string, tmdbID int64) (store.TitleUpsert, string, []store.SeasonRow, error) {
	if kind == "movie" {
		m, err := s.TMDB.MovieDetails(ctx, tmdbID)
		if err != nil {
			return store.TitleUpsert{}, "", nil, err
		}
		return store.TitleUpsert{
			TMDBID: m.TMDBID, Kind: "movie", Name: m.Name, Overview: m.Overview,
			PosterPath: m.PosterPath, RuntimeMinutes: m.RuntimeMinutes,
		}, "", nil, nil
	}
	tv, err := s.TMDB.TVDetails(ctx, tmdbID)
	if err != nil {
		return store.TitleUpsert{}, "", nil, err
	}
	seasons := make([]store.SeasonRow, 0, len(tv.Seasons))
	for _, se := range tv.Seasons {
		seasons = append(seasons, store.SeasonRow{Number: se.Number, EpisodeCount: se.EpisodeCount})
	}
	return store.TitleUpsert{
		TMDBID: tv.TMDBID, Kind: "series", Name: tv.Name, Overview: tv.Overview,
		PosterPath: tv.PosterPath, RuntimeMinutes: tv.RuntimeMinutes, Airing: tv.Airing,
	}, tv.IMDBID, seasons, nil
}

// ensureAirings runs the TVMaze pipeline for airing series: resolve the
// TVMaze id once (needs an IMDB id from this request's metadata fetch),
// then refresh future air dates on the 1d TTL. Every failure path returns
// the title unchanged — airings are enrichment, never worth failing the
// request over; failed classes keep their stale stamp and retry next hit.
func (s *Service) ensureAirings(ctx context.Context, title *store.Title, imdbID string) *store.Title {
	if title.Kind != "series" || !title.Airing {
		return title
	}
	if title.TVMazeID == nil {
		if imdbID == "" {
			return title
		}
		show, err := s.TVMaze.LookupByIMDB(ctx, imdbID)
		if err != nil {
			return title // ErrNotFound is a normal outcome (no TVMaze entry)
		}
		id64 := int64(show.ID)
		t2, uerr := s.Store.UpsertTitle(ctx, store.TitleUpsert{
			TMDBID: title.TMDBID, Kind: title.Kind, TVMazeID: &id64,
			Name: title.Name, Overview: title.Overview, PosterPath: title.PosterPath,
			RuntimeMinutes: title.RuntimeMinutes, Airing: title.Airing,
		})
		if uerr != nil {
			return title
		}
		title = t2
	}
	if !s.stale(title.AiringsRefreshedAt, airingsTTL) {
		return title
	}
	eps, err := s.TVMaze.Episodes(ctx, int(*title.TVMazeID))
	if err != nil {
		return title
	}
	today := s.now().UTC().Format("2006-01-02")
	rows := []store.AiringRow{}
	for _, e := range eps {
		if e.AirDate == "" || e.AirDate < today {
			continue
		}
		rows = append(rows, store.AiringRow{Season: e.Season, Episode: e.Number, AirDate: e.AirDate})
	}
	if err := s.Store.ReplaceFutureAirings(ctx, title.ID, today, rows); err != nil {
		return title
	}
	return title
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./internal/ingest/ && go vet ./internal/ingest/ && go test ./internal/ingest/ -v`
Expected: gofmt prints nothing; all 14 tests PASS.

Note: `TestMetadataRefreshAfterTTL` exercises the counted-upsert path where the lookup gate (`tvmaze_id` already stored) prevents a second upsert — if you see `upserts=2`, `ensureAirings` is re-upserting despite a stored id; the gate is wrong.

- [ ] **Step 5: Commit**

```bash
git add api/internal/ingest/
git commit -m "feat(api): ingest.EnsureTitle with per-class TTL refresh and stale-cache fallback"
```

---

### Task 3: HTTP layer — search + title handlers, wiring

**Files:**
- Create: `api/internal/httpserver/titles.go`
- Modify: `api/internal/httpserver/server.go` (Deps struct + v1 routes)
- Modify: `api/cmd/api/main.go` (wire real clients when the token is set)
- Test: `api/internal/httpserver/titles_test.go`

**Interfaces:**
- Consumes: `ingest.Service`/`ErrBadKind`/`ErrTitleNotFound` (Task 2), `store.Title`/`TitleFull` (Task 1), `tmdb.SearchResult{TMDBID int64; Kind, Name, Overview, PosterPath, Year string}`, `writeJSONError` (auth.go:33), `userFrom` (auth.go:28), test helpers `fakeVerifierWithTok1()` (me_test.go:15) and `newFakeUsers()` (auth_test.go:23 — its users get Region "US").
- Produces: `GET /v1/search` and `GET /v1/titles/{kind}/{tmdbID}` mounted under the authed v1 group; `Deps.Search SearchClient`, `Deps.Ingest Ingester`, `Deps.Titles TitleReader`.

- [ ] **Step 1: Write the failing tests**

Create `api/internal/httpserver/titles_test.go`:

```go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shottah/lineup/api/internal/ingest"
	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
)

type fakeSearch struct {
	results []tmdb.SearchResult
	err     error
	gotQ    string
}

func (f *fakeSearch) SearchMulti(_ context.Context, q string) ([]tmdb.SearchResult, error) {
	f.gotQ = q
	return f.results, f.err
}

type fakeIngester struct {
	title              *store.Title
	err                error
	gotKind, gotRegion string
	gotTMDBID          int64
}

func (f *fakeIngester) EnsureTitle(_ context.Context, kind string, tmdbID int64, region string) (*store.Title, error) {
	f.gotKind, f.gotTMDBID, f.gotRegion = kind, tmdbID, region
	return f.title, f.err
}

type fakeTitleReader struct {
	full      *store.TitleFull
	err       error
	gotUserID int64
	gotTitle  int64
	gotRegion string
}

func (f *fakeTitleReader) GetTitleFull(_ context.Context, userID, titleID int64, region string) (*store.TitleFull, error) {
	f.gotUserID, f.gotTitle, f.gotRegion = userID, titleID, region
	return f.full, f.err
}

// titlesServer mounts the full authed router with search/ingest fakes.
func titlesServer(t *testing.T, fs *fakeSearch, fi *fakeIngester, fr *fakeTitleReader) http.Handler {
	t.Helper()
	return New(Deps{
		Users:    newFakeUsers(),
		Verifier: fakeVerifierWithTok1(),
		Search:   fs,
		Ingest:   fi,
		Titles:   fr,
	}).Handler
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSearchHappyPath(t *testing.T) {
	fs := &fakeSearch{results: []tmdb.SearchResult{
		{TMDBID: 603, Kind: "movie", Name: "The Matrix", Overview: "o", PosterPath: "/m.jpg", Year: "1999"},
		{TMDBID: 1399, Kind: "series", Name: "GoT", Year: "2011"},
	}}
	rec := get(t, titlesServer(t, fs, &fakeIngester{}, &fakeTitleReader{}), "/v1/search?q=the+matrix")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if fs.gotQ != "the matrix" {
		t.Fatalf("query = %q", fs.gotQ)
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Results) != 2 {
		t.Fatalf("results = %+v", body.Results)
	}
	first := body.Results[0]
	if first["tmdb_id"] != float64(603) || first["kind"] != "movie" || first["name"] != "The Matrix" ||
		first["poster_path"] != "/m.jpg" || first["year"] != "1999" {
		t.Fatalf("first result = %+v (keys must be snake_case)", first)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	h := titlesServer(t, &fakeSearch{}, &fakeIngester{}, &fakeTitleReader{})
	for _, path := range []string{"/v1/search", "/v1/search?q=", "/v1/search?q=%20%20"} {
		if rec := get(t, h, path); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d, want 422", path, rec.Code)
		}
	}
}

func TestSearchUpstreamDown(t *testing.T) {
	fs := &fakeSearch{err: errors.New("boom")}
	rec := get(t, titlesServer(t, fs, &fakeIngester{}, &fakeTitleReader{}), "/v1/search?q=x")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestGetTitleHappyPath(t *testing.T) {
	title := &store.Title{ID: 7, TMDBID: 1399, Kind: "series", Name: "GoT"}
	fi := &fakeIngester{title: title}
	fr := &fakeTitleReader{full: &store.TitleFull{
		Title:     *title,
		Seasons:   []store.SeasonRow{{Number: 1, EpisodeCount: 10}},
		Providers: []store.ProviderRow{{ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}},
	}}
	rec := get(t, titlesServer(t, &fakeSearch{}, fi, fr), "/v1/titles/series/1399")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if fi.gotKind != "series" || fi.gotTMDBID != 1399 || fi.gotRegion != "US" {
		t.Fatalf("ingester got %s/%d/%s, want series/1399/US", fi.gotKind, fi.gotTMDBID, fi.gotRegion)
	}
	if fr.gotTitle != 7 || fr.gotRegion != "US" || fr.gotUserID == 0 {
		t.Fatalf("reader got user=%d title=%d region=%s", fr.gotUserID, fr.gotTitle, fr.gotRegion)
	}
	var body struct {
		Title struct {
			TMDBID int64  `json:"tmdb_id"`
			Name   string `json:"name"`
		} `json:"title"`
		Seasons   []store.SeasonRow   `json:"seasons"`
		Providers []store.ProviderRow `json:"providers"`
		Entry     *store.Entry        `json:"entry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Title.TMDBID != 1399 || body.Title.Name != "GoT" || len(body.Seasons) != 1 ||
		len(body.Providers) != 1 || body.Entry != nil {
		t.Fatalf("payload = %+v", body)
	}
}

func TestGetTitleBadRoute(t *testing.T) {
	h := titlesServer(t, &fakeSearch{}, &fakeIngester{}, &fakeTitleReader{})
	for _, path := range []string{"/v1/titles/tv/1399", "/v1/titles/series/abc", "/v1/titles/series/0"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestGetTitleErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ingest.ErrTitleNotFound, http.StatusNotFound},
		{errors.New("tmdb down"), http.StatusBadGateway},
	}
	for _, c := range cases {
		fi := &fakeIngester{err: c.err}
		rec := get(t, titlesServer(t, &fakeSearch{}, fi, &fakeTitleReader{}), "/v1/titles/movie/603")
		if rec.Code != c.want {
			t.Fatalf("err %v: status = %d, want %d", c.err, rec.Code, c.want)
		}
	}
}

func TestSearchRequiresAuth(t *testing.T) {
	h := titlesServer(t, &fakeSearch{}, &fakeIngester{}, &fakeTitleReader{})
	req := httptest.NewRequest(http.MethodGet, "/v1/search?q=x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd api && go test ./internal/httpserver/ -run 'TestSearch|TestGetTitle'`
Expected: FAIL — compile errors (`unknown field Search in struct literal of type Deps`).

- [ ] **Step 3: Implement handlers and wiring**

Create `api/internal/httpserver/titles.go`:

```go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/ingest"
	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
)

// SearchClient is the slice of *tmdb.Client the search proxy needs.
type SearchClient interface {
	SearchMulti(ctx context.Context, query string) ([]tmdb.SearchResult, error)
}

// Ingester is the slice of *ingest.Service the title handler needs.
type Ingester interface {
	EnsureTitle(ctx context.Context, kind string, tmdbID int64, region string) (*store.Title, error)
}

// TitleReader is the slice of *store.Store the title handler needs.
type TitleReader interface {
	GetTitleFull(ctx context.Context, userID, titleID int64, region string) (*store.TitleFull, error)
}

// searchResult is the /v1/search wire shape (snake_case like every other
// endpoint).
type searchResult struct {
	TMDBID     int64  `json:"tmdb_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	PosterPath string `json:"poster_path"`
	Year       string `json:"year"`
}

// handleSearch is a thin TMDB proxy: no DB reads or writes.
func handleSearch(search SearchClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "q required")
			return
		}
		hits, err := search.SearchMulti(r.Context(), q)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream")
			return
		}
		out := make([]searchResult, 0, len(hits))
		for _, h := range hits {
			out = append(out, searchResult{TMDBID: h.TMDBID, Kind: h.Kind, Name: h.Name,
				Overview: h.Overview, PosterPath: h.PosterPath, Year: h.Year})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]searchResult{"results": out})
	}
}

// handleGetTitle ingests-if-absent then returns the title-page payload.
func handleGetTitle(ing Ingester, titles TitleReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := chi.URLParam(r, "kind")
		if kind != "movie" && kind != "series" {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		tmdbID, err := strconv.ParseInt(chi.URLParam(r, "tmdbID"), 10, 64)
		if err != nil || tmdbID < 1 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		user := userFrom(r.Context())
		title, err := ing.EnsureTitle(r.Context(), kind, tmdbID, user.Region)
		switch {
		case errors.Is(err, ingest.ErrTitleNotFound), errors.Is(err, ingest.ErrBadKind):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			// Fresh-ingest upstream failures land here; DB errors do too,
			// which 502 slightly mislabels — acceptable for v1, where the
			// dominant failure mode on this path is TMDB.
			writeJSONError(w, http.StatusBadGateway, "upstream")
			return
		}
		full, err := titles.GetTitleFull(r.Context(), user.ID, title.ID, user.Region)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(full)
	}
}
```

In `api/internal/httpserver/server.go`, replace the `Deps` struct:

```go
type Deps struct {
	Store    *store.Store
	Users    UserStore
	Verifier fbauth.TokenVerifier
	Entries  EntryStore
	Guides   GuideStore
	Search   SearchClient
	Ingest   Ingester
	Titles   TitleReader
	Now      func() time.Time
}
```

and add inside the `r.Route("/v1", …)` group, after the `d.Entries` block:

```go
			if d.Search != nil {
				v1.Get("/search", handleSearch(d.Search))
			}
			if d.Ingest != nil && d.Titles != nil {
				v1.Get("/titles/{kind}/{tmdbID}", handleGetTitle(d.Ingest, d.Titles))
			}
```

In `api/cmd/api/main.go`, replace the `srv := …` line with:

```go
	deps := httpserver.Deps{Store: st, Users: st, Verifier: verifier, Entries: st, Guides: st}
	// Search/ingestion need a TMDB read token; without one the API boots
	// with those routes absent (404s) rather than half-working.
	if cfg.TMDBReadToken != "" {
		tm := tmdb.New(cfg.TMDBReadToken)
		deps.Search = tm
		deps.Ingest = &ingest.Service{TMDB: tm, TVMaze: tvmaze.New(), Store: st}
		deps.Titles = st
	}
	srv := httpserver.New(deps)
```

and add to main.go's imports: `"github.com/shottah/lineup/api/internal/ingest"`, `"github.com/shottah/lineup/api/internal/tmdb"`, `"github.com/shottah/lineup/api/internal/tvmaze"`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l ./... && go vet ./... && go test ./...`
Expected: gofmt prints nothing; every package PASSes (store tests skip without TEST_DATABASE_URL). Then `go build ./...` — clean.

- [ ] **Step 5: Commit**

```bash
git add api/internal/httpserver/titles.go api/internal/httpserver/titles_test.go api/internal/httpserver/server.go api/cmd/api/main.go
git commit -m "feat(api): search proxy and title ingestion endpoints"
```

---

### Task 4: Full-suite sweep + PR

**Files:** none new.

- [ ] **Step 1: Full verification**

Run: `docker start lineup-pg 2>/dev/null; cd api && gofmt -l ./... && go vet ./... && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./... && go test ./...`
Expected: gofmt silent; both test runs green (with and without live PG).

- [ ] **Step 2: Push and open the PR** (writing-github-content style; no infra steps — local-only)

```bash
git push -u origin feat/11-search-ingestion
gh pr create --title "feat(api): search and title ingestion endpoints" --body "..."  # body: closes #11, summary, test notes, session link
```

Squash-merge is the user's call after CI.

---

## Execution notes

- Task order strict: 1 → 2 → 3 → 4 (each consumes the previous task's exports).
- Tasks 1's GREEN step needs the local `lineup-pg` docker container; if docker is unavailable, STOP and report BLOCKED rather than accepting skipped-only runs.
- The final whole-branch review happens before Step 2 of Task 4 (controller dispatches it per subagent-driven-development).
