package docstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
)

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

// watchCtx is everything a mark/unmark reads, loaded before any write.
type watchCtx struct {
	gref, eref *firestore.DocumentRef
	guide      *guideDoc
	idx        int
	title      *titleDoc
	entry      *entryDoc // nil when the user has no entry for the title
}

func (w *watchCtx) item() *guideItemDoc { return &w.guide.Items[w.idx] }

// seasonCounts converts the title's seasons map for nextPointer.
func (w *watchCtx) seasonCounts() map[int]int {
	counts := map[int]int{}
	for _, r := range seasonRows(w.title.Seasons) {
		counts[r.Number] = r.EpisodeCount
	}
	return counts
}

// txWatchRead reads the guide, the item's title, and the caller's entry for
// it inside tx. ErrGuideNotFound when the guide, the item, or the title is
// missing (the SQL joined titles, so a dangling title was "not found").
func (s *Store) txWatchRead(tx *firestore.Transaction, userID, guideID, itemID string) (*watchCtx, error) {
	w := &watchCtx{gref: s.guides(userID).Doc(guideID)}
	d, err := txGuide(tx, w.gref)
	if err != nil {
		return nil, err
	}
	w.guide = d
	w.idx = slices.IndexFunc(d.Items, func(it guideItemDoc) bool { return it.ID == itemID })
	if w.idx < 0 {
		return nil, ErrGuideNotFound
	}
	titleID := d.Items[w.idx].TitleID
	if !validID(titleID) {
		return nil, ErrGuideNotFound
	}
	tsnap, err := tx.Get(s.c.Collection("titles").Doc(titleID))
	if isNotFound(err) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, err
	}
	w.title = &titleDoc{}
	if err := tsnap.DataTo(w.title); err != nil {
		return nil, err
	}
	w.eref = s.entries(userID).Doc(titleID)
	esnap, err := tx.Get(w.eref)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	if err == nil {
		w.entry = &entryDoc{}
		if err := esnap.DataTo(w.entry); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// MarkItemWatched flags the item watched and advances the series pointer
// (season rollover via the title's seasons map; past-finale auto-completes
// the title to the watched shelf). Movies auto-complete immediately. The
// pointer never regresses when old episodes are re-watched. With no entry
// the item still flips and no entry is created. One transaction: guide,
// title and entry are read first, then the guide and entry are written.
// ErrGuideNotFound when the guide, item, or the item's title is missing.
func (s *Store) MarkItemWatched(ctx context.Context, userID, guideID, itemID string) (*GuideItem, error) {
	if !validID(userID) || !validID(guideID) || !validID(itemID) {
		return nil, ErrGuideNotFound
	}
	var out *GuideItem
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		w, err := s.txWatchRead(tx, userID, guideID, itemID)
		if err != nil {
			return err
		}
		it := w.item()
		it.Watched = true
		if err := tx.Update(w.gref, []firestore.Update{{Path: "items", Value: w.guide.Items}}); err != nil {
			return err
		}

		if e := w.entry; e != nil {
			now := time.Now().UTC()
			var ups []firestore.Update
			complete := func() {
				ups = append(ups,
					firestore.Update{Path: "status", Value: "watched"},
					firestore.Update{Path: "watchedAt", Value: now})
			}
			if w.title.Kind == "movie" {
				complete()
			} else if it.Season > e.PointerSeason || (it.Season == e.PointerSeason && it.Episode >= e.PointerEpisode) {
				next, pastFinale := nextPointer(it.Season, it.Episode, w.seasonCounts())
				ups = append(ups,
					firestore.Update{Path: "pointerSeason", Value: next.Season},
					firestore.Update{Path: "pointerEpisode", Value: next.Episode})
				if pastFinale {
					complete()
				}
			}
			if len(ups) > 0 {
				if err := tx.Update(w.eref, ups); err != nil {
					return err
				}
			}
		}
		gi := GuideItem(*it)
		out = &gi
		return nil
	})
	if errors.Is(err, ErrGuideNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: watch item: %w", err)
	}
	return out, nil
}

// UnmarkItemWatched reverses MarkItemWatched conditionally: it mirrors the
// pointer/status writes MarkItemWatched made, but only where nothing has
// happened since that would make undoing them wrong. The item's watched
// flag always clears; unwatching an already-unwatched item writes nothing
// (idempotent). The pointer and status rollbacks are independent of each
// other: for a series, the entry pointer rolls back to (item.season,
// item.episode) only if it's still sitting exactly where this item's mark
// left it (nextPointer(item.season, item.episode); a later mark that
// advanced the pointer further, or a season-count change since the mark,
// leaves it untouched); status reverts from watched to rotation (deleting
// watchedAt) whenever status is still watched, regardless of the pointer
// check, since the title may have been completed by a different item than
// the one being unmarked. For a movie (pointer irrelevant), status reverts
// under that same status-only rule. With no entry the item still flips and
// no entry is created. ErrGuideNotFound when the guide, item, or the item's
// title is missing.
func (s *Store) UnmarkItemWatched(ctx context.Context, userID, guideID, itemID string) (*GuideItem, error) {
	if !validID(userID) || !validID(guideID) || !validID(itemID) {
		return nil, ErrGuideNotFound
	}
	var out *GuideItem
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		w, err := s.txWatchRead(tx, userID, guideID, itemID)
		if err != nil {
			return err
		}
		it := w.item()
		if it.Watched {
			it.Watched = false
			if err := tx.Update(w.gref, []firestore.Update{{Path: "items", Value: w.guide.Items}}); err != nil {
				return err
			}
			if e := w.entry; e != nil {
				var ups []firestore.Update
				if w.title.Kind != "movie" {
					next, _ := nextPointer(it.Season, it.Episode, w.seasonCounts())
					if next.Season == e.PointerSeason && next.Episode == e.PointerEpisode {
						ups = append(ups,
							firestore.Update{Path: "pointerSeason", Value: it.Season},
							firestore.Update{Path: "pointerEpisode", Value: it.Episode})
					}
				}
				if e.Status == "watched" {
					ups = append(ups,
						firestore.Update{Path: "status", Value: "rotation"},
						firestore.Update{Path: "watchedAt", Value: firestore.Delete})
				}
				if len(ups) > 0 {
					if err := tx.Update(w.eref, ups); err != nil {
						return err
					}
				}
			}
		}
		gi := GuideItem(*it)
		out = &gi
		return nil
	})
	if errors.Is(err, ErrGuideNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: unwatch item: %w", err)
	}
	return out, nil
}
