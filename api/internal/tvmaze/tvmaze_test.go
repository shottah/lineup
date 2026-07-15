package tvmaze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
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

func TestLookupByIMDB(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "lookup_show.json"))
	}))
	defer srv.Close()

	c := NewWithBaseURL(srv.URL)
	show, err := c.LookupByIMDB(context.Background(), "tt0944947")
	if err != nil {
		t.Fatalf("LookupByIMDB: %v", err)
	}
	if gotPath != "/lookup/shows?imdb=tt0944947" {
		t.Fatalf("requested %q, want /lookup/shows?imdb=tt0944947", gotPath)
	}
	if show.ID != 82 || show.Name != "Game of Thrones" || show.Status != "Ended" {
		t.Fatalf("show = %+v, want id 82 / Game of Thrones / Ended", show)
	}
}

func TestLookupByIMDBNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"name":"Not Found","message":"","code":0,"status":404}`, http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEpisodes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "episodes.json"))
	}))
	defer srv.Close()

	eps, err := NewWithBaseURL(srv.URL).Episodes(context.Background(), 82)
	if err != nil {
		t.Fatalf("Episodes: %v", err)
	}
	if gotPath != "/shows/82/episodes" {
		t.Fatalf("requested %q, want /shows/82/episodes", gotPath)
	}
	if len(eps) != 3 {
		t.Fatalf("len = %d, want 3", len(eps))
	}
	if eps[0].Season != 1 || eps[0].Number != 1 || eps[0].AirDate != "2011-04-17" {
		t.Fatalf("eps[0] = %+v", eps[0])
	}
	// Blank and null air dates both decode to "" without error.
	if eps[1].AirDate != "" || eps[2].AirDate != "" {
		t.Fatalf("blank/null airdates = %q, %q; want empty strings", eps[1].AirDate, eps[2].AirDate)
	}
	if eps[2].Season != 2 || eps[2].Number != 1 {
		t.Fatalf("eps[2] = %+v", eps[2])
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "lookup_show.json"))
	}))
	defer srv.Close()

	show, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0944947")
	if err != nil {
		t.Fatalf("LookupByIMDB after 429: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (exactly one retry)", calls)
	}
	if show.ID != 82 {
		t.Fatalf("show = %+v", show)
	}
}

func TestNoSecondRetryOn500(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0944947")
	if err == nil {
		t.Fatal("want error after repeated 500s")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("500 must not map to ErrNotFound")
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want exactly 2 (one retry, then give up)", calls)
	}
}

func TestNotFoundDoesNotRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0000000")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (404 never retries)", calls)
	}
}

func TestRetryDefaultDelayWithoutHeader(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // no Retry-After header
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture(t, "lookup_show.json"))
	}))
	defer srv.Close()

	start := time.Now()
	show, err := NewWithBaseURL(srv.URL).LookupByIMDB(context.Background(), "tt0944947")
	if err != nil {
		t.Fatalf("LookupByIMDB after 503: %v", err)
	}
	if calls != 2 || show.ID != 82 {
		t.Fatalf("calls = %d, show = %+v", calls, show)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Fatalf("retried after %v, want >= 500ms default backoff", elapsed)
	}
}
