package docstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/shottah/lineup/api/internal/guide"
)

// TMDB omits runtimes for many current shows; a zero-duration item would
// let the scheduler stack everything at the window start. Assume a
// typical episode/feature length instead.
const (
	defaultSeriesRuntimeMin = 45
	defaultMovieRuntimeMin  = 120
)

// defaultRuntime applies the fallback runtime for a title whose stored
// runtimeMinutes is 0 (TMDB omits episode_run_time for many current shows).
// Both the generation path (GuideInputTitles) and the swap path (SwapTitle)
// route through this so a swapped-in back-catalog title gets the same
// duration a generated one would, instead of collapsing to a zero-length
// slot (endMin == startMin).
func defaultRuntime(kind string, raw int) int {
	if raw != 0 {
		return raw
	}
	if kind == "movie" {
		return defaultMovieRuntimeMin
	}
	return defaultSeriesRuntimeMin
}

// Guide is one of a user's guides plus its items in guide order.
type Guide struct {
	ID        string      `json:"id"`
	StartDate string      `json:"start_date"`
	EndDate   string      `json:"end_date"`
	Seed      int64       `json:"seed"`
	Items     []GuideItem `json:"items"`
}

// GuideItem is one scheduled slot in a guide.
type GuideItem struct {
	ID         string `json:"id"`
	Date       string `json:"date"`
	StartMin   int    `json:"start_min"`
	EndMin     int    `json:"end_min"`
	TitleID    string `json:"title_id"`
	Season     int    `json:"season"`
	Episode    int    `json:"episode"`
	ProviderID int64  `json:"provider_id"`
	IsPlan     bool   `json:"is_plan"`
	Pinned     bool   `json:"pinned"`
	Edited     bool   `json:"edited"`
	Watched    bool   `json:"watched"`
}

// TitleLookup is the guide sidecar's per-title rendering data (#18).
type TitleLookup struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	TMDBID     int64  `json:"tmdb_id"`
	PosterPath string `json:"poster_path"`
}

// guideDoc mirrors users/{uid}/guides/{guideId}.
type guideDoc struct {
	StartDate string         `firestore:"startDate"`
	EndDate   string         `firestore:"endDate"`
	Seed      int64          `firestore:"seed"`
	CreatedAt time.Time      `firestore:"createdAt"`
	Items     []guideItemDoc `firestore:"items"`
}

type guideItemDoc struct {
	ID         string `firestore:"id"`
	Date       string `firestore:"date"`
	StartMin   int    `firestore:"startMin"`
	EndMin     int    `firestore:"endMin"`
	TitleID    string `firestore:"titleId"`
	Season     int    `firestore:"season"`
	Episode    int    `firestore:"episode"`
	ProviderID int64  `firestore:"providerId"`
	IsPlan     bool   `firestore:"isPlan"`
	Pinned     bool   `firestore:"pinned"`
	Edited     bool   `firestore:"edited"`
	Watched    bool   `firestore:"watched"`
}

func (d *guideDoc) guide(id string) *Guide {
	g := &Guide{ID: id, StartDate: d.StartDate, EndDate: d.EndDate, Seed: d.Seed,
		Items: make([]GuideItem, len(d.Items))}
	for i, it := range d.Items {
		g.Items[i] = GuideItem(it)
	}
	sortItems(g.Items)
	return g
}

// genItemID mints guide item IDs; tests swap it to force collisions.
var genItemID = newItemID

// appendNewItems appends items to d with fresh IDs, regenerating any ID
// already used within the guide.
func appendNewItems(d *guideDoc, items []guide.Item) {
	used := make(map[string]bool, len(d.Items)+len(items))
	for _, it := range d.Items {
		used[it.ID] = true
	}
	for _, it := range items {
		id := genItemID()
		for used[id] {
			id = genItemID()
		}
		used[id] = true
		d.Items = append(d.Items, guideItemDoc{
			ID: id, Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin, TitleID: it.TitleID,
			Season: it.Season, Episode: it.Episode, ProviderID: it.Provider,
			IsPlan: it.IsPlan, Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched,
		})
	}
}

// sortItems orders items by date, startMin, isPlan desc, titleID (the old
// SQL order); ties keep their stored order.
func sortItems(items []GuideItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Date != b.Date {
			return a.Date < b.Date
		}
		if a.StartMin != b.StartMin {
			return a.StartMin < b.StartMin
		}
		if a.IsPlan != b.IsPlan {
			return a.IsPlan
		}
		return a.TitleID < b.TitleID
	})
}

