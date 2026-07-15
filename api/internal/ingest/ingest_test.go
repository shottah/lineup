package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
	"github.com/shottah/lineup/api/internal/tvmaze"
)

// now is the fixed test clock: 2026-07-15 noon UTC.
var now = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

type fakeTMDB struct {
	movie                          tmdb.Movie
	tv                             tmdb.TV
	provs                          []tmdb.Provider
	movieErr, tvErr, provErr       error
	movieCalls, tvCalls, provCalls int
	provRegion                     string
}

func (f *fakeTMDB) MovieDetails(_ context.Context, _ int64) (tmdb.Movie, error) {
	f.movieCalls++
	return f.movie, f.movieErr
}
func (f *fakeTMDB) TVDetails(_ context.Context, _ int64) (tmdb.TV, error) {
	f.tvCalls++
	return f.tv, f.tvErr
}
func (f *fakeTMDB) WatchProviders(_ context.Context, _ string, _ int64, region string) ([]tmdb.Provider, error) {
	f.provCalls++
	f.provRegion = region
	return f.provs, f.provErr
}

type fakeTVMaze struct {
	show                  tvmaze.Show
	eps                   []tvmaze.Episode
	lookupErr, epsErr     error
	lookupCalls, epsCalls int
}

func (f *fakeTVMaze) LookupByIMDB(_ context.Context, _ string) (tvmaze.Show, error) {
	f.lookupCalls++
	if f.lookupErr != nil {
		return tvmaze.Show{}, f.lookupErr
	}
	return f.show, nil
}
func (f *fakeTVMaze) Episodes(_ context.Context, _ int) ([]tvmaze.Episode, error) {
	f.epsCalls++
	if f.epsErr != nil {
		return nil, f.epsErr
	}
	return f.eps, nil
}

// fakeStore mimics the SQL semantics ingest depends on: COALESCE'd
// tvmaze_id, refreshed_at stamping on upsert, class stamps on replaces.
type fakeStore struct {
	title                                           *store.Title
	seasons                                         []store.SeasonRow
	provsByRegion                                   map[string][]store.ProviderRow
	airings                                         []store.AiringRow
	airToday                                        string
	getErr, upsertErr, seasonsErr, provsErr, airErr error
	upserts, seasonCalls, provCalls, airCalls       int
}

func (f *fakeStore) GetTitleByTMDB(_ context.Context, _ string, _ int64) (*store.Title, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.title == nil {
		return nil, nil
	}
	cp := *f.title
	return &cp, nil
}

func (f *fakeStore) UpsertTitle(_ context.Context, u store.TitleUpsert) (*store.Title, error) {
	f.upserts++
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if f.title == nil {
		f.title = &store.Title{ID: 1}
	}
	f.title.TMDBID, f.title.Kind = u.TMDBID, u.Kind
	f.title.Name, f.title.Overview, f.title.PosterPath = u.Name, u.Overview, u.PosterPath
	f.title.RuntimeMinutes, f.title.Airing = u.RuntimeMinutes, u.Airing
	if u.TVMazeID != nil {
		f.title.TVMazeID = u.TVMazeID
	}
	f.title.RefreshedAt = now
	cp := *f.title
	return &cp, nil
}

func (f *fakeStore) ReplaceSeasons(_ context.Context, _ int64, seasons []store.SeasonRow) error {
	f.seasonCalls++
	if f.seasonsErr != nil {
		return f.seasonsErr
	}
	f.seasons = seasons
	return nil
}

func (f *fakeStore) ReplaceProviders(_ context.Context, _ int64, region string, provs []store.ProviderRow) error {
	f.provCalls++
	if f.provsErr != nil {
		return f.provsErr
	}
	if f.provsByRegion == nil {
		f.provsByRegion = map[string][]store.ProviderRow{}
	}
	f.provsByRegion[region] = provs
	f.title.ProvidersRefreshedAt = now
	return nil
}

func (f *fakeStore) ReplaceFutureAirings(_ context.Context, _ int64, today string, airs []store.AiringRow) error {
	f.airCalls++
	if f.airErr != nil {
		return f.airErr
	}
	f.airToday, f.airings = today, airs
	f.title.AiringsRefreshedAt = now
	return nil
}

