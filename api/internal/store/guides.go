package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/shottah/lineup/api/internal/guide"
)

// ErrGuideNotFound is returned for guides/items that don't exist or belong
// to another user (indistinguishable by design).
var ErrGuideNotFound = errors.New("store: guide not found")

// ErrPastMove is returned when the moved item's current date is already in
// the past; ErrPastTarget when the requested target date is in the past.
var ErrPastMove = errors.New("store: cannot move a past slot")
var ErrPastTarget = errors.New("store: cannot move a slot into the past")

// TMDB omits runtimes for many current shows; a zero-duration item would
// let the scheduler stack everything at the window start. Assume a
// typical episode/feature length instead.
const (
	defaultSeriesRuntimeMin = 45
	defaultMovieRuntimeMin  = 120
)

// Guide mirrors a guides row plus its items.
type Guide struct {
	ID        int64       `json:"id"`
	StartDate string      `json:"start_date"`
	EndDate   string      `json:"end_date"`
	Seed      int64       `json:"seed"`
	Items     []GuideItem `json:"items"`
}

// GuideItem mirrors a guide_items row.
type GuideItem struct {
	ID         int64  `json:"id"`
	Date       string `json:"date"`
	StartMin   int    `json:"start_min"`
	EndMin     int    `json:"end_min"`
	TitleID    int64  `json:"title_id"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	ProviderID int64  `json:"provider_id"`
	IsPlan     bool   `json:"is_plan"`
	Pinned     bool   `json:"pinned"`
	Edited     bool   `json:"edited"`
	Watched    bool   `json:"watched"`
}

// GuideItemUpdate carries PATCH semantics; nil = unchanged. DurationMin
// resizes the item (end_min = start_min + duration); when nil the current
// duration is preserved across moves. SetEdited ORs into edited.
type GuideItemUpdate struct {
	Date        *string
	StartMin    *int
	DurationMin *int
	TitleID     *int64
	Season      *int
	Episode     *int
	Pinned      *bool
	SetEdited   bool
	// Today (YYYY-MM-DD, UTC) gates move enforcement; empty disables it.
	// Only consulted when the update is a move (Date or StartMin set).
	Today string
	// SwapProviders is the new title's region-filtered provider ids
	// (SwapInfo.Providers, sorted ascending), set only on a title swap.
	// UpdateGuideItem keeps the item's current provider when it's among
	// these, otherwise falls back to the lowest id; nil/empty leaves
	// provider_id unchanged (see UpdateGuideItem).
	SwapProviders []int64
}

// SwapInfo describes a swap-eligible title (rotation or watchlist).
type SwapInfo struct {
	TitleID        int64
	Kind           string
	Runtime        int
	PointerSeason  int
	PointerEpisode int
	// Providers is the swap target's region-filtered provider ids, sorted
	// ascending; empty (not nil) when the title has none in this region —
	// pre-ingestion reality (#11 populates title_providers), so callers
	// treat empty as "leave the provider alone, best effort".
	Providers []int64
}

// nextPointer returns the pointer following the watched episode, rolling
// seasons via counts; pastFinale reports rolling off the last known season
// (the returned pointer then parks on the watched episode itself).
func nextPointer(itemSeason, itemEpisode int, counts map[int]int) (Pointer, bool) {
	cnt, ok := counts[itemSeason]
	if ok && itemEpisode < cnt {
		return Pointer{Season: itemSeason, Episode: itemEpisode + 1}, false
	}
	if _, next := counts[itemSeason+1]; next {
		return Pointer{Season: itemSeason + 1, Episode: 1}, false
	}
	return Pointer{Season: itemSeason, Episode: itemEpisode}, true
}

const guideItemCols = `gi.id, to_char(gi.date,'YYYY-MM-DD'), gi.start_min, gi.end_min, gi.title_id,
       gi.season, gi.episode, gi.provider_id, gi.is_plan, gi.pinned, gi.edited, gi.watched`

func scanGuideItem(row pgx.Row) (*GuideItem, error) {
	it := &GuideItem{}
	err := row.Scan(&it.ID, &it.Date, &it.StartMin, &it.EndMin, &it.TitleID,
		&it.Season, &it.Episode, &it.ProviderID, &it.IsPlan, &it.Pinned, &it.Edited, &it.Watched)
	if err != nil {
		return nil, err
	}
	return it, nil
}

// GuideInputTitles hydrates the user's rotation into engine titles with
// region-filtered providers, season counts, and airings.
func (s *Store) GuideInputTitles(ctx context.Context, userID int64, region string) ([]guide.Title, error) {
	rows, err := s.Pool.Query(ctx, `
SELECT t.id, t.kind, t.name, t.runtime_minutes, t.airing, ut.pointer_season, ut.pointer_episode
FROM user_titles ut JOIN titles t ON t.id = ut.title_id
WHERE ut.user_id = $1 AND ut.status = 'rotation'
ORDER BY t.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: guide input titles: %w", err)
	}
	defer rows.Close()

	titles := []guide.Title{}
	idx := map[int64]int{}
	var ids []int64
	for rows.Next() {
		var t guide.Title
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.Runtime, &t.Airing, &t.Pointer.Season, &t.Pointer.Episode); err != nil {
			return nil, fmt.Errorf("store: guide input titles: scan: %w", err)
		}
		if t.Runtime == 0 {
			if t.Kind == "movie" {
				t.Runtime = defaultMovieRuntimeMin
			} else {
				t.Runtime = defaultSeriesRuntimeMin
			}
		}
		t.SeasonEpisodes = map[int]int{}
		idx[t.ID] = len(titles)
		ids = append(ids, t.ID)
		titles = append(titles, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input titles: rows: %w", err)
	}
	if len(titles) == 0 {
		return titles, nil
	}

	prows, err := s.Pool.Query(ctx, `
SELECT title_id, provider_id FROM title_providers
WHERE region = $1 AND title_id = ANY($2) ORDER BY title_id, provider_id`, region, ids)
	if err != nil {
		return nil, fmt.Errorf("store: guide input providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var tid, pid int64
		if err := prows.Scan(&tid, &pid); err != nil {
			return nil, fmt.Errorf("store: guide input providers: scan: %w", err)
		}
		i := idx[tid]
		titles[i].Providers = append(titles[i].Providers, pid)
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input providers: rows: %w", err)
	}

	srows, err := s.Pool.Query(ctx, `
SELECT title_id, season_number, episode_count FROM title_seasons WHERE title_id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: guide input seasons: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var tid int64
		var sn, ec int
		if err := srows.Scan(&tid, &sn, &ec); err != nil {
			return nil, fmt.Errorf("store: guide input seasons: scan: %w", err)
		}
		titles[idx[tid]].SeasonEpisodes[sn] = ec
	}
	if err := srows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input seasons: rows: %w", err)
	}

	arows, err := s.Pool.Query(ctx, `
SELECT title_id, season, episode, to_char(air_date,'YYYY-MM-DD')
FROM title_airings WHERE title_id = ANY($1) ORDER BY title_id, season, episode`, ids)
	if err != nil {
		return nil, fmt.Errorf("store: guide input airings: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var tid int64
		var a guide.AiredEpisode
		if err := arows.Scan(&tid, &a.Season, &a.Episode, &a.Date); err != nil {
			return nil, fmt.Errorf("store: guide input airings: scan: %w", err)
		}
		i := idx[tid]
		titles[i].AirDates = append(titles[i].AirDates, a)
	}
	if err := arows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide input airings: rows: %w", err)
	}
	return titles, nil
}

func insertGuideItems(ctx context.Context, tx pgx.Tx, guideID int64, items []guide.Item) error {
	for _, it := range items {
		if _, err := tx.Exec(ctx, `
INSERT INTO guide_items (guide_id, date, start_min, end_min, title_id, season, episode, provider_id, is_plan, pinned, edited, watched)
VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			guideID, it.Date, it.StartMin, it.EndMin, it.TitleID, it.Season, it.Episode, it.Provider,
			it.IsPlan, it.Pinned, it.Edited, it.Watched); err != nil {
			return fmt.Errorf("store: insert guide item: %w", err)
		}
	}
	return nil
}