func (s *Store) guides(userID string) *firestore.CollectionRef {
	return s.c.Collection("users").Doc(userID).Collection("guides")
}

// GuideInputTitles hydrates the user's rotation into engine titles with
// region-filtered providers, season counts, and airings, sorted by title
// ID. Entries whose title doc is missing are skipped.
func (s *Store) GuideInputTitles(ctx context.Context, userID, region string) ([]guide.Title, error) {
	if !validID(userID) {
		return nil, fmt.Errorf("docstore: guide input titles: invalid user id %q", userID)
	}
	snaps, err := s.entries(userID).Where("status", "==", "rotation").Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("docstore: guide input titles: %w", err)
	}
	ents := make(map[string]entryDoc, len(snaps))
	ids := make([]string, 0, len(snaps))
	for _, snap := range snaps {
		var d entryDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, fmt.Errorf("docstore: guide input titles: %w", err)
		}
		ents[snap.Ref.ID] = d
		ids = append(ids, snap.Ref.ID)
	}
	tdocs, err := s.getTitles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("docstore: guide input titles: %w", err)
	}
	sort.Strings(ids)
	titles := []guide.Title{}
	for _, id := range ids {
		td := tdocs[id]
		if td == nil {
			continue
		}
		e := ents[id]
		t := guide.Title{
			ID: id, Kind: td.Kind, Name: td.Name, Runtime: defaultRuntime(td.Kind, td.RuntimeMinutes),
			Airing:         td.Airing,
			Pointer:        guide.Pointer{Season: e.PointerSeason, Episode: e.PointerEpisode},
			SeasonEpisodes: map[int]int{},
		}
		if p := td.Providers[region]; len(p) > 0 {
			t.Providers = append([]int64(nil), p...)
			sort.Slice(t.Providers, func(i, j int) bool { return t.Providers[i] < t.Providers[j] })
		}
		for k, n := range td.Seasons {
			if num, err := strconv.Atoi(k); err == nil {
				t.SeasonEpisodes[num] = n
			}
		}
		for _, a := range td.Airings {
			t.AirDates = append(t.AirDates, guide.AiredEpisode{Season: a.Season, Episode: a.Episode, Date: a.AirDate})
		}
		sort.Slice(t.AirDates, func(i, j int) bool {
			if t.AirDates[i].Season != t.AirDates[j].Season {
				return t.AirDates[i].Season < t.AirDates[j].Season
			}
			return t.AirDates[i].Episode < t.AirDates[j].Episode
		})
		titles = append(titles, t)
	}
	return titles, nil
}

// CreateGuideReplacingOverlaps atomically replaces any of the user's guides
// overlapping [startDate, endDate] (inclusive) with a fresh one.
func (s *Store) CreateGuideReplacingOverlaps(ctx context.Context, userID, startDate, endDate string, seed int64, items []guide.Item) (*Guide, error) {
	if !validID(userID) {
		return nil, fmt.Errorf("docstore: create guide: invalid user id %q", userID)
	}
	col := s.guides(userID)
	var out *Guide
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snaps, err := tx.Documents(col.Where("endDate", ">=", startDate)).GetAll()
		if err != nil {
			return err
		}
		for _, snap := range snaps {
			st, err := snap.DataAt("startDate")
			if err != nil {
				return err
			}
			if sd, _ := st.(string); sd <= endDate {
				if err := tx.Delete(snap.Ref); err != nil {
					return err
				}
			}
		}
		ref := col.NewDoc()
		d := guideDoc{StartDate: startDate, EndDate: endDate, Seed: seed,
			CreatedAt: time.Now().UTC(), Items: []guideItemDoc{}}
		appendNewItems(&d, items)
		if err := tx.Create(ref, &d); err != nil {
			return err
		}
		out = d.guide(ref.ID)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("docstore: create guide: %w", err)
	}
	return out, nil
}

// GuideWithItems loads one of the user's guides with items in guide order.
// Another user's guide is simply absent under this user's path.
func (s *Store) GuideWithItems(ctx context.Context, userID, guideID string) (*Guide, error) {
	if !validID(userID) || !validID(guideID) {
		return nil, ErrGuideNotFound
	}
	snap, err := s.guides(userID).Doc(guideID).Get(ctx)
	if isNotFound(err) {
		return nil, ErrGuideNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: guide: %w", err)
	}
	var d guideDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, fmt.Errorf("docstore: guide: %w", err)
	}
	return d.guide(guideID), nil
}

