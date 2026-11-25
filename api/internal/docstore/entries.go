package docstore

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
)

// Pointer is a user's position in a series.
type Pointer struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

// Entry is a user's relationship to a title, joined with the title fields
// shelf views render.
type Entry struct {
	TitleID        string     `json:"title_id"`
	TMDBID         int64      `json:"tmdb_id"`
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
// ClearRating true removes the rating (wins over Rating).
type EntryUpdate struct {
	Status      *string
	Rating      *float64
	ClearRating bool
	Favorite    *bool
	Pointer     *Pointer
}

// entryDoc mirrors users/{uid}/entries/{titleId}. Rating and watchedAt are
// absent (not null) when unset.
type entryDoc struct {
	TitleID        string     `firestore:"titleId"`
	Status         string     `firestore:"status"`
	Rating         *float64   `firestore:"rating,omitempty"`
	Favorite       bool       `firestore:"favorite"`
	PointerSeason  int        `firestore:"pointerSeason"`
	PointerEpisode int        `firestore:"pointerEpisode"`
	AddedAt        time.Time  `firestore:"addedAt"`
	WatchedAt      *time.Time `firestore:"watchedAt,omitempty"`
}

func (d *entryDoc) entry(t *titleDoc) *Entry {
	return &Entry{
		TitleID: d.TitleID, TMDBID: t.TMDBID, Kind: t.Kind, Name: t.Name,
		PosterPath: t.PosterPath, RuntimeMinutes: t.RuntimeMinutes, Airing: t.Airing,
		Status: d.Status, Rating: d.Rating, Favorite: d.Favorite,
		Pointer: Pointer{Season: d.PointerSeason, Episode: d.PointerEpisode},
		AddedAt: d.AddedAt, WatchedAt: d.WatchedAt,
	}
}

func (s *Store) entries(userID string) *firestore.CollectionRef {
	return s.c.Collection("users").Doc(userID).Collection("entries")
}

// UpsertEntry creates or partially updates the caller's entry for a title in
// one transaction, returning the entry joined with title data. Setting
// status to watched stamps watchedAt; other statuses preserve it.
// ErrTitleNotFound when the title is missing or its id is invalid.
func (s *Store) UpsertEntry(ctx context.Context, userID, titleID string, u EntryUpdate) (*Entry, error) {
	if !validID(titleID) || !validID(userID) {
		return nil, ErrTitleNotFound
	}
	tref := s.c.Collection("titles").Doc(titleID)
	eref := s.entries(userID).Doc(titleID)
	var out *Entry
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		tsnap, err := tx.Get(tref)
		if isNotFound(err) {
			return ErrTitleNotFound
		}
		if err != nil {
			return err
		}
		var td titleDoc
		if err := tsnap.DataTo(&td); err != nil {
			return err
		}
		esnap, err := tx.Get(eref)
		if err != nil && !isNotFound(err) {
			return err
		}
		now := time.Now().UTC()
		var d entryDoc
		if err == nil {
			if err := esnap.DataTo(&d); err != nil {
				return err
			}
		} else {
			d = entryDoc{Status: "none", PointerSeason: 1, PointerEpisode: 1, AddedAt: now}
		}
		d.TitleID = titleID
		if u.Status != nil {
			d.Status = *u.Status
			if *u.Status == "watched" {
				d.WatchedAt = &now
			}
		}
		if u.ClearRating {
			d.Rating = nil
		} else if u.Rating != nil {
			r := *u.Rating
			d.Rating = &r
		}
		if u.Favorite != nil {
			d.Favorite = *u.Favorite
		}
		if u.Pointer != nil {
			d.PointerSeason, d.PointerEpisode = u.Pointer.Season, u.Pointer.Episode
		}
		if err := tx.Set(eref, &d); err != nil {
			return err
		}
		out = d.entry(&td)
		return nil
	})
	if err == ErrTitleNotFound {
		return nil, ErrTitleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: upsert entry: %w", err)
	}
	return out, nil
}

// CountRotation counts the user's rotation entries, excluding excludeTitleID
// so re-setting an already-rotating title stays idempotent.
func (s *Store) CountRotation(ctx context.Context, userID, excludeTitleID string) (int, error) {
	if !validID(userID) {
		return 0, fmt.Errorf("docstore: count rotation: invalid user id %q", userID)
	}
	rq := s.entries(userID).Where("status", "==", "rotation")
	res, err := rq.NewAggregationQuery().WithCount("n").Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("docstore: count rotation: %w", err)
	}
	v, ok := res["n"].(interface{ GetIntegerValue() int64 })
	if !ok {
		return 0, fmt.Errorf("docstore: count rotation: unexpected result %T", res["n"])
	}
	n := int(v.GetIntegerValue())
	if validID(excludeTitleID) {
		snap, err := s.entries(userID).Doc(excludeTitleID).Get(ctx)
		if err != nil && !isNotFound(err) {
			return 0, fmt.Errorf("docstore: count rotation: %w", err)
		}
		if err == nil {
			if st, _ := snap.DataAt("status"); st == "rotation" {
				n--
			}
		}
	}
	return n, nil
}

// Shelf lists a user's entries for one shelf view, joined with title data.
// Entries whose title doc is missing are skipped. shelf must be
// pre-validated by the caller; unknown values error.
func (s *Store) Shelf(ctx context.Context, userID, shelf string) ([]Entry, error) {
	if !validID(userID) {
		return nil, fmt.Errorf("docstore: shelf: invalid user id %q", userID)
	}
	col := s.entries(userID)
	var q firestore.Query
	switch shelf {
	case "watchlist", "rotation", "watched":
		q = col.Where("status", "==", shelf).OrderBy("addedAt", firestore.Desc)
	case "favorites":
		q = col.Where("favorite", "==", true).OrderBy("addedAt", firestore.Desc)
	case "ratings":
		q = col.Where("rating", ">", 0).OrderBy("rating", firestore.Desc).OrderBy("addedAt", firestore.Desc)
	default:
		return nil, fmt.Errorf("docstore: unknown shelf %q", shelf)
	}
	snaps, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("docstore: shelf %s: %w", shelf, err)
	}
	docs := make([]entryDoc, len(snaps))
	ids := make([]string, len(snaps))
	for i, snap := range snaps {
		if err := snap.DataTo(&docs[i]); err != nil {
			return nil, fmt.Errorf("docstore: shelf %s: %w", shelf, err)
		}
		docs[i].TitleID = snap.Ref.ID
		ids[i] = snap.Ref.ID
	}
	titles, err := s.getTitles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("docstore: shelf %s: %w", shelf, err)
	}
	out := []Entry{}
	for i := range docs {
		if t := titles[docs[i].TitleID]; t != nil {
			out = append(out, *docs[i].entry(t))
		}
	}
	return out, nil
}

// seasonRows converts a title's seasons map to rows sorted by number.
func seasonRows(m map[string]int) []SeasonRow {
	out := make([]SeasonRow, 0, len(m))
	for k, n := range m {
		num, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		out = append(out, SeasonRow{Number: num, EpisodeCount: n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out
}
