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
		if _, err := tx.Exec(ctx, `
INSERT INTO title_seasons (title_id, season_number, episode_count) VALUES ($1, $2, $3)
ON CONFLICT (title_id, season_number) DO UPDATE SET episode_count = EXCLUDED.episode_count`,
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
		if _, err := tx.Exec(ctx, `
INSERT INTO title_providers (title_id, region, provider_id) VALUES ($1, $2, $3)
ON CONFLICT (title_id, region, provider_id) DO NOTHING`,
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
