package docstore

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"

	"github.com/shottah/lineup/api/internal/guide"
)

// Title mirrors one titles/{kind}-{tmdbId} document. The refresh stamps
// drive ingest's TTL decisions and are internal; they never appear in API
// JSON.
type Title struct {
	ID                   string    `json:"id"`
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
	// ProviderOffers is region -> offer type of that region's providers
	// ("stream" | "rent" | "buy", "" when the region has none). A region
	// missing from the map was never fetched with offer data (docs ingested
	// before offers existed held flatrate only); ingest treats it as stale.
	ProviderOffers map[string]string `json:"-"`
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
	ID       int64  `json:"id" firestore:"-"`
	Name     string `json:"name" firestore:"name"`
	LogoPath string `json:"logo_path" firestore:"logoPath"`
}

// AiringRow is one episode air date ("YYYY-MM-DD").
type AiringRow struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	AirDate string `json:"air_date"`
}

type airing struct {
	Season  int    `firestore:"season"`
	Episode int    `firestore:"episode"`
	AirDate string `firestore:"airDate"`
}

type titleDoc struct {
	Kind                 string             `firestore:"kind"`
	TMDBID               int64              `firestore:"tmdbId"`
	TVMazeID             *int64             `firestore:"tvmazeId"`
	Name                 string             `firestore:"name"`
	Overview             string             `firestore:"overview"`
	PosterPath           string             `firestore:"posterPath"`
	RuntimeMinutes       int                `firestore:"runtimeMinutes"`
	Airing               bool               `firestore:"airing"`
	RefreshedAt          time.Time          `firestore:"refreshedAt"`
	ProvidersRefreshedAt time.Time          `firestore:"providersRefreshedAt"`
	AiringsRefreshedAt   time.Time          `firestore:"airingsRefreshedAt"`
	Seasons              map[string]int     `firestore:"seasons"`
	Airings              []airing           `firestore:"airings"`
	Providers            map[string][]int64 `firestore:"providers"`
	// ProviderOffers is region -> offer type for Providers[region]; see
	// Title.ProviderOffers.
	ProviderOffers map[string]string `firestore:"providerOffers"`
}

// offer returns the region's offer type. Docs written before offers were
// recorded held TMDB flatrate data only, so a missing entry with providers
// present reads as "stream".
func (d *titleDoc) offer(region string) string {
	if o, ok := d.ProviderOffers[region]; ok {
		return o
	}
	if len(d.Providers[region]) > 0 {
		return guide.OfferStream
	}
	return ""
}

func (d *titleDoc) title(id string) *Title {
	return &Title{
		ID: id, TMDBID: d.TMDBID, Kind: d.Kind, TVMazeID: d.TVMazeID, Name: d.Name,
		Overview: d.Overview, PosterPath: d.PosterPath, RuntimeMinutes: d.RuntimeMinutes,
		Airing: d.Airing, RefreshedAt: d.RefreshedAt,
		ProvidersRefreshedAt: d.ProvidersRefreshedAt, AiringsRefreshedAt: d.AiringsRefreshedAt,
		ProviderOffers: maps.Clone(d.ProviderOffers),
	}
}

// TitleID returns the deterministic document ID for a title.
func TitleID(kind string, tmdbID int64) string {
	return kind + "-" + strconv.FormatInt(tmdbID, 10)
}

// epoch is the initial class stamp: immediately stale.
var epoch = time.Unix(0, 0).UTC()

// UpsertTitle inserts or refreshes the metadata class of a title, stamping
// refreshedAt. tvmazeId is kept when the refresh omits it. Provider/airing
// stamps and data belong to their Replace methods and are preserved.
func (s *Store) UpsertTitle(ctx context.Context, u TitleUpsert) (*Title, error) {
	id := TitleID(u.Kind, u.TMDBID)
	if !validID(id) {
		return nil, fmt.Errorf("docstore: upsert title: invalid id %q", id)
	}
	ref := s.c.Collection("titles").Doc(id)
	var out *Title
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if err != nil && !isNotFound(err) {
			return err
		}
		var d titleDoc
		if err == nil {
			if err := snap.DataTo(&d); err != nil {
				return err
			}
		} else {
			d = titleDoc{ProvidersRefreshedAt: epoch, AiringsRefreshedAt: epoch}
		}
		if d.Seasons == nil {
			d.Seasons = map[string]int{}
		}
		if d.Airings == nil {
			d.Airings = []airing{}
		}
		if d.Providers == nil {
			d.Providers = map[string][]int64{}
		}
		d.Kind, d.TMDBID = u.Kind, u.TMDBID
		if u.TVMazeID != nil {
			d.TVMazeID = u.TVMazeID
		}
		d.Name, d.Overview, d.PosterPath = u.Name, u.Overview, u.PosterPath
		d.RuntimeMinutes, d.Airing = u.RuntimeMinutes, u.Airing
		d.RefreshedAt = now()
		if err := tx.Set(ref, &d); err != nil {
			return err
		}
		out = d.title(id)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("docstore: upsert title: %w", err)
	}
	return out, nil
}

