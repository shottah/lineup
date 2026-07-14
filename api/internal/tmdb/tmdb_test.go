package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fixture reads a testdata file or fails the test.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return b
}

func TestSearchMulti(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "search_multi.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	results, err := c.SearchMulti(context.Background(), "the matrix")
	if err != nil {
		t.Fatalf("SearchMulti: %v", err)
	}
	if gotPath != "/3/search/multi?query=the+matrix" {
		t.Fatalf("requested %q, want /3/search/multi?query=the+matrix", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	// Fixture holds one movie (603), one tv (1399), one person; the person
	// must be dropped and tv mapped to "series".
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2 (person dropped): %+v", len(results), results)
	}
	movie, series := results[0], results[1]
	if movie.Kind != "movie" || movie.TMDBID != 603 || movie.Name != "The Matrix" || movie.Year != "1999" {
		t.Fatalf("movie = %+v, want movie/603/The Matrix/1999", movie)
	}
	if movie.Overview == "" || movie.PosterPath == "" {
		t.Fatalf("movie overview/poster not decoded: %+v", movie)
	}
	if series.Kind != "series" || series.TMDBID != 1399 || series.Name != "Game of Thrones" || series.Year != "2011" {
		t.Fatalf("series = %+v, want series/1399/Game of Thrones/2011", series)
	}
}

func TestNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"status_code":34,"status_message":"not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	if _, err := c.SearchMulti(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"page":1,"results":[]}`))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	results, err := c.SearchMulti(context.Background(), "q")
	if err != nil {
		t.Fatalf("SearchMulti after retry: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (exactly one retry)", calls)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want empty", results)
	}
}

func TestRetryCapOn500(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	_, err := c.SearchMulti(context.Background(), "q")
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("err = %v, want unexpected status 500", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (retry capped at one)", calls)
	}
}

func TestMovieDetails(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "movie_details.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	m, err := c.MovieDetails(context.Background(), 603)
	if err != nil {
		t.Fatalf("MovieDetails: %v", err)
	}
	if gotPath != "/3/movie/603" {
		t.Fatalf("requested %q, want /3/movie/603", gotPath)
	}
	if m.TMDBID != 603 || m.Name != "The Matrix" || m.RuntimeMinutes != 136 {
		t.Fatalf("movie = %+v, want 603/The Matrix/136m", m)
	}
	if m.Overview == "" || m.PosterPath == "" {
		t.Fatalf("overview/poster not decoded: %+v", m)
	}
}

// gotRuntimeMins is Game of Thrones' first episode_run_time entry as
// recorded in tv_details.json. If Task 2's recording printed a different
// episode_run_time array (TMDB data drifts), set this to its first
// element — or 0 if the array was empty.
const gotRuntimeMins = 0

func TestTVDetails(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "tv_details.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	tv, err := c.TVDetails(context.Background(), 1399)
	if err != nil {
		t.Fatalf("TVDetails: %v", err)
	}
	if gotPath != "/3/tv/1399?append_to_response=external_ids" {
		t.Fatalf("requested %q, want /3/tv/1399?append_to_response=external_ids", gotPath)
	}
	if tv.TMDBID != 1399 || tv.Name != "Game of Thrones" {
		t.Fatalf("tv = %+v, want 1399/Game of Thrones", tv)
	}
	if tv.IMDBID != "tt0944947" {
		t.Fatalf("IMDBID = %q, want tt0944947", tv.IMDBID)
	}
	if tv.Airing {
		t.Fatalf("Airing = true for status Ended, want false")
	}
	if tv.RuntimeMinutes != gotRuntimeMins {
		t.Fatalf("RuntimeMinutes = %d, want %d", tv.RuntimeMinutes, gotRuntimeMins)
	}
	// The fixture contains season 0 (specials) plus seasons 1-8; season 0
	// must be excluded and season 1's episode count preserved.
	if len(tv.Seasons) != 8 {
		t.Fatalf("got %d seasons, want 8 (specials excluded): %+v", len(tv.Seasons), tv.Seasons)
	}
	for _, s := range tv.Seasons {
		if s.Number == 0 {
			t.Fatalf("season 0 not excluded: %+v", tv.Seasons)
		}
	}
	if tv.Seasons[0].Number != 1 || tv.Seasons[0].EpisodeCount != 10 {
		t.Fatalf("season 1 = %+v, want number 1 / 10 episodes", tv.Seasons[0])
	}
}

func TestTVDetailsAiringWithoutIMDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "tv_details_variant.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	tv, err := c.TVDetails(context.Background(), 1399)
	if err != nil {
		t.Fatalf("TVDetails: %v", err)
	}
	if !tv.Airing {
		t.Fatalf("Airing = false for status Returning Series, want true")
	}
	if tv.IMDBID != "" {
		t.Fatalf("IMDBID = %q for null external imdb_id, want empty", tv.IMDBID)
	}
	if tv.RuntimeMinutes != 0 {
		t.Fatalf("RuntimeMinutes = %d for empty episode_run_time, want 0", tv.RuntimeMinutes)
	}
}

func TestWatchProviders(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "watch_providers.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	got, err := c.WatchProviders(context.Background(), "series", 1399, "US")
	if err != nil {
		t.Fatalf("WatchProviders: %v", err)
	}
	if gotPath != "/3/tv/1399/watch/providers" {
		t.Fatalf("requested %q, want /3/tv/1399/watch/providers (series maps to tv)", gotPath)
	}

	// Expected set derives from the fixture's own US.flatrate array; the
	// fixture also carries buy/rent sections, which must NOT leak through.
	var raw struct {
		Results map[string]struct {
			Flatrate []struct {
				ProviderID int64  `json:"provider_id"`
				Name       string `json:"provider_name"`
				LogoPath   string `json:"logo_path"`
			} `json:"flatrate"`
		} `json:"results"`
	}
	if err := json.Unmarshal(fixture(t, "watch_providers.json"), &raw); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	want := raw.Results["US"].Flatrate
	if len(want) == 0 {
		t.Fatal("fixture has no US flatrate entries; re-record Task 2")
	}
	if len(got) != len(want) {
		t.Fatalf("got %d providers, want %d (flatrate only): %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.ProviderID || got[i].Name != w.Name || got[i].LogoPath != w.LogoPath {
			t.Fatalf("provider[%d] = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestWatchProvidersRegionMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "watch_providers.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	got, err := c.WatchProviders(context.Background(), "series", 1399, "ZZ")
	if err != nil {
		t.Fatalf("WatchProviders(ZZ): %v, want nil error (no home region is data, not failure)", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v for absent region, want empty", got)
	}
}

func TestWatchProvidersBadKind(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		calls++
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL, "test-token")
	if _, err := c.WatchProviders(context.Background(), "tv", 1399, "US"); err == nil {
		t.Fatal("kind \"tv\" accepted, want error (Lineup vocabulary is movie|series)")
	}
	if calls != 0 {
		t.Fatalf("bad kind reached the server (%d calls), want validation before request", calls)
	}
}