// svc builds a Service over the fakes with the fixed clock.
func svc(tm *fakeTMDB, tv *fakeTVMaze, st *fakeStore) *Service {
	return &Service{TMDB: tm, TVMaze: tv, Store: st, Now: func() time.Time { return now }}
}

// airingSeries is a cached series row with every class fresh as of `now`.
func airingSeries(tvmazeID *int64) *store.Title {
	return &store.Title{
		ID: 1, TMDBID: 1399, Kind: "series", TVMazeID: tvmazeID, Name: "GoT",
		Airing: true, RefreshedAt: now.Add(-time.Hour),
		ProvidersRefreshedAt: now.Add(-time.Hour), AiringsRefreshedAt: now.Add(-time.Hour),
	}
}

func TestEnsureTitleBadKind(t *testing.T) {
	tm, tv, st := &fakeTMDB{}, &fakeTVMaze{}, &fakeStore{}
	_, err := svc(tm, tv, st).EnsureTitle(context.Background(), "tv", 603, "US")
	if !errors.Is(err, ErrBadKind) {
		t.Fatalf("err = %v, want ErrBadKind", err)
	}
	if tm.movieCalls+tm.tvCalls+tm.provCalls+tv.lookupCalls+st.upserts != 0 {
		t.Fatal("bad kind must not touch upstreams or store")
	}
}

func TestFreshMovie(t *testing.T) {
	tm := &fakeTMDB{
		movie: tmdb.Movie{TMDBID: 603, Name: "The Matrix", Overview: "o", PosterPath: "/m.jpg", RuntimeMinutes: 136},
		provs: []tmdb.Provider{{ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}},
	}
	tv, st := &fakeTVMaze{}, &fakeStore{}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "movie", 603, "US")
	if err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if ti.Name != "The Matrix" || ti.Kind != "movie" || ti.RuntimeMinutes != 136 {
		t.Fatalf("title = %+v", ti)
	}
	if tm.movieCalls != 1 || tm.tvCalls != 0 || st.seasonCalls != 0 || tv.lookupCalls != 0 {
		t.Fatalf("calls: movie=%d tv=%d seasons=%d lookup=%d", tm.movieCalls, tm.tvCalls, st.seasonCalls, tv.lookupCalls)
	}
	if tm.provRegion != "US" || len(st.provsByRegion["US"]) != 1 || st.provsByRegion["US"][0].Name != "Netflix" {
		t.Fatalf("providers = %+v (region %q)", st.provsByRegion, tm.provRegion)
	}
}

func TestFreshAiringSeriesWithIMDB(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{
		TMDBID: 1399, Name: "GoT", Airing: true, IMDBID: "tt0944947",
		Seasons: []tmdb.Season{{Number: 1, EpisodeCount: 10}, {Number: 2, EpisodeCount: 10}},
	}}
	tv := &fakeTVMaze{show: tvmaze.Show{ID: 82}, eps: []tvmaze.Episode{
		{Season: 1, Number: 1, AirDate: "2020-01-01"}, // past — filtered
		{Season: 9, Number: 1, AirDate: ""},           // unscheduled — filtered
		{Season: 9, Number: 2, AirDate: "2026-07-15"}, // today — kept
		{Season: 9, Number: 3, AirDate: "2026-08-01"}, // future — kept
	}}
	st := &fakeStore{}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if ti.TVMazeID == nil || *ti.TVMazeID != 82 {
		t.Fatalf("tvmaze id = %v, want 82", ti.TVMazeID)
	}
	if len(st.seasons) != 2 || st.seasons[0].Number != 1 || st.seasons[0].EpisodeCount != 10 {
		t.Fatalf("seasons = %+v", st.seasons)
	}
	if st.airToday != "2026-07-15" {
		t.Fatalf("today = %q", st.airToday)
	}
	want := []store.AiringRow{{Season: 9, Episode: 2, AirDate: "2026-07-15"}, {Season: 9, Episode: 3, AirDate: "2026-08-01"}}
	if len(st.airings) != 2 || st.airings[0] != want[0] || st.airings[1] != want[1] {
		t.Fatalf("airings = %+v, want %+v", st.airings, want)
	}
}

