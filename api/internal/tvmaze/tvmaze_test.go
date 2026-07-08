package tvmaze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
