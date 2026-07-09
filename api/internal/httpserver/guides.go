// api/internal/httpserver/guides.go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/guide"
	"github.com/shottah/lineup/api/internal/prefs"
	"github.com/shottah/lineup/api/internal/store"
)

// GuideStore is the slice of *store.Store the guide handlers need.
type GuideStore interface {
	GuideInputTitles(ctx context.Context, userID int64, region string) ([]guide.Title, error)
	CreateGuideReplacingOverlaps(ctx context.Context, userID int64, startDate, endDate string, seed int64, items []guide.Item) (*store.Guide, error)
	CurrentGuide(ctx context.Context, userID int64, today string) (*store.Guide, error)
	GuideWithItems(ctx context.Context, userID, guideID int64) (*store.Guide, error)
	ReplaceUnkeptItems(ctx context.Context, userID, guideID int64, keepIDs []int64, newItems []guide.Item) (*store.Guide, error)
	UpdateGuideItem(ctx context.Context, userID, guideID, itemID int64, upd store.GuideItemUpdate) (*store.GuideItem, error)
	DeleteGuideItem(ctx context.Context, userID, guideID, itemID int64) error
	MarkItemWatched(ctx context.Context, userID, guideID, itemID int64) (*store.GuideItem, error)
	SwapTitle(ctx context.Context, userID, titleID int64) (*store.SwapInfo, error)
}

const dateFmt = "2006-01-02"

// guideDays maps a start date + length onto the user's weekday windows.
func guideDays(start time.Time, days int, windows map[string]prefs.ParsedWindow) []guide.Day {
	out := make([]guide.Day, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		key := strings.ToLower(d.Weekday().String()[:3])
		w := guide.Window{}
		if pw, ok := windows[key]; ok && pw.Enabled {
			w = guide.Window{StartMin: pw.StartMin, EndMin: pw.EndMin}
		}
		out[i] = guide.Day{Date: d.Format(dateFmt), Window: w}
	}
	return out
}

func (d Deps) buildInput(ctx context.Context, u *store.User, start time.Time, days int, seed int64, keep []guide.Item) (guide.Input, error) {
	windows, err := prefs.Windows(u.SchedulePrefs)
	if err != nil {
		return guide.Input{}, err
	}
	titles, err := d.Guides.GuideInputTitles(ctx, u.ID, u.Region)
	if err != nil {
		return guide.Input{}, err
	}
	return guide.Input{Seed: seed, Days: guideDays(start, days, windows), Titles: titles, Keep: keep}, nil
}

func handleCreateGuide(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			StartDate string `json:"start_date"`
			Days      int    `json:"days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		start, err := time.Parse(dateFmt, body.StartDate)
		if err != nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid start_date")
			return
		}
		if body.Days < 1 || body.Days > 14 {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid days")
			return
		}
		u := userFrom(r.Context())
		seed := d.Now().UnixNano()
		in, err := d.buildInput(r.Context(), u, start, body.Days, seed, nil)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		items := guide.Generate(in)
		end := start.AddDate(0, 0, body.Days-1).Format(dateFmt)
		g, err := d.Guides.CreateGuideReplacingOverlaps(r.Context(), u.ID, body.StartDate, end, seed, items)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(g)
	}
}

func handleCurrentGuide(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		g, err := d.Guides.CurrentGuide(r.Context(), u.ID, d.Now().UTC().Format(dateFmt))
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g)
	}
}

func handleRegenerate(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok := pathID(r, "id")
		if !ok {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		g, err := d.Guides.GuideWithItems(r.Context(), u.ID, gid)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}

		today := d.Now().UTC().Format(dateFmt)
		var keepIDs []int64
		var keep []guide.Item
		for _, it := range g.Items {
			if it.Pinned || it.Edited || it.Watched || it.Date < today {
				keepIDs = append(keepIDs, it.ID)
				keep = append(keep, guide.Item{Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin,
					TitleID: it.TitleID, Season: it.Season, Episode: it.Episode, Provider: it.ProviderID,
					IsPlan: it.IsPlan, Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched})
			}
		}

		start, err := time.Parse(dateFmt, g.StartDate)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		end, err := time.Parse(dateFmt, g.EndDate)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		days := int(end.Sub(start).Hours()/24) + 1

		in, err := d.buildInput(r.Context(), u, start, days, g.Seed, keep)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		out := guide.Generate(in)

		kept := map[guide.Item]bool{}
		for _, k := range keep {
			kept[k] = true
		}
		newItems := []guide.Item{}
		for _, it := range out {
			if !kept[it] {
				newItems = append(newItems, it)
			}
		}

		refreshed, err := d.Guides.ReplaceUnkeptItems(r.Context(), u.ID, gid, keepIDs, newItems)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(refreshed)
	}
}

func handlePatchItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		var body struct {
			Date     *string `json:"date"`
			StartMin *int    `json:"start_min"`
			TitleID  *int64  `json:"title_id"`
			Pinned   *bool   `json:"pinned"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed json")
			return
		}
		if body.Date == nil && body.StartMin == nil && body.TitleID == nil && body.Pinned == nil {
			writeJSONError(w, http.StatusUnprocessableEntity, "nothing to update")
			return
		}
		if body.Date != nil {
			if _, err := time.Parse(dateFmt, *body.Date); err != nil {
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid date")
				return
			}
		}
		if body.StartMin != nil && (*body.StartMin < 0 || *body.StartMin > 1440) {
			writeJSONError(w, http.StatusUnprocessableEntity, "invalid start_min")
			return
		}

		u := userFrom(r.Context())
		upd := store.GuideItemUpdate{Date: body.Date, StartMin: body.StartMin, Pinned: body.Pinned,
			SetEdited: body.Date != nil || body.StartMin != nil || body.TitleID != nil}

		if body.TitleID != nil {
			info, err := d.Guides.SwapTitle(r.Context(), u.ID, *body.TitleID)
			switch {
			case errors.Is(err, store.ErrTitleNotFound):
				writeJSONError(w, http.StatusUnprocessableEntity, "invalid title")
				return
			case err != nil:
				writeJSONError(w, http.StatusInternalServerError, "internal")
				return
			}
			upd.TitleID = &info.TitleID
			upd.DurationMin = &info.Runtime
			season, episode := 0, 0
			if info.Kind == "series" {
				season, episode = info.PointerSeason, info.PointerEpisode
			}
			upd.Season, upd.Episode = &season, &episode
		}

		it, err := d.Guides.UpdateGuideItem(r.Context(), u.ID, gid, itemID, upd)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(it)
	}
}

func handleDeleteItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		err := d.Guides.DeleteGuideItem(r.Context(), u.ID, gid, itemID)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleWatchItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		it, err := d.Guides.MarkItemWatched(r.Context(), u.ID, gid, itemID)
		switch {
		case errors.Is(err, store.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(it)
	}
}

// pathID parses a positive int64 chi URL param.
func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	return id, err == nil && id >= 1
}
