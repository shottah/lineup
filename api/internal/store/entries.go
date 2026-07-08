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
