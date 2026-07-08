package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/store"
)

// EntryStore is the slice of *store.Store the shelf/entry handlers need.
type EntryStore interface {
	UpsertEntry(ctx context.Context, userID, titleID int64, u store.EntryUpdate) (*store.Entry, error)
	CountRotation(ctx context.Context, userID, excludeTitleID int64) (int, error)
	Shelf(ctx context.Context, userID int64, shelf string) ([]store.Entry, error)
}

// rotationCap is fixed at 8 in v1 (design spec).
const rotationCap = 8

var validStatuses = map[string]bool{"none": true, "watchlist": true, "rotation": true, "watched": true}

var validShelves = map[string]bool{"watchlist": true, "rotation": true, "watched": true, "favorites": true, "ratings": true}

// validRating reports whether v is a half-step in [0.5, 5.0]. The DB CHECK
// covers the range; half-step granularity is API policy.
func validRating(v float64) bool {
	d := v * 2
	return v >= 0.5 && v <= 5.0 && d == math.Trunc(d)
}

func handlePatchEntry(entries EntryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		titleID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || titleID < 1 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		var body struct {
			Status   *string         `json:"status"`
			Rating   json.RawMessage `json:"rating"`
			Favorite *bool           `json:"favorite"`
			Pointer  *store.Pointer  `json:"pointer"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Status == nil && body.Rating == nil && body.Favorite == nil && body.Pointer == nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "nothing to update")
			return
		}
		if body.Status != nil && !validStatuses[*body.Status] {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid status")
			return
		}
		u := store.EntryUpdate{Status: body.Status, Favorite: body.Favorite, Pointer: body.Pointer}
		if body.Rating != nil {
			if string(body.Rating) == "null" {
				u.ClearRating = true
			} else {
				var v float64
				if err := json.Unmarshal(body.Rating, &v); err != nil || !validRating(v) {
					writeJSONError(w, http.StatusUnprocessableEntity, "invalid rating")
					return
				}
				u.Rating = &v
			}
		}
		if body.Pointer != nil && (body.Pointer.Season < 1 || body.Pointer.Episode < 1) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid pointer")
			return
		}

		user := userFrom(r.Context())
		if body.Status != nil && *body.Status == "rotation" {
			n, cerr := entries.CountRotation(r.Context(), user.ID, titleID)
			if cerr != nil {
				writeJSONError(w, http.StatusInternalServerError, "internal")
				return
			}
			if n >= rotationCap {
				writeJSONError(w, http.StatusConflict, "rotation_full")
				return
			}
		}

		e, err := entries.UpsertEntry(r.Context(), user.ID, titleID, u)
		switch {
		case errors.Is(err, store.ErrTitleNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(e)
	}
}

func handleGetShelf(entries EntryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shelf := chi.URLParam(r, "shelf")
		if !validShelves[shelf] {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		user := userFrom(r.Context())
		list, err := entries.Shelf(r.Context(), user.ID, shelf)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]store.Entry{"entries": list})
	}
}