// CreateGuideReplacingOverlaps atomically replaces any of the user's
// guides overlapping [startDate, endDate] with a fresh one.
func (s *Store) CreateGuideReplacingOverlaps(ctx context.Context, userID int64, startDate, endDate string, seed int64, items []guide.Item) (*Guide, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: create guide: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
DELETE FROM guides WHERE user_id = $1 AND start_date <= $3::date AND end_date >= $2::date`,
		userID, startDate, endDate); err != nil {
		return nil, fmt.Errorf("store: create guide: replace overlaps: %w", err)
	}
	var gid int64
	if err := tx.QueryRow(ctx, `
INSERT INTO guides (user_id, start_date, end_date, seed) VALUES ($1, $2::date, $3::date, $4) RETURNING id`,
		userID, startDate, endDate, seed).Scan(&gid); err != nil {
		return nil, fmt.Errorf("store: create guide: insert: %w", err)
	}
	if err := insertGuideItems(ctx, tx, gid, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: create guide: commit: %w", err)
	}
	return s.GuideWithItems(ctx, userID, gid)
}

// GuideWithItems loads one of the user's guides with items in guide order.
func (s *Store) GuideWithItems(ctx context.Context, userID, guideID int64) (*Guide, error) {
	g := &Guide{}
	err := s.Pool.QueryRow(ctx, `
SELECT id, to_char(start_date,'YYYY-MM-DD'), to_char(end_date,'YYYY-MM-DD'), seed
FROM guides WHERE id = $2 AND user_id = $1`, userID, guideID).
		Scan(&g.ID, &g.StartDate, &g.EndDate, &g.Seed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: guide: %w", err)
	}
	rows, err := s.Pool.Query(ctx, `
SELECT `+guideItemCols+` FROM guide_items gi
WHERE gi.guide_id = $1 ORDER BY gi.date, gi.start_min, gi.is_plan DESC, gi.title_id`, guideID)
	if err != nil {
		return nil, fmt.Errorf("store: guide items: %w", err)
	}
	defer rows.Close()
	g.Items = []GuideItem{}
	for rows.Next() {
		it, serr := scanGuideItem(rows)
		if serr != nil {
			return nil, fmt.Errorf("store: guide items: scan: %w", serr)
		}
		g.Items = append(g.Items, *it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: guide items: rows: %w", err)
	}
	return g, nil
}

// CurrentGuide returns the newest guide still covering today.
func (s *Store) CurrentGuide(ctx context.Context, userID int64, today string) (*Guide, error) {
	var gid int64
	err := s.Pool.QueryRow(ctx, `
SELECT id FROM guides WHERE user_id = $1 AND end_date >= $2::date
ORDER BY created_at DESC, id DESC LIMIT 1`, userID, today).Scan(&gid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: current guide: %w", err)
	}
	return s.GuideWithItems(ctx, userID, gid)
}

// ReplaceUnkeptItems deletes the guide's items not named in keepIDs,
// inserts newItems, and persists seed as the guide's new seed, atomically.
// seed is whatever actually produced newItems (a regenerate mints a fresh
// one), so a later regenerate chains from that rather than replaying the
// guide's original seed.
func (s *Store) ReplaceUnkeptItems(ctx context.Context, userID, guideID int64, keepIDs []int64, newItems []guide.Item, seed int64) (*Guide, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: replace items: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var owned int64
	err = tx.QueryRow(ctx, `SELECT id FROM guides WHERE id = $2 AND user_id = $1 FOR UPDATE`, userID, guideID).Scan(&owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: replace items: ownership: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE guides SET seed = $2 WHERE id = $1`, guideID, seed); err != nil {
		return nil, fmt.Errorf("store: replace items: seed: %w", err)
	}
	if keepIDs == nil {
		keepIDs = []int64{}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM guide_items WHERE guide_id = $1 AND NOT (id = ANY($2))`, guideID, keepIDs); err != nil {
		return nil, fmt.Errorf("store: replace items: delete: %w", err)
	}
	if err := insertGuideItems(ctx, tx, guideID, newItems); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: replace items: commit: %w", err)
	}
	return s.GuideWithItems(ctx, userID, guideID)
}

// UpdateGuideItem applies move/swap/pin semantics; moves preserve duration
// unless DurationMin overrides it. On a title swap (SwapProviders set),
// provider_id is recomputed rather than left stale: kept when the new
// title also streams on it, else set to the new title's lowest provider
// id. Empty/nil SwapProviders (title not yet through provider ingestion,
// #11) leaves provider_id unchanged — best effort until then. A title
// swap (upd.TitleID set) also deletes any alternate (is_plan=false) row
// left shadowed in the same guide+date slot: once the plan item itself
// shows the swapped-in title, an alternate offering that same title is a
// redundant duplicate of what's already the plan.
func (s *Store) UpdateGuideItem(ctx context.Context, userID, guideID, itemID int64, upd GuideItemUpdate) (*GuideItem, error) {
	var providers []int64
	var lowest int64
	if len(upd.SwapProviders) > 0 {
		providers = upd.SwapProviders
		lowest = providers[0]
		for _, p := range providers[1:] {
			if p < lowest {
				lowest = p
			}
		}
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: update guide item: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if (upd.Date != nil || upd.StartMin != nil) && upd.Today != "" {
		var currentDate string
		err := tx.QueryRow(ctx, `
SELECT to_char(gi.date, 'YYYY-MM-DD') FROM guide_items gi
JOIN guides g ON g.id = gi.guide_id
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.user_id = $1
FOR UPDATE OF gi`, userID, guideID, itemID).Scan(&currentDate)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGuideNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("store: update guide item: load date: %w", err)
		}
		if currentDate < upd.Today {
			return nil, ErrPastMove
		}
		if upd.Date != nil && *upd.Date < upd.Today {
			return nil, ErrPastTarget
		}
	}

	q := `
UPDATE guide_items gi SET
  date        = COALESCE($4::date, gi.date),
  start_min   = COALESCE($5, gi.start_min),
  end_min     = COALESCE($5, gi.start_min) + COALESCE($6, gi.end_min - gi.start_min),
  title_id    = COALESCE($7, gi.title_id),
  season      = COALESCE($8, gi.season),
  episode     = COALESCE($9, gi.episode),
  pinned      = COALESCE($10, gi.pinned),
  edited      = gi.edited OR $11,
  provider_id = CASE
    WHEN $12::bigint[] IS NULL OR cardinality($12::bigint[]) = 0 THEN gi.provider_id
    WHEN gi.provider_id = ANY($12::bigint[]) THEN gi.provider_id
    ELSE $13
  END
FROM guides g
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.id = gi.guide_id AND g.user_id = $1
RETURNING ` + guideItemCols
	it, err := scanGuideItem(tx.QueryRow(ctx, q, userID, guideID, itemID,
		upd.Date, upd.StartMin, upd.DurationMin, upd.TitleID, upd.Season, upd.Episode, upd.Pinned, upd.SetEdited,
		providers, lowest))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: update guide item: %w", err)
	}

	if upd.TitleID != nil {
		// Date-wide: a title swapped into the plan stops being offered as
		// that day's alternate anywhere, not just in the swapped slot.
		if _, err := tx.Exec(ctx, `
DELETE FROM guide_items
WHERE guide_id = $1 AND date = $2::date AND is_plan = false AND title_id = $3 AND id != $4`,
			guideID, it.Date, *upd.TitleID, itemID); err != nil {
			return nil, fmt.Errorf("store: update guide item: clear shadowed alternate: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: update guide item: commit: %w", err)
	}
	return it, nil
}

// DeleteGuideItem removes one item, ownership-scoped.
func (s *Store) DeleteGuideItem(ctx context.Context, userID, guideID, itemID int64) error {
	tag, err := s.Pool.Exec(ctx, `
DELETE FROM guide_items gi USING guides g
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.id = gi.guide_id AND g.user_id = $1`, userID, guideID, itemID)
	if err != nil {
		return fmt.Errorf("store: delete guide item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrGuideNotFound
	}
	return nil
}

// MarkItemWatched flags the item watched and advances the series pointer
// (season rollover via title_seasons; past-finale auto-completes the title
// to the watched shelf). Movies auto-complete immediately. The pointer
// never regresses when old episodes are re-watched.
func (s *Store) MarkItemWatched(ctx context.Context, userID, guideID, itemID int64) (*GuideItem, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: watch item: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var titleID int64
	var season, episode int
	var kind string
	err = tx.QueryRow(ctx, `
SELECT gi.title_id, gi.season, gi.episode, t.kind
FROM guide_items gi JOIN guides g ON g.id = gi.guide_id JOIN titles t ON t.id = gi.title_id
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.user_id = $1
FOR UPDATE OF gi`, userID, guideID, itemID).Scan(&titleID, &season, &episode, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: watch item: load: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE guide_items SET watched = true WHERE id = $1`, itemID); err != nil {
		return nil, fmt.Errorf("store: watch item: flag: %w", err)
	}

	if kind == "movie" {
		if _, err := tx.Exec(ctx, `
UPDATE user_titles SET status = 'watched', watched_at = now()
WHERE user_id = $1 AND title_id = $2`, userID, titleID); err != nil {
			return nil, fmt.Errorf("store: watch item: movie complete: %w", err)
		}
	} else {
		var ps, pe int
		err = tx.QueryRow(ctx, `
SELECT pointer_season, pointer_episode FROM user_titles
WHERE user_id = $1 AND title_id = $2 FOR UPDATE`, userID, titleID).Scan(&ps, &pe)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("store: watch item: pointer: %w", err)
		}
		if err == nil && (season > ps || (season == ps && episode >= pe)) {
			counts := map[int]int{}
			crows, cerr := tx.Query(ctx, `SELECT season_number, episode_count FROM title_seasons WHERE title_id = $1`, titleID)
			if cerr != nil {
				return nil, fmt.Errorf("store: watch item: seasons: %w", cerr)
			}
			for crows.Next() {
				var sn, ec int
				if cerr := crows.Scan(&sn, &ec); cerr != nil {
					crows.Close()
					return nil, fmt.Errorf("store: watch item: seasons scan: %w", cerr)
				}
				counts[sn] = ec
			}
			crows.Close()
			if cerr := crows.Err(); cerr != nil {
				return nil, fmt.Errorf("store: watch item: seasons rows: %w", cerr)
			}

			next, pastFinale := nextPointer(season, episode, counts)
			if pastFinale {
				if _, err := tx.Exec(ctx, `
UPDATE user_titles SET status = 'watched', watched_at = now(),
  pointer_season = $3, pointer_episode = $4
WHERE user_id = $1 AND title_id = $2`, userID, titleID, next.Season, next.Episode); err != nil {
					return nil, fmt.Errorf("store: watch item: series complete: %w", err)
				}
			} else {
				if _, err := tx.Exec(ctx, `
UPDATE user_titles SET pointer_season = $3, pointer_episode = $4
WHERE user_id = $1 AND title_id = $2`, userID, titleID, next.Season, next.Episode); err != nil {
					return nil, fmt.Errorf("store: watch item: advance: %w", err)
				}
			}
		}
	}

	it, err := scanGuideItem(tx.QueryRow(ctx, `SELECT `+guideItemCols+` FROM guide_items gi WHERE gi.id = $1`, itemID))
	if err != nil {
		return nil, fmt.Errorf("store: watch item: reload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: watch item: commit: %w", err)
	}
	return it, nil
}

