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
}

// SwapInfo describes a swap-eligible title (rotation or watchlist).
type SwapInfo struct {
	TitleID        int64
	Kind           string
	Runtime        int
	PointerSeason  int
	PointerEpisode int
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

// ReplaceUnkeptItems deletes the guide's items not named in keepIDs and
// inserts newItems, atomically.
func (s *Store) ReplaceUnkeptItems(ctx context.Context, userID, guideID int64, keepIDs []int64, newItems []guide.Item) (*Guide, error) {
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
// unless DurationMin overrides it.
func (s *Store) UpdateGuideItem(ctx context.Context, userID, guideID, itemID int64, upd GuideItemUpdate) (*GuideItem, error) {
	q := `
UPDATE guide_items gi SET
  date      = COALESCE($4::date, gi.date),
  start_min = COALESCE($5, gi.start_min),
  end_min   = COALESCE($5, gi.start_min) + COALESCE($6, gi.end_min - gi.start_min),
  title_id  = COALESCE($7, gi.title_id),
  season    = COALESCE($8, gi.season),
  episode   = COALESCE($9, gi.episode),
  pinned    = COALESCE($10, gi.pinned),
  edited    = gi.edited OR $11
FROM guides g
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.id = gi.guide_id AND g.user_id = $1
RETURNING ` + guideItemCols
	it, err := scanGuideItem(s.Pool.QueryRow(ctx, q, userID, guideID, itemID,
		upd.Date, upd.StartMin, upd.DurationMin, upd.TitleID, upd.Season, upd.Episode, upd.Pinned, upd.SetEdited))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: update guide item: %w", err)
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

// SwapTitle validates a swap target (rotation or watchlist) and returns
// what the handler needs to rewrite the item.
func (s *Store) SwapTitle(ctx context.Context, userID, titleID int64) (*SwapInfo, error) {
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
	return info, nil
}
