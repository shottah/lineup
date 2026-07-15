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
