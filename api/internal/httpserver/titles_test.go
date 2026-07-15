package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shottah/lineup/api/internal/ingest"
	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
)

type fakeSearch struct {
	results []tmdb.SearchResult
	err     error
	gotQ    string
}

func (f *fakeSearch) SearchMulti(_ context.Context, q string) ([]tmdb.SearchResult, error) {
	f.gotQ = q
	return f.results, f.err
}

type fakeIngester struct {
	title              *store.Title
	err                error
	gotKind, gotRegion string
	gotTMDBID          int64
}

func (f *fakeIngester) EnsureTitle(_ context.Context, kind string, tmdbID int64, region string) (*store.Title, error) {
	f.gotKind, f.gotTMDBID, f.gotRegion = kind, tmdbID, region
	return f.title, f.err
}

type fakeTitleReader struct {
	full      *store.TitleFull
	err       error
	gotUserID int64
	gotTitle  int64
	gotRegion string
}

func (f *fakeTitleReader) GetTitleFull(_ context.Context, userID, titleID int64, region string) (*store.TitleFull, error) {
	f.gotUserID, f.gotTitle, f.gotRegion = userID, titleID, region
	return f.full, f.err
}

// titlesServer mounts the full authed router with search/ingest fakes.
func titlesServer(t *testing.T, fs *fakeSearch, fi *fakeIngester, fr *fakeTitleReader) http.Handler {
	t.Helper()
	return New(Deps{
		Users:    newFakeUsers(),
		Verifier: fakeVerifierWithTok1(),
		Search:   fs,
		Ingest:   fi,
		Titles:   fr,
	}).Handler
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSearchHappyPath(t *testing.T) {
	fs := &fakeSearch{results: []tmdb.SearchResult{
		{TMDBID: 603, Kind: "movie", Name: "The Matrix", Overview: "o", PosterPath: "/m.jpg", Year: "1999"},
		{TMDBID: 1399, Kind: "series", Name: "GoT", Year: "2011"},
	}}
	rec := get(t, titlesServer(t, fs, &fakeIngester{}, &fakeTitleReader{}), "/v1/search?q=the+matrix")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if fs.gotQ != "the matrix" {
		t.Fatalf("query = %q", fs.gotQ)
	}
	var body struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Results) != 2 {
		t.Fatalf("results = %+v", body.Results)
	}
	first := body.Results[0]
	if first["tmdb_id"] != float64(603) || first["kind"] != "movie" || first["name"] != "The Matrix" ||
		first["poster_path"] != "/m.jpg" || first["year"] != "1999" {
		t.Fatalf("first result = %+v (keys must be snake_case)", first)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	h := titlesServer(t, &fakeSearch{}, &fakeIngester{}, &fakeTitleReader{})
	for _, path := range []string{"/v1/search", "/v1/search?q=", "/v1/search?q=%20%20"} {
		if rec := get(t, h, path); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d, want 422", path, rec.Code)
		}
	}
}

func TestSearchUpstreamDown(t *testing.T) {
	fs := &fakeSearch{err: errors.New("boom")}
	rec := get(t, titlesServer(t, fs, &fakeIngester{}, &fakeTitleReader{}), "/v1/search?q=x")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestGetTitleHappyPath(t *testing.T) {
	title := &store.Title{ID: 7, TMDBID: 1399, Kind: "series", Name: "GoT"}
	fi := &fakeIngester{title: title}
	fr := &fakeTitleReader{full: &store.TitleFull{
		Title:     *title,
		Seasons:   []store.SeasonRow{{Number: 1, EpisodeCount: 10}},
		Providers: []store.ProviderRow{{ID: 8, Name: "Netflix", LogoPath: "/n.jpg"}},
	}}
	rec := get(t, titlesServer(t, &fakeSearch{}, fi, fr), "/v1/titles/series/1399")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if fi.gotKind != "series" || fi.gotTMDBID != 1399 || fi.gotRegion != "US" {
		t.Fatalf("ingester got %s/%d/%s, want series/1399/US", fi.gotKind, fi.gotTMDBID, fi.gotRegion)
	}
	if fr.gotTitle != 7 || fr.gotRegion != "US" || fr.gotUserID == 0 {
		t.Fatalf("reader got user=%d title=%d region=%s", fr.gotUserID, fr.gotTitle, fr.gotRegion)
	}
	var body struct {
		Title struct {
			TMDBID int64  `json:"tmdb_id"`
			Name   string `json:"name"`
		} `json:"title"`
		Seasons   []store.SeasonRow   `json:"seasons"`
		Providers []store.ProviderRow `json:"providers"`
		Entry     *store.Entry        `json:"entry"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Title.TMDBID != 1399 || body.Title.Name != "GoT" || len(body.Seasons) != 1 ||
		len(body.Providers) != 1 || body.Entry != nil {
		t.Fatalf("payload = %+v", body)
	}
}

func TestGetTitleBadRoute(t *testing.T) {
	h := titlesServer(t, &fakeSearch{}, &fakeIngester{}, &fakeTitleReader{})
	for _, path := range []string{"/v1/titles/tv/1399", "/v1/titles/series/abc", "/v1/titles/series/0"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

func TestGetTitleErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{ingest.ErrTitleNotFound, http.StatusNotFound},
		{errors.New("tmdb down"), http.StatusBadGateway},
	}
	for _, c := range cases {
		fi := &fakeIngester{err: c.err}
		rec := get(t, titlesServer(t, &fakeSearch{}, fi, &fakeTitleReader{}), "/v1/titles/movie/603")
		if rec.Code != c.want {
			t.Fatalf("err %v: status = %d, want %d", c.err, rec.Code, c.want)
		}
	}
}

func TestSearchRequiresAuth(t *testing.T) {
	h := titlesServer(t, &fakeSearch{}, &fakeIngester{}, &fakeTitleReader{})
	req := httptest.NewRequest(http.MethodGet, "/v1/search?q=x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
