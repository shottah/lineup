// api/internal/httpserver/guides.go
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shottah/lineup/api/internal/docstore"
	"github.com/shottah/lineup/api/internal/guide"
	"github.com/shottah/lineup/api/internal/prefs"
)

// GuideStore is the slice of *docstore.Store the guide handlers need.
type GuideStore interface {
	GuideInputTitles(ctx context.Context, userID, region string) ([]guide.Title, error)
	CreateGuideReplacingOverlaps(ctx context.Context, userID, startDate, endDate string, seed int64, items []guide.Item) (*docstore.Guide, error)
	CurrentGuide(ctx context.Context, userID, today string) (*docstore.Guide, error)
	GuideWithItems(ctx context.Context, userID, guideID string) (*docstore.Guide, error)
	ReplaceUnkeptItems(ctx context.Context, userID, guideID string, keepIDs []string, newItems []guide.Item, seed int64) (*docstore.Guide, error)
	UpdateGuideItem(ctx context.Context, userID, guideID, itemID string, upd docstore.GuideItemUpdate) (*docstore.GuideItem, error)
	DeleteGuideItem(ctx context.Context, userID, guideID, itemID string) error
	MarkItemWatched(ctx context.Context, userID, guideID, itemID string) (*docstore.GuideItem, error)
	UnmarkItemWatched(ctx context.Context, userID, guideID, itemID string) (*docstore.GuideItem, error)
	SwapTitle(ctx context.Context, userID, titleID, region string) (*docstore.SwapInfo, error)
	GuideLookups(ctx context.Context, items []docstore.GuideItem) (map[string]docstore.TitleLookup, map[int64]docstore.ProviderRow, error)
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

func (d Deps) buildInput(ctx context.Context, u *docstore.User, start time.Time, days int, seed int64, keep []guide.Item) (guide.Input, error) {
	windows, err := prefs.Windows(u.SchedulePrefs)
	if err != nil {
		return guide.Input{}, err
	}
	titles, err := d.Guides.GuideInputTitles(ctx, u.ID, u.Region)
	if err != nil {
		return guide.Input{}, err
	}
	if d.refreshRotation(ctx, titles, u.Region) {
		if titles, err = d.Guides.GuideInputTitles(ctx, u.ID, u.Region); err != nil {
			return guide.Input{}, err
		}
	}
	return guide.Input{Seed: seed, Days: guideDays(start, days, windows), Titles: titles, Keep: keep}, nil
}

// refreshRotation runs every rotation title through ingest's TTL refresh
// before scheduling, so the guide never schedules from provider data the
// title page would already have refreshed (e.g. docs ingested before the
// rent/buy fallback). Fresh titles cost one doc read each. Failures are
// ignored: ingest serves cached rows on upstream errors, and a store error
// here just means generating from what is already stored. Reports whether
// anything ran, i.e. whether titles must be re-hydrated.
func (d Deps) refreshRotation(ctx context.Context, titles []guide.Title, region string) bool {
	if d.Ingest == nil || len(titles) == 0 {
		return false
	}
	var wg sync.WaitGroup
	for _, t := range titles {
		kind, rawID, ok := strings.Cut(t.ID, "-")
		tmdbID, err := strconv.ParseInt(rawID, 10, 64)
		if !ok || err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.Ingest.EnsureTitle(ctx, kind, tmdbID, region)
		}()
	}
	wg.Wait()
	return true
}

// guideResponse decorates a guide with rendering dictionaries so the web
// can resolve item ids without extra round trips (#18).
type guideResponse struct {
	*docstore.Guide
	Titles    map[string]docstore.TitleLookup `json:"titles"`
	Providers map[int64]docstore.ProviderRow  `json:"providers"`
	// Windows lists the enabled viewing window of each guide day, from the
	// user's current schedule prefs (disabled days are absent). The web
	// sizes the calendar's time axis from these rather than from items.
	Windows []dayWindow `json:"windows"`
}

type dayWindow struct {
	Date     string `json:"date"`
	StartMin int    `json:"start_min"`
	EndMin   int    `json:"end_min"`
}

// guideWindows maps a guide's date range onto the user's enabled windows.
// Unparseable dates or prefs yield an empty list rather than failing the
// response: the windows only size the calendar.
func guideWindows(u *docstore.User, g *docstore.Guide) []dayWindow {
	out := []dayWindow{}
	if u == nil {
		return out
	}
	start, err1 := time.Parse(dateFmt, g.StartDate)
	end, err2 := time.Parse(dateFmt, g.EndDate)
	windows, err3 := prefs.Windows(u.SchedulePrefs)
	if err1 != nil || err2 != nil || err3 != nil || end.Before(start) {
		return out
	}
	n := int(end.Sub(start).Hours()/24) + 1
	for _, day := range guideDays(start, n, windows) {
		if day.Window != (guide.Window{}) {
			out = append(out, dayWindow{Date: day.Date, StartMin: day.Window.StartMin, EndMin: day.Window.EndMin})
		}
	}
	return out
}