// GetTitleByTMDB returns the title for (kind, tmdbID), or (nil, nil) when
// absent; ingest-if-absent branches on nil rather than an error.
func (s *Store) GetTitleByTMDB(ctx context.Context, kind string, tmdbID int64) (*Title, error) {
	id := TitleID(kind, tmdbID)
	if !validID(id) {
		return nil, nil
	}
	snap, err := s.c.Collection("titles").Doc(id).Get(ctx)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("docstore: get title by tmdb: %w", err)
	}
	var d titleDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, fmt.Errorf("docstore: get title by tmdb: %w", err)
	}
	return d.title(id), nil
}

// getTitles fetches title docs with one GetAll. Missing and invalid IDs are
// simply absent from the result.
func (s *Store) getTitles(ctx context.Context, ids []string) (map[string]*titleDoc, error) {
	out := map[string]*titleDoc{}
	var refs []*firestore.DocumentRef
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id) || seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, s.c.Collection("titles").Doc(id))
	}
	if len(refs) == 0 {
		return out, nil
	}
	snaps, err := s.c.GetAll(ctx, refs)
	if err != nil {
		return nil, fmt.Errorf("docstore: get titles: %w", err)
	}
	for _, snap := range snaps {
		if !snap.Exists() {
			continue
		}
		var d titleDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, fmt.Errorf("docstore: get titles: %w", err)
		}
		out[snap.Ref.ID] = &d
	}
	return out, nil
}

// updateTitle runs fn on an existing title doc inside a transaction; fn
// returns the field updates (and may queue extra writes). A missing or
// invalid title is ErrTitleNotFound (wrapped); it is never created.
func (s *Store) updateTitle(ctx context.Context, op, titleID string,
	fn func(tx *firestore.Transaction, d *titleDoc) ([]firestore.Update, error)) error {
	if !validID(titleID) {
		return fmt.Errorf("docstore: %s: %w", op, ErrTitleNotFound)
	}
	ref := s.c.Collection("titles").Doc(titleID)
	err := s.c.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snap, err := tx.Get(ref)
		if isNotFound(err) {
			return ErrTitleNotFound
		}
		if err != nil {
			return err
		}
		var d titleDoc
		if err := snap.DataTo(&d); err != nil {
			return err
		}
		ups, err := fn(tx, &d)
		if err != nil {
			return err
		}
		return tx.Update(ref, ups)
	})
	if err != nil {
		return fmt.Errorf("docstore: %s: %w", op, err)
	}
	return nil
}

// ReplaceSeasons replaces a title's seasons map. Duplicate numbers: last wins.
func (s *Store) ReplaceSeasons(ctx context.Context, titleID string, seasons []SeasonRow) error {
	m := make(map[string]int, len(seasons))
	for _, se := range seasons {
		m[strconv.Itoa(se.Number)] = se.EpisodeCount
	}
	return s.updateTitle(ctx, "replace seasons", titleID,
		func(_ *firestore.Transaction, _ *titleDoc) ([]firestore.Update, error) {
			return []firestore.Update{{Path: "seasons", Value: m}}, nil
		})
}

