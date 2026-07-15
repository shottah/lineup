package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/ingest"
	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
)

// SearchClient is the slice of *tmdb.Client the search proxy needs.
type SearchClient interface {
	SearchMulti(ctx context.Context, query string) ([]tmdb.SearchResult, error)
}

// Ingester is the slice of *ingest.Service the title handler needs.
type Ingester interface {
	EnsureTitle(ctx context.Context, kind string, tmdbID int64, region string) (*store.Title, error)
}

// TitleReader is the slice of *store.Store the title handler needs.
type TitleReader interface {
	GetTitleFull(ctx context.Context, userID, titleID int64, region string) (*store.TitleFull, error)
}

// searchResult is the /v1/search wire shape (snake_case like every other
// endpoint).
type searchResult struct {
	TMDBID     int64  `json:"tmdb_id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	PosterPath string `json:"poster_path"`
	Year       string `json:"year"`
}

// handleSearch is a thin TMDB proxy: no DB reads or writes.
func handleSearch(search SearchClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "q required")
			return
		}
		hits, err := search.SearchMulti(r.Context(), q)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream")
			return
		}
		out := make([]searchResult, 0, len(hits))
		for _, h := range hits {
			out = append(out, searchResult{TMDBID: h.TMDBID, Kind: h.Kind, Name: h.Name,
				Overview: h.Overview, PosterPath: h.PosterPath, Year: h.Year})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]searchResult{"results": out})
	}
}

// handleGetTitle ingests-if-absent then returns the title-page payload.
func handleGetTitle(ing Ingester, titles TitleReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind := chi.URLParam(r, "kind")
		if kind != "movie" && kind != "series" {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		tmdbID, err := strconv.ParseInt(chi.URLParam(r, "tmdbID"), 10, 64)
		if err != nil || tmdbID < 1 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		user := userFrom(r.Context())
		title, err := ing.EnsureTitle(r.Context(), kind, tmdbID, user.Region)
		switch {
		case errors.Is(err, ingest.ErrTitleNotFound), errors.Is(err, ingest.ErrBadKind):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			// Fresh-ingest upstream failures land here; DB errors do too,
			// which 502 slightly mislabels — acceptable for v1, where the
			// dominant failure mode on this path is TMDB.
			writeJSONError(w, http.StatusBadGateway, "upstream")
			return
		}
		full, err := titles.GetTitleFull(r.Context(), user.ID, title.ID, user.Region)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(full)
	}
}