// writeGuide responds with the guide plus its sidecars at the given status code.
// It fetches the title/provider lookups first; on failure, writes a 500 error and returns.
// Sets Content-Type and status code before encoding to ensure they ship with the response.
func writeGuide(w http.ResponseWriter, r *http.Request, d Deps, status int, g *docstore.Guide) {
	titles, provs, err := d.Guides.GuideLookups(r.Context(), g.Items)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(guideResponse{Guide: g, Titles: titles, Providers: provs,
		Windows: guideWindows(userFrom(r.Context()), g)})
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
		writeGuide(w, r, d, http.StatusCreated, g)
	}
}

func handleCurrentGuide(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		g, err := d.Guides.CurrentGuide(r.Context(), u.ID, userToday(u, d.Now()))
		switch {
		case errors.Is(err, docstore.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		writeGuide(w, r, d, http.StatusOK, g)
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
		case errors.Is(err, docstore.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case err != nil:
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}

		// "Today" is the user's local date: in a US evening UTC is already
		// tomorrow, and using it would freeze tonight as the past.
		today := userToday(u, d.Now())
		var keepIDs []string
		var keep []guide.Item
		for _, it := range g.Items {
			if it.Pinned || it.Edited || it.Watched || it.Date < today {
				keepIDs = append(keepIDs, it.ID)
				keep = append(keep, guide.Item{Date: it.Date, StartMin: it.StartMin, EndMin: it.EndMin,
					TitleID: it.TitleID, Season: it.Season, Episode: it.Episode, Provider: it.ProviderID,
					Offer: it.Offer, IsPlan: it.IsPlan, Pinned: it.Pinned, Edited: it.Edited, Watched: it.Watched})
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

		// Regenerate is a fresh reshuffle, not a replay: it mints a new
		// seed every call rather than reusing g.Seed, so future unpinned
		// items are free to land differently each time. Pinned/edited/
		// watched/past-dated items are keeps regardless (see the loop
		// above) — they bind to their current date/time/title no matter
		// what the new seed does with everything else.
		seed := d.Now().UnixNano()
		in, err := d.buildInput(r.Context(), u, start, days, seed, keep)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		// The past is immutable: keeps (pinned/edited/watched/past-dated
		// items) echo through via `keep` above, but no NEW items or
		// alternates may land before today, so disable those days for
		// generation regardless of what the user's schedule windows say.
		for i := range in.Days {
			if in.Days[i].Date < today {
				in.Days[i].Window = guide.Window{}
			}
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

		refreshed, err := d.Guides.ReplaceUnkeptItems(r.Context(), u.ID, gid, keepIDs, newItems, seed)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal")
			return
		}
		writeGuide(w, r, d, http.StatusOK, refreshed)
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
			TitleID  *string `json:"title_id"`
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
		upd := docstore.GuideItemUpdate{Date: body.Date, StartMin: body.StartMin, Pinned: body.Pinned,
			SetEdited: body.Date != nil || body.StartMin != nil || body.TitleID != nil,
			Today:     userToday(u, d.Now())}

		if body.TitleID != nil {
			info, err := d.Guides.SwapTitle(r.Context(), u.ID, *body.TitleID, u.Region)
			switch {
			case errors.Is(err, docstore.ErrTitleNotFound):
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
			// Recompute provider_id in the store instead of carrying over
			// the swapped-out title's stale provider: keep the item's
			// current provider when the new title streams there too,
			// else the store falls back to the lowest id.
			upd.SwapProviders = info.Providers
			upd.SwapOffer = info.Offer
		}

		it, err := d.Guides.UpdateGuideItem(r.Context(), u.ID, gid, itemID, upd)
		switch {
		case errors.Is(err, docstore.ErrGuideNotFound):
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		case errors.Is(err, docstore.ErrPastMove):
			writeJSONError(w, http.StatusUnprocessableEntity, "cannot move past slot")
			return
		case errors.Is(err, docstore.ErrPastTarget):
			writeJSONError(w, http.StatusUnprocessableEntity, "cannot move into the past")
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
		case errors.Is(err, docstore.ErrGuideNotFound):
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
		case errors.Is(err, docstore.ErrGuideNotFound):
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

func handleUnwatchItem(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gid, ok1 := pathID(r, "id")
		itemID, ok2 := pathID(r, "itemID")
		if !ok1 || !ok2 {
			writeJSONError(w, http.StatusNotFound, "not_found")
			return
		}
		u := userFrom(r.Context())
		it, err := d.Guides.UnmarkItemWatched(r.Context(), u.ID, gid, itemID)
		switch {
		case errors.Is(err, docstore.ErrGuideNotFound):
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

// pathID returns a chi URL param as a document ID. chi matches on the raw
// path, so the param is unescaped first: an encoded slash (%2F) must not slip
// past the check. ok is false for anything that can't be a Firestore
// document ID; callers answer 404, as they did for non-numeric IDs.
func pathID(r *http.Request, name string) (string, bool) {
	id, err := url.PathUnescape(chi.URLParam(r, name))
	if err != nil || !docstore.ValidID(id) {
		return "", false
	}
	return id, true
}
