package docstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"cloud.google.com/go/firestore"
)

// GuideItemUpdate carries PATCH semantics; nil = unchanged. DurationMin
// resizes the item (endMin = startMin + duration); when nil the current
// duration is preserved across moves. SetEdited ORs into edited.
type GuideItemUpdate struct {
	Date        *string
	StartMin    *int
	DurationMin *int
	TitleID     *string
	Season      *int
	Episode     *int
	Pinned      *bool
	SetEdited   bool
	// Today (YYYY-MM-DD, UTC) gates move enforcement; empty disables it.
	// Only consulted when the update is a move (Date or StartMin set).
	Today string
	// SwapProviders is the new title's region-filtered provider ids
	// (SwapInfo.Providers), set only on a title swap. UpdateGuideItem keeps
	// the item's current provider when it's among these, otherwise falls
	// back to the lowest id; nil/empty leaves providerId unchanged.
	SwapProviders []int64
}

// SwapInfo describes a swap-eligible title (rotation or watchlist).
type SwapInfo struct {
	TitleID        string
	Kind           string
	Runtime        int
	PointerSeason  int
	PointerEpisode int
	// Providers is the swap target's region-filtered provider ids, sorted
	// ascending; empty (not nil) when the title has none in this region, in
	// which case callers leave the provider alone.
	Providers []int64
}

// UpdateGuideItem applies move/swap/pin semantics to one item in a single
// transaction on the guide doc. Moves preserve duration unless DurationMin
// overrides it. When Today is set and the update is a move, moving an item
// dated before Today is ErrPastMove and moving onto a date before Today is
// ErrPastTarget. On a title swap with SwapProviders set, providerId is kept
// when the new title also streams on it, else set to the lowest of
// SwapProviders. A title swap also drops every other alternate
// (isPlan=false) on the item's resulting date offering the swapped-in title.
// ErrGuideNotFound when the guide or item is missing.
func (s *Store) UpdateGuideItem(ctx context.Context, userID, guideID, itemID string, upd GuideItemUpdate) (*GuideItem, error) {
	if !validID(userID) || !validID(guideID) || !validID(itemID) {
		return nil, ErrGuideNotFound
	}
	ref := s.guides(userID).Doc(guideID)
	var out *GuideItem
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		d, err := txGuide(tx, ref)
		if err != nil {
			return err
		}
		idx := slices.IndexFunc(d.Items, func(it guideItemDoc) bool { return it.ID == itemID })
		if idx < 0 {
			return ErrGuideNotFound
		}
		it := d.Items[idx]

		if (upd.Date != nil || upd.StartMin != nil) && upd.Today != "" {
			if it.Date < upd.Today {
				return ErrPastMove
			}
			if upd.Date != nil && *upd.Date < upd.Today {
				return ErrPastTarget
			}
		}

		duration := it.EndMin - it.StartMin
		if upd.DurationMin != nil {
			duration = *upd.DurationMin
		}
		if upd.Date != nil {
			it.Date = *upd.Date
		}
		if upd.StartMin != nil {
			it.StartMin = *upd.StartMin
		}
		it.EndMin = it.StartMin + duration
		if upd.TitleID != nil {
			it.TitleID = *upd.TitleID
		}
		if upd.Season != nil {
			it.Season = *upd.Season
		}
		if upd.Episode != nil {
			it.Episode = *upd.Episode
		}
		if upd.Pinned != nil {
			it.Pinned = *upd.Pinned
		}
		it.Edited = it.Edited || upd.SetEdited
		if len(upd.SwapProviders) > 0 && !slices.Contains(upd.SwapProviders, it.ProviderID) {
			it.ProviderID = slices.Min(upd.SwapProviders)
		}
		d.Items[idx] = it

		if upd.TitleID != nil {
			// Date-wide: a title swapped into the plan stops being offered
			// as that day's alternate anywhere, not just in the swapped slot.
			d.Items = slices.DeleteFunc(d.Items, func(o guideItemDoc) bool {
				return o.ID != itemID && o.Date == it.Date && !o.IsPlan && o.TitleID == *upd.TitleID
			})
		}

		if err := tx.Update(ref, []firestore.Update{{Path: "items", Value: d.Items}}); err != nil {
			return err
		}
		gi := GuideItem(it)
		out = &gi
		return nil
	})
	if errors.Is(err, ErrGuideNotFound) || errors.Is(err, ErrPastMove) || errors.Is(err, ErrPastTarget) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: update guide item: %w", err)
	}
	return out, nil
}

// DeleteGuideItem removes one item from the guide. ErrGuideNotFound when the
// guide or item is missing.
func (s *Store) DeleteGuideItem(ctx context.Context, userID, guideID, itemID string) error {
	if !validID(userID) || !validID(guideID) || !validID(itemID) {
		return ErrGuideNotFound
	}
	ref := s.guides(userID).Doc(guideID)
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		d, err := txGuide(tx, ref)
		if err != nil {
			return err
		}
		n := len(d.Items)
		d.Items = slices.DeleteFunc(d.Items, func(it guideItemDoc) bool { return it.ID == itemID })
		if len(d.Items) == n {
			return ErrGuideNotFound
		}
		return tx.Update(ref, []firestore.Update{{Path: "items", Value: d.Items}})
	})
	if errors.Is(err, ErrGuideNotFound) {
		return err
	}
	if err != nil {
		return fmt.Errorf("docstore: delete guide item: %w", err)
	}
	return nil
}

// txGuide reads a guide doc inside tx; ErrGuideNotFound when absent.
func txGuide(tx *firestore.Transaction, ref *firestore.DocumentRef) (*guideDoc, error) {
	snap, err := tx.Get(ref)
	if isNotFound(err) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, err
	}
	var d guideDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// SwapTitle validates a swap target (the caller's entry is on rotation or
// watchlist) and returns what the handler needs to rewrite the item,
// including the target's region providers sorted ascending. ErrTitleNotFound
// when the entry or title is missing or the entry is on another shelf.
func (s *Store) SwapTitle(ctx context.Context, userID, titleID, region string) (*SwapInfo, error) {
	if !validID(userID) || !validID(titleID) {
		return nil, ErrTitleNotFound
	}
	snaps, err := s.c.GetAll(ctx, []*firestore.DocumentRef{
		s.entries(userID).Doc(titleID),
		s.c.Collection("titles").Doc(titleID),
	})
	if err != nil {
		return nil, fmt.Errorf("docstore: swap title: %w", err)
	}
	esnap, tsnap := snaps[0], snaps[1]
	if !esnap.Exists() || !tsnap.Exists() {
		return nil, ErrTitleNotFound
	}
	var e entryDoc
	if err := esnap.DataTo(&e); err != nil {
		return nil, fmt.Errorf("docstore: swap title: %w", err)
	}
	if e.Status != "rotation" && e.Status != "watchlist" {
		return nil, ErrTitleNotFound
	}
	var td titleDoc
	if err := tsnap.DataTo(&td); err != nil {
		return nil, fmt.Errorf("docstore: swap title: %w", err)
	}
	info := &SwapInfo{
		TitleID: titleID, Kind: td.Kind,
		// Same fallback the generation path applies, so a title with no
		// runtime swaps in at a sane duration instead of collapsing the slot.
		Runtime:       defaultRuntime(td.Kind, td.RuntimeMinutes),
		PointerSeason: e.PointerSeason, PointerEpisode: e.PointerEpisode,
		Providers: append([]int64{}, td.Providers[region]...),
	}
	sort.Slice(info.Providers, func(i, j int) bool { return info.Providers[i] < info.Providers[j] })
	return info, nil
}
