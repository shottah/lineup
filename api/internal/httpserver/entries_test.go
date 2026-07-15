package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/store"
)

// fakeEntries implements EntryStore in memory for one user, mirroring the
// SQL semantics (COALESCE partials, watched stamping, count exclusion).
type fakeEntries struct {
	titles  map[int64]string // known title ids -> name
	entries map[int64]*store.Entry
}

func newFakeEntries(titleIDs ...int64) *fakeEntries {
	f := &fakeEntries{titles: map[int64]string{}, entries: map[int64]*store.Entry{}}
	for _, id := range titleIDs {
		f.titles[id] = fmt.Sprintf("Title %d", id)
	}
	return f
}

func (f *fakeEntries) UpsertEntry(_ context.Context, _ int64, titleID int64, u store.EntryUpdate) (*store.Entry, error) {
	if _, ok := f.titles[titleID]; !ok {
		return nil, store.ErrTitleNotFound
	}
	e, ok := f.entries[titleID]
	if !ok {
		// Fake tmdb ids are derived deterministically so handler tests can
		// assert the field flows through (real ids come from the join).
		e = &store.Entry{TitleID: titleID, TMDBID: titleID + 100000, Kind: "series", Name: f.titles[titleID], Status: "none",
			Pointer: store.Pointer{Season: 1, Episode: 1}, AddedAt: time.Now()}
		f.entries[titleID] = e
	}
	if u.Status != nil {
		e.Status = *u.Status
		if *u.Status == "watched" {
			now := time.Now()
			e.WatchedAt = &now
		}
	}
	if u.ClearRating {
		e.Rating = nil
	} else if u.Rating != nil {
		e.Rating = u.Rating
	}
	if u.Favorite != nil {
		e.Favorite = *u.Favorite
	}
	if u.Pointer != nil {
		e.Pointer = *u.Pointer
	}
	cp := *e
	return &cp, nil
}

func (f *fakeEntries) CountRotation(_ context.Context, _ int64, excludeTitleID int64) (int, error) {
	n := 0
	for id, e := range f.entries {
		if e.Status == "rotation" && id != excludeTitleID {
			n++
		}
	}
	return n, nil
}

func (f *fakeEntries) Shelf(_ context.Context, _ int64, shelf string) ([]store.Entry, error) {
	out := []store.Entry{}
	for _, e := range f.entries {
		switch shelf {
		case "watchlist", "rotation", "watched":
			if e.Status == shelf {
				out = append(out, *e)
			}
		case "favorites":
			if e.Favorite {
				out = append(out, *e)
			}
		case "ratings":
			if e.Rating != nil {
				out = append(out, *e)
			}
		}
	}
	return out, nil
}

func entriesServer(t *testing.T, titleIDs ...int64) (http.Handler, *fakeEntries) {
	t.Helper()
	fe := newFakeEntries(titleIDs...)
	users := newFakeUsers()
	verifier := fakeVerifierWithTok1()
	srv := New(Deps{Users: users, Verifier: verifier, Entries: fe})
	return srv.Handler, fe
}