// UnmarkItemWatched reverses MarkItemWatched conditionally: it mirrors the
// pointer/status writes MarkItemWatched made, but only where nothing has
// happened since that would make undoing them wrong. guide_items.watched
// always clears — unwatching an already-unwatched item is a no-op beyond
// that (idempotent). The pointer and status rollbacks are independent of
// each other: for a series, the user_titles pointer rolls back to
// (item.season, item.episode) only if it's still sitting exactly where this
// item's mark left it (nextPointer(item.season, item.episode) — a later
// mark that advanced the pointer further, or a season-count change since
// the mark, leaves it untouched); status reverts from 'watched' to
// 'rotation' (clearing watched_at) whenever status is still 'watched',
// regardless of the pointer check — the title may have been completed by a
// different item than the one being unmarked. For a movie (pointer
// irrelevant), status reverts under that same status-only rule.
func (s *Store) UnmarkItemWatched(ctx context.Context, userID, guideID, itemID int64) (*GuideItem, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: unwatch item: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var titleID int64
	var season, episode int
	var kind string
	var watched bool
	err = tx.QueryRow(ctx, `
SELECT gi.title_id, gi.season, gi.episode, t.kind, gi.watched
FROM guide_items gi JOIN guides g ON g.id = gi.guide_id JOIN titles t ON t.id = gi.title_id
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.user_id = $1
FOR UPDATE OF gi`, userID, guideID, itemID).Scan(&titleID, &season, &episode, &kind, &watched)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: unwatch item: load: %w", err)
	}

	if watched {
		if _, err := tx.Exec(ctx, `UPDATE guide_items SET watched = false WHERE id = $1`, itemID); err != nil {
			return nil, fmt.Errorf("store: unwatch item: flag: %w", err)
		}

		if kind == "movie" {
			if _, err := tx.Exec(ctx, `
UPDATE user_titles SET status = 'rotation', watched_at = NULL
WHERE user_id = $1 AND title_id = $2 AND status = 'watched'`, userID, titleID); err != nil {
				return nil, fmt.Errorf("store: unwatch item: movie revert: %w", err)
			}
		} else {
			var ps, pe int
			err = tx.QueryRow(ctx, `
SELECT pointer_season, pointer_episode FROM user_titles
WHERE user_id = $1 AND title_id = $2 FOR UPDATE`, userID, titleID).Scan(&ps, &pe)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("store: unwatch item: pointer: %w", err)
			}
			if err == nil {
				counts := map[int]int{}
				crows, cerr := tx.Query(ctx, `SELECT season_number, episode_count FROM title_seasons WHERE title_id = $1`, titleID)
				if cerr != nil {
					return nil, fmt.Errorf("store: unwatch item: seasons: %w", cerr)
				}
				for crows.Next() {
					var sn, ec int
					if cerr := crows.Scan(&sn, &ec); cerr != nil {
						crows.Close()
						return nil, fmt.Errorf("store: unwatch item: seasons scan: %w", cerr)
					}
					counts[sn] = ec
				}
				crows.Close()
				if cerr := crows.Err(); cerr != nil {
					return nil, fmt.Errorf("store: unwatch item: seasons rows: %w", cerr)
				}

				// next is what MarkItemWatched would have set the pointer to
				// for this item (parked on the item itself past the finale).
				// The pointer rollback and the status rollback are
				// independent: the pointer only rolls back if it's still
				// exactly where this mark left it (a later mark elsewhere may
				// have pushed it further, or a re-ingest may have changed
				// season counts so it no longer recomputes to the stored
				// value); the status rollback fires whenever status is still
				// 'watched', regardless of the pointer, since the completion
				// it reflects may have come from a different item entirely.
				next, _ := nextPointer(season, episode, counts)
				if next.Season == ps && next.Episode == pe {
					if _, err := tx.Exec(ctx, `
UPDATE user_titles SET pointer_season = $3, pointer_episode = $4
WHERE user_id = $1 AND title_id = $2`, userID, titleID, season, episode); err != nil {
						return nil, fmt.Errorf("store: unwatch item: pointer revert: %w", err)
					}
				}
				if _, err := tx.Exec(ctx, `
UPDATE user_titles SET status = 'rotation', watched_at = NULL
WHERE user_id = $1 AND title_id = $2 AND status = 'watched'`, userID, titleID); err != nil {
					return nil, fmt.Errorf("store: unwatch item: status revert: %w", err)
				}
			}
		}
	}

	it, err := scanGuideItem(tx.QueryRow(ctx, `SELECT `+guideItemCols+` FROM guide_items gi WHERE gi.id = $1`, itemID))
	if err != nil {
		return nil, fmt.Errorf("store: unwatch item: reload: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: unwatch item: commit: %w", err)
	}
	return it, nil
}