// ReplaceProviders upserts the provider dictionary and replaces the title's
// availability for one region (IDs sorted ascending) along with its offer
// type ("stream" | "rent" | "buy", "" for none), stamping
// providersRefreshedAt. Other regions are untouched; the dictionary is
// append-only.
func (s *Store) ReplaceProviders(ctx context.Context, titleID, region, offer string, provs []ProviderRow) error {
	dict := map[int64]ProviderRow{}
	for _, p := range provs {
		dict[p.ID] = p
	}
	ids := make([]int64, 0, len(dict))
	for id := range dict {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return s.updateTitle(ctx, "replace providers", titleID,
		func(tx *firestore.Transaction, d *titleDoc) ([]firestore.Update, error) {
			// Reads are done; queue the dictionary writes.
			for _, id := range ids {
				p := dict[id]
				ref := s.c.Collection("providers").Doc(strconv.FormatInt(id, 10))
				if err := tx.Set(ref, &p); err != nil {
					return nil, err
				}
			}
			all := d.Providers
			if all == nil {
				all = map[string][]int64{}
			}
			all[region] = ids
			offers := d.ProviderOffers
			if offers == nil {
				offers = map[string]string{}
			}
			offers[region] = offer
			return []firestore.Update{
				{Path: "providers", Value: all},
				{Path: "providerOffers", Value: offers},
				{Path: "providersRefreshedAt", Value: now()},
			}, nil
		})
}

// ReplaceFutureAirings drops airings on/after today and adds the given
// (caller-pre-filtered) set, stamping airingsRefreshedAt. Past airings are
// kept; a rescheduled episode replaces its old entry.
func (s *Store) ReplaceFutureAirings(ctx context.Context, titleID, today string, airs []AiringRow) error {
	return s.updateTitle(ctx, "replace airings", titleID,
		func(_ *firestore.Transaction, d *titleDoc) ([]firestore.Update, error) {
			type key struct{ s, e int }
			byEp := map[key]airing{}
			for _, a := range d.Airings {
				if a.AirDate < today {
					byEp[key{a.Season, a.Episode}] = a
				}
			}
			for _, a := range airs {
				byEp[key{a.Season, a.Episode}] = airing{a.Season, a.Episode, a.AirDate}
			}
			out := make([]airing, 0, len(byEp))
			for _, a := range byEp {
				out = append(out, a)
			}
			sort.Slice(out, func(i, j int) bool {
				if out[i].Season != out[j].Season {
					return out[i].Season < out[j].Season
				}
				return out[i].Episode < out[j].Episode
			})
			return []firestore.Update{
				{Path: "airings", Value: out},
				{Path: "airingsRefreshedAt", Value: now()},
			}, nil
		})
}

// TitleFull is the title-page payload.
type TitleFull struct {
	Title     Title         `json:"title"`
	Seasons   []SeasonRow   `json:"seasons"`
	Providers []ProviderRow `json:"providers"`
	// Offer is the providers' offer type in the region ("stream" | "rent"
	// | "buy"), "" when there are none.
	Offer string `json:"offer"`
	Entry *Entry `json:"entry"`
}

// GetTitleFull assembles the title-page payload: title, seasons, the
// region's providers (sorted by name, hydrated from providers/{id}), and the
// caller's entry (nil when absent). An unknown or invalid titleID returns
// ErrTitleNotFound.
func (s *Store) GetTitleFull(ctx context.Context, userID, titleID, region string) (*TitleFull, error) {
	if !validID(titleID) {
		return nil, ErrTitleNotFound
	}
	tm, err := s.getTitles(ctx, []string{titleID})
	if err != nil {
		return nil, fmt.Errorf("docstore: title full: %w", err)
	}
	td := tm[titleID]
	if td == nil {
		return nil, ErrTitleNotFound
	}
	full := &TitleFull{Title: *td.title(titleID), Seasons: seasonRows(td.Seasons), Providers: []ProviderRow{},
		Offer: td.offer(region)}

	if pids := td.Providers[region]; len(pids) > 0 {
		refs := make([]*firestore.DocumentRef, len(pids))
		for i, id := range pids {
			refs[i] = s.c.Collection("providers").Doc(strconv.FormatInt(id, 10))
		}
		snaps, err := s.c.GetAll(ctx, refs)
		if err != nil {
			return nil, fmt.Errorf("docstore: title full: providers: %w", err)
		}
		for _, snap := range snaps {
			if !snap.Exists() {
				continue
			}
			var p ProviderRow
			if err := snap.DataTo(&p); err != nil {
				return nil, fmt.Errorf("docstore: title full: providers: %w", err)
			}
			id, err := strconv.ParseInt(snap.Ref.ID, 10, 64)
			if err != nil {
				continue
			}
			p.ID = id
			full.Providers = append(full.Providers, p)
		}
		sort.Slice(full.Providers, func(i, j int) bool {
			if full.Providers[i].Name != full.Providers[j].Name {
				return full.Providers[i].Name < full.Providers[j].Name
			}
			return full.Providers[i].ID < full.Providers[j].ID
		})
	}

	if validID(userID) {
		esnap, err := s.entries(userID).Doc(titleID).Get(ctx)
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("docstore: title full: entry: %w", err)
		}
		if err == nil {
			var d entryDoc
			if err := esnap.DataTo(&d); err != nil {
				return nil, fmt.Errorf("docstore: title full: entry: %w", err)
			}
			d.TitleID = titleID
			full.Entry = d.entry(td)
		}
	}
	return full, nil
}
