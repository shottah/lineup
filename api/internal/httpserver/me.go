package httpserver

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/shottah/lineup/api/internal/docstore"
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
			Timezone      *string         `json:"timezone"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Region == nil && body.SchedulePrefs == nil && body.Timezone == nil {
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
		if body.Timezone != nil && !validTimezone(*body.Timezone) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid timezone")
			return
		}
		u := userFrom(r.Context())
		updated, err := users.UpdateUserPrefs(r.Context(), u.ID, docstore.UserPrefsUpdate{
			Region: body.Region, SchedulePrefs: body.SchedulePrefs, Timezone: body.Timezone,
		})
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)
	}
}

// validTimezone accepts "" (UTC) and IANA zone names. "Local" is rejected:
// it names the server's zone, not the user's.
func validTimezone(tz string) bool {
	if tz == "" {
		return true
	}
	if tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// userToday is the user's current calendar date (YYYY-MM-DD) in their
// stored timezone. An unset or unloadable timezone falls back to UTC.
func userToday(u *docstore.User, now time.Time) string {
	loc := time.UTC
	if u != nil && u.Timezone != "" {
		if l, err := time.LoadLocation(u.Timezone); err == nil {
			loc = l
		}
	}
	return now.In(loc).Format(dateFmt)
}

func writeUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(userFrom(r.Context()))
}