// TitleLookup is the guide sidecar's per-title rendering data (#18).
type TitleLookup struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	TMDBID     int64  `json:"tmdb_id"`
	PosterPath string `json:"poster_path"`
}

// GuideLookups returns rendering dictionaries for a guide's items: every
// distinct title and provider referenced, keyed by id. Ownership is the
// caller's concern (handlers resolve the guide by user first).
func (s *Store) GuideLookups(ctx context.Context, guideID int64) (map[int64]TitleLookup, map[int64]ProviderRow, error) {
	titles := map[int64]TitleLookup{}
	rows, err := s.Pool.Query(ctx, `
SELECT DISTINCT t.id, t.name, t.kind, t.tmdb_id, t.poster_path
FROM guide_items gi JOIN titles t ON t.id = gi.title_id
WHERE gi.guide_id = $1`, guideID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: titles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var tl TitleLookup
		if err := rows.Scan(&id, &tl.Name, &tl.Kind, &tl.TMDBID, &tl.PosterPath); err != nil {
			return nil, nil, fmt.Errorf("store: guide lookups: titles scan: %w", err)
		}
		titles[id] = tl
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: titles rows: %w", err)
	}

	provs := map[int64]ProviderRow{}
	prows, err := s.Pool.Query(ctx, `
SELECT DISTINCT p.id, p.name, p.logo_path
FROM guide_items gi JOIN providers p ON p.id = gi.provider_id
WHERE gi.guide_id = $1`, guideID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var p ProviderRow
		if err := prows.Scan(&p.ID, &p.Name, &p.LogoPath); err != nil {
			return nil, nil, fmt.Errorf("store: guide lookups: providers scan: %w", err)
		}
		provs[p.ID] = p
	}
	if err := prows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: guide lookups: providers rows: %w", err)
	}
	return titles, provs, nil
}

