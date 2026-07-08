package httpserver

import (
	"encoding/json"
	"net/http"

	"github.com/shottah/lineup/api/internal/prefs"
)

func handleGetMe(w http.ResponseWriter, r *http.Request) {
	writeUser(w, r)
}

func handlePatchMe(users UserStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Region        *string         `json:"region"`
			SchedulePrefs json.RawMessage `json:"schedule_prefs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Region == nil && body.SchedulePrefs == nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "nothing to update")
			return
		}
		if body.Region != nil && *body.Region == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "region must be non-empty")
			return
		}
		if body.SchedulePrefs != nil {
			if err := prefs.Validate(body.SchedulePrefs); err != nil {
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid schedule_prefs")
				return
			}
		}
		u := userFrom(r.Context())
		updated, err := users.UpdateUserPrefs(r.Context(), u.ID, body.Region, body.SchedulePrefs)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}
}

func writeUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userFrom(r.Context()))
}