func TestFreshSeriesWithoutIMDB(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 1, Name: "Obscure", Airing: true, IMDBID: ""}}
	tv, st := &fakeTVMaze{}, &fakeStore{}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.lookupCalls != 0 || st.airCalls != 0 {
		t.Fatalf("TVMaze touched without an IMDB id: lookups=%d airings=%d", tv.lookupCalls, st.airCalls)
	}
}

func TestFreshEndedSeriesSkipsTVMaze(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 2, Name: "Ended", Airing: false, IMDBID: "tt0000001"}}
	tv, st := &fakeTVMaze{}, &fakeStore{}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 2, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.lookupCalls != 0 || tv.epsCalls != 0 {
		t.Fatal("ended series must skip TVMaze entirely")
	}
}

func TestFreshTitleNotFound(t *testing.T) {
	tm := &fakeTMDB{movieErr: tmdb.ErrNotFound}
	_, err := svc(tm, &fakeTVMaze{}, &fakeStore{}).EnsureTitle(context.Background(), "movie", 999, "US")
	if !errors.Is(err, ErrTitleNotFound) {
		t.Fatalf("err = %v, want ErrTitleNotFound", err)
	}
}

func TestFreshTMDBDownErrors(t *testing.T) {
	tm := &fakeTMDB{movieErr: errors.New("boom")}
	st := &fakeStore{}
	_, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "movie", 603, "US")
	if err == nil {
		t.Fatal("want error on fresh ingest with TMDB down (nothing cached)")
	}
	if st.upserts != 0 {
		t.Fatal("no store writes on failed fresh ingest")
	}
}

func TestFreshProvidersDownDegrades(t *testing.T) {
	tm := &fakeTMDB{
		movie:   tmdb.Movie{TMDBID: 603, Name: "The Matrix"},
		provErr: errors.New("boom"),
	}
	st := &fakeStore{}
	ti, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "movie", 603, "US")
	if err != nil || ti == nil {
		t.Fatalf("providers-down fresh ingest must still land the title: %v", err)
	}
	if st.provCalls != 0 {
		t.Fatal("ReplaceProviders must not run when the fetch failed")
	}
}

func TestFreshTVMazeDownDegrades(t *testing.T) {
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 1399, Name: "GoT", Airing: true, IMDBID: "tt0944947"}}
	tv := &fakeTVMaze{lookupErr: tvmaze.ErrNotFound}
	st := &fakeStore{}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil || ti == nil {
		t.Fatalf("TVMaze-down fresh ingest must still land the title: %v", err)
	}
	if st.airCalls != 0 {
		t.Fatal("no airings written when lookup failed")
	}
}

func TestMetadataRefreshAfterTTL(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.RefreshedAt = now.Add(-31 * 24 * time.Hour) // stale metadata
	tm := &fakeTMDB{tv: tmdb.TV{TMDBID: 1399, Name: "GoT Renamed", Airing: true, IMDBID: "tt0944947",
		Seasons: []tmdb.Season{{Number: 1, EpisodeCount: 10}}}}
	tv, st := &fakeTVMaze{}, &fakeStore{title: cached}
	ti, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tm.tvCalls != 1 || st.upserts != 1 || st.seasonCalls != 1 {
		t.Fatalf("calls: tv=%d upserts=%d seasons=%d, want 1/1/1", tm.tvCalls, st.upserts, st.seasonCalls)
	}
	if ti.Name != "GoT Renamed" {
		t.Fatalf("title = %+v", ti)
	}
	if tm.provCalls != 0 {
		t.Fatal("fresh providers class must not refresh")
	}
	if tv.lookupCalls != 0 {
		t.Fatal("stored tvmaze_id must not re-lookup")
	}
}