// CurrentGuide returns the newest guide (createdAt, then guide ID) whose
// endDate is on or after today.
func (s *Store) CurrentGuide(ctx context.Context, userID, today string) (*Guide, error) {
	if !validID(userID) {
		return nil, ErrGuideNotFound
	}
	snaps, err := s.guides(userID).Where("endDate", ">=", today).Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("docstore: current guide: %w", err)
	}
	var best *guideDoc
	var bestID string
	for _, snap := range snaps {
		var d guideDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, fmt.Errorf("docstore: current guide: %w", err)
		}
		id := snap.Ref.ID
		if best == nil || d.CreatedAt.After(best.CreatedAt) ||
			(d.CreatedAt.Equal(best.CreatedAt) && id > bestID) {
			best, bestID = &d, id
		}
	}
	if best == nil {
		return nil, ErrGuideNotFound
	}
	return best.guide(bestID), nil
}

// ReplaceUnkeptItems drops the guide's items not named in keepIDs (unknown
// IDs are ignored), appends newItems with fresh IDs, and persists seed as
// the guide's new seed, atomically. seed is whatever actually produced
// newItems, so a later regenerate chains from that rather than replaying the
// guide's original seed.
func (s *Store) ReplaceUnkeptItems(ctx context.Context, userID, guideID string, keepIDs []string, newItems []guide.Item, seed int64) (*Guide, error) {
	if !validID(userID) || !validID(guideID) {
		return nil, ErrGuideNotFound
	}
	keep := make(map[string]bool, len(keepIDs))
	for _, id := range keepIDs {
		keep[id] = true
	}
	ref := s.guides(userID).Doc(guideID)
	var out *Guide
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if isNotFound(err) {
			return ErrGuideNotFound
		}
		if err != nil {
			return err
		}
		var d guideDoc
		if err := snap.DataTo(&d); err != nil {
			return err
		}
		kept := []guideItemDoc{}
		for _, it := range d.Items {
			if keep[it.ID] {
				kept = append(kept, it)
			}
		}
		d.Items = kept
		d.Seed = seed
		appendNewItems(&d, newItems)
		if err := tx.Set(ref, &d); err != nil {
			return err
		}
		out = d.guide(guideID)
		return nil
	})
	if errors.Is(err, ErrGuideNotFound) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: replace items: %w", err)
	}
	return out, nil
}

// GuideLookups returns rendering dictionaries for already-loaded guide
// items: every distinct title and provider referenced, keyed by ID. Missing
// titles and providers are absent; the maps are never nil.
func (s *Store) GuideLookups(ctx context.Context, items []GuideItem) (map[string]TitleLookup, map[int64]ProviderRow, error) {
	titles := map[string]TitleLookup{}
	provs := map[int64]ProviderRow{}
	tids := make([]string, 0, len(items))
	var prefs []*firestore.DocumentRef
	seenP := map[int64]bool{}
	for _, it := range items {
		tids = append(tids, it.TitleID)
		if !seenP[it.ProviderID] {
			seenP[it.ProviderID] = true
			prefs = append(prefs, s.c.Collection("providers").Doc(strconv.FormatInt(it.ProviderID, 10)))
		}
	}
	tdocs, err := s.getTitles(ctx, tids)
	if err != nil {
		return nil, nil, fmt.Errorf("docstore: guide lookups: %w", err)
	}
	for id, td := range tdocs {
		titles[id] = TitleLookup{Name: td.Name, Kind: td.Kind, TMDBID: td.TMDBID, PosterPath: td.PosterPath}
	}
	if len(prefs) == 0 {
		return titles, provs, nil
	}
	snaps, err := s.c.GetAll(ctx, prefs)
	if err != nil {
		return nil, nil, fmt.Errorf("docstore: guide lookups: providers: %w", err)
	}
	for _, snap := range snaps {
		if !snap.Exists() {
			continue
		}
		id, err := strconv.ParseInt(snap.Ref.ID, 10, 64)
		if err != nil {
			continue
		}
		var p ProviderRow
		if err := snap.DataTo(&p); err != nil {
			return nil, nil, fmt.Errorf("docstore: guide lookups: providers: %w", err)
		}
		p.ID = id
		provs[id] = p
	}
	return titles, provs, nil
}