// SwapTitle validates a swap target (rotation or watchlist) and returns
// what the handler needs to rewrite the item, including the target's
// region-filtered providers so the caller can recompute provider_id
// instead of leaving the old title's provider stale.
func (s *Store) SwapTitle(ctx context.Context, userID, titleID int64, region string) (*SwapInfo, error) {
	info := &SwapInfo{}
	err := s.Pool.QueryRow(ctx, `
SELECT t.id, t.kind, t.runtime_minutes, ut.pointer_season, ut.pointer_episode
FROM user_titles ut JOIN titles t ON t.id = ut.title_id
WHERE ut.user_id = $1 AND ut.title_id = $2 AND ut.status IN ('rotation','watchlist')`,
		userID, titleID).Scan(&info.TitleID, &info.Kind, &info.Runtime, &info.PointerSeason, &info.PointerEpisode)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTitleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: swap title: %w", err)
	}

	// Region-filtered providers for the swap target, sorted ascending.
	// Empty (not nil) when the title hasn't been through provider
	// ingestion yet (#11) — the caller then leaves provider_id alone.
	info.Providers = []int64{}
	prows, err := s.Pool.Query(ctx, `
SELECT provider_id FROM title_providers WHERE title_id = $1 AND region = $2 ORDER BY provider_id`,
		titleID, region)
	if err != nil {
		return nil, fmt.Errorf("store: swap title providers: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var pid int64
		if err := prows.Scan(&pid); err != nil {
			return nil, fmt.Errorf("store: swap title providers: scan: %w", err)
		}
		info.Providers = append(info.Providers, pid)
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("store: swap title providers: rows: %w", err)
	}
	return info, nil
}