func TestPatchEntryValidation(t *testing.T) {
	h, _ := entriesServer(t, 1)
	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{"unknown title", "/v1/titles/99/entry", `{"favorite":true}`, http.StatusNotFound},
		{"non-numeric id", "/v1/titles/abc/entry", `{"favorite":true}`, http.StatusNotFound},
		{"malformed json", "/v1/titles/1/entry", `{`, http.StatusBadRequest},
		{"empty body object", "/v1/titles/1/entry", `{}`, http.StatusUnprocessableEntity},
		{"bad status", "/v1/titles/1/entry", `{"status":"queued"}`, http.StatusUnprocessableEntity},
		{"rating too low", "/v1/titles/1/entry", `{"rating":0.4}`, http.StatusUnprocessableEntity},
		{"rating not half step", "/v1/titles/1/entry", `{"rating":3.7}`, http.StatusUnprocessableEntity},
		{"rating too high", "/v1/titles/1/entry", `{"rating":5.5}`, http.StatusUnprocessableEntity},
		{"rating wrong type", "/v1/titles/1/entry", `{"rating":"five"}`, http.StatusUnprocessableEntity},
		{"pointer zero season", "/v1/titles/1/entry", `{"pointer":{"season":0,"episode":1}}`, http.StatusUnprocessableEntity},
		{"pointer zero episode", "/v1/titles/1/entry", `{"pointer":{"season":1,"episode":0}}`, http.StatusUnprocessableEntity},
		{"valid rating", "/v1/titles/1/entry", `{"rating":4.5}`, http.StatusOK},
		{"valid status", "/v1/titles/1/entry", `{"status":"watchlist"}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, tc.path, "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d, want %d (body %s)", tc.name, tc.body, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestPatchEntryRotationCap(t *testing.T) {
	ids := make([]int64, 9)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	h, fe := entriesServer(t, ids...)

	for i := int64(1); i <= 8; i++ {
		rec := do(t, h, http.MethodPatch, fmt.Sprintf("/v1/titles/%d/entry", i), "tok-1", `{"status":"rotation"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("adding %d = %d (body %s)", i, rec.Code, rec.Body.String())
		}
	}
	// 9th title: cap hit.
	rec := do(t, h, http.MethodPatch, "/v1/titles/9/entry", "tok-1", `{"status":"rotation"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "rotation_full") {
		t.Fatalf("9th rotation = %d body %s, want 409 rotation_full", rec.Code, rec.Body.String())
	}
	// Re-setting an existing rotation title at cap: idempotent, 200.
	rec = do(t, h, http.MethodPatch, "/v1/titles/3/entry", "tok-1", `{"status":"rotation"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent re-set = %d, want 200", rec.Code)
	}
	// Other fields untouched by cap: rating a 9th title is fine.
	rec = do(t, h, http.MethodPatch, "/v1/titles/9/entry", "tok-1", `{"rating":5.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rating at cap = %d, want 200", rec.Code)
	}
	if fe.entries[9] != nil && fe.entries[9].Status == "rotation" {
		t.Fatal("9th title must not have entered rotation")
	}
}

func TestPatchEntryWatchedAndClear(t *testing.T) {
	h, fe := entriesServer(t, 1)

	rec := do(t, h, http.MethodPatch, "/v1/titles/1/entry", "tok-1", `{"status":"watched","rating":4.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("watched = %d", rec.Code)
	}
	var got store.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body: %v", err)
	}
	if got.WatchedAt == nil || got.Rating == nil || *got.Rating != 4.0 {
		t.Fatalf("watched entry = %+v, want watched_at + rating set", got)
	}

	rec = do(t, h, http.MethodPatch, "/v1/titles/1/entry", "tok-1", `{"rating":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear rating = %d", rec.Code)
	}
	if fe.entries[1].Rating != nil {
		t.Fatal("rating not cleared")
	}
	if fe.entries[1].WatchedAt == nil {
		t.Fatal("watched_at lost on unrelated update")
	}
}

func TestGetShelf(t *testing.T) {
	h, _ := entriesServer(t, 1, 2, 3)
	seed := func(id int64, body string) {
		if rec := do(t, h, http.MethodPatch, fmt.Sprintf("/v1/titles/%d/entry", id), "tok-1", body); rec.Code != http.StatusOK {
			t.Fatalf("seed %d: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	seed(1, `{"status":"watchlist"}`)
	seed(2, `{"status":"rotation","favorite":true}`)
	seed(3, `{"status":"watched","rating":4.5}`)

	cases := []struct {
		shelf string
		want  []int64
	}{
		{"watchlist", []int64{1}},
		{"rotation", []int64{2}},
		{"watched", []int64{3}},
		{"favorites", []int64{2}},
		{"ratings", []int64{3}},
	}
	for _, tc := range cases {
		t.Run(tc.shelf, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/v1/me/shelves/"+tc.shelf, "tok-1", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("shelf %s = %d", tc.shelf, rec.Code)
			}
			var body struct {
				Entries []store.Entry `json:"entries"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body: %v", err)
			}
			ids := []int64{}
			for _, e := range body.Entries {
				ids = append(ids, e.TitleID)
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("shelf %s ids = %v, want %v", tc.shelf, ids, tc.want)
			}
			if len(body.Entries) > 0 && body.Entries[0].TMDBID != body.Entries[0].TitleID+100000 {
				t.Fatalf("entry tmdb_id = %d, want title_id+100000 (fake derivation)", body.Entries[0].TMDBID)
			}
		})
	}

	if rec := do(t, h, http.MethodGet, "/v1/me/shelves/bogus", "tok-1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bogus shelf = %d, want 404", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/v1/me/shelves/watchlist", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated shelf = %d, want 401", rec.Code)
	}

	// Empty shelf serializes as [] not null.
	h2, _ := entriesServer(t, 1)
	rec := do(t, h2, http.MethodGet, "/v1/me/shelves/watchlist", "tok-1", "")
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("empty shelf body = %s, want entries:[]", rec.Body.String())
	}
}