func TestProvidersRefreshOnly(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.ProvidersRefreshedAt = now.Add(-8 * 24 * time.Hour) // stale providers
	tm := &fakeTMDB{provs: []tmdb.Provider{{ID: 8, Name: "Netflix"}}}
	tv, st := &fakeTVMaze{}, &fakeStore{title: cached}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "GB"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tm.tvCalls != 0 || tm.provCalls != 1 || tv.epsCalls != 0 {
		t.Fatalf("calls: tv=%d prov=%d eps=%d, want 0/1/0", tm.tvCalls, tm.provCalls, tv.epsCalls)
	}
	if tm.provRegion != "GB" || len(st.provsByRegion["GB"]) != 1 {
		t.Fatalf("region = %q, provs = %+v", tm.provRegion, st.provsByRegion)
	}
}

func TestAiringsRefreshOnly(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.AiringsRefreshedAt = now.Add(-25 * time.Hour) // stale airings
	tm := &fakeTMDB{}
	tv := &fakeTVMaze{eps: []tvmaze.Episode{{Season: 9, Number: 4, AirDate: "2026-09-01"}}}
	st := &fakeStore{title: cached}
	if _, err := svc(tm, tv, st).EnsureTitle(context.Background(), "series", 1399, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.lookupCalls != 0 || tv.epsCalls != 1 || tm.tvCalls != 0 || tm.provCalls != 0 {
		t.Fatalf("calls: lookup=%d eps=%d tv=%d prov=%d, want 0/1/0/0", tv.lookupCalls, tv.epsCalls, tm.tvCalls, tm.provCalls)
	}
	if st.airToday != "2026-07-15" || len(st.airings) != 1 {
		t.Fatalf("airings = %+v today=%q", st.airings, st.airToday)
	}
}

func TestAiringsSkippedWhenFreshOrNotAiring(t *testing.T) {
	// Fresh stamp: no TVMaze call.
	tv := &fakeTVMaze{}
	st := &fakeStore{title: airingSeries(int64p(82))}
	if _, err := svc(&fakeTMDB{}, tv, st).EnsureTitle(context.Background(), "series", 1399, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv.epsCalls != 0 {
		t.Fatal("fresh airings must not refresh")
	}

	// Ended show with stale stamp: still no TVMaze call.
	ended := airingSeries(int64p(82))
	ended.Airing = false
	ended.AiringsRefreshedAt = now.Add(-48 * time.Hour)
	tv2 := &fakeTVMaze{}
	st2 := &fakeStore{title: ended}
	if _, err := svc(&fakeTMDB{}, tv2, st2).EnsureTitle(context.Background(), "series", 1399, "US"); err != nil {
		t.Fatalf("EnsureTitle: %v", err)
	}
	if tv2.epsCalls != 0 {
		t.Fatal("ended series must not refresh airings")
	}
}

func TestRefreshUpstreamDownServesCache(t *testing.T) {
	for name, tvErr := range map[string]error{"tmdb 500": errors.New("boom"), "tmdb 404": tmdb.ErrNotFound} {
		cached := airingSeries(int64p(82))
		cached.RefreshedAt = now.Add(-31 * 24 * time.Hour)
		tm := &fakeTMDB{tvErr: tvErr}
		st := &fakeStore{title: cached}
		ti, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "series", 1399, "US")
		if err != nil || ti == nil || ti.Name != "GoT" {
			t.Fatalf("%s: cached row must be served unchanged: ti=%+v err=%v", name, ti, err)
		}
		if st.upserts != 0 {
			t.Fatalf("%s: no writes on failed refresh", name)
		}
	}
}

func TestRefreshProvidersDownServesCache(t *testing.T) {
	cached := airingSeries(int64p(82))
	cached.ProvidersRefreshedAt = now.Add(-8 * 24 * time.Hour)
	tm := &fakeTMDB{provErr: errors.New("boom")}
	st := &fakeStore{title: cached}
	ti, err := svc(tm, &fakeTVMaze{}, st).EnsureTitle(context.Background(), "series", 1399, "US")
	if err != nil || ti == nil {
		t.Fatalf("cached row must be served: %v", err)
	}
	if st.provCalls != 0 {
		t.Fatal("ReplaceProviders must not run when the fetch failed")
	}
}

func int64p(v int64) *int64 { return &v }
