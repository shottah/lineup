package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/docstore"
)

// fakeEntries implements EntryStore in memory for one user, mirroring the
// store semantics (partial updates, watched stamping, count exclusion).
type fakeEntries struct {
	titles  map[string]int64 // known title ids -> tmdb id
	entries map[string]*docstore.Entry
}

func newFakeEntries(titleIDs ...string) *fakeEntries {
	f := &fakeEntries{titles: map[string]int64{}, entries: map[string]*docstore.Entry{}}
	for i, id := range titleIDs {
		f.titles[id] = int64(100000 + i)
	}
	return f
}

func (f *fakeEntries) UpsertEntry(_ context.Context, _ string, titleID string, u docstore.EntryUpdate) (*docstore.Entry, error) {
	if _, ok := f.titles[titleID]; !ok {
		return nil, docstore.ErrTitleNotFound
	}
	e, ok := f.entries[titleID]
	if !ok {
		// Fake tmdb ids are assigned per known title so handler tests can
		// assert the field flows through (real ids come from the title doc).
		e = &docstore.Entry{TitleID: titleID, TMDBID: f.titles[titleID], Kind: "series", Name: "Title " + titleID, Status: "none",
			Pointer: docstore.Pointer{Season: 1, Episode: 1}, AddedAt: time.Now()}
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

func (f *fakeEntries) CountRotation(_ context.Context, _ string, excludeTitleID string) (int, error) {
	n := 0
	for id, e := range f.entries {
		if e.Status == "rotation" && id != excludeTitleID {
			n++
		}
	}
	return n, nil
}

func (f *fakeEntries) Shelf(_ context.Context, _ string, shelf string) ([]docstore.Entry, error) {
	out := []docstore.Entry{}
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

func entriesServer(t *testing.T, titleIDs ...string) (http.Handler, *fakeEntries) {
	t.Helper()
	fe := newFakeEntries(titleIDs...)
	users := newFakeUsers()
	verifier := fakeVerifierWithTok1()
	srv := New(Deps{Users: users, Verifier: verifier, Entries: fe})
	return srv.Handler, fe
}

func TestPatchEntryValidation(t *testing.T) {
	h, _ := entriesServer(t, "series-1")
	cases := []struct {
		name string
		path string
		body string
		want int
	}{
		{"unknown title", "/v1/titles/series-99/entry", `{"favorite":true}`, http.StatusNotFound},
		{"reserved id", "/v1/titles/__x__/entry", `{"favorite":true}`, http.StatusNotFound},
		{"malformed json", "/v1/titles/series-1/entry", `{`, http.StatusBadRequest},
		{"empty body object", "/v1/titles/series-1/entry", `{}`, http.StatusUnprocessableEntity},
		{"bad status", "/v1/titles/series-1/entry", `{"status":"queued"}`, http.StatusUnprocessableEntity},
		{"rating too low", "/v1/titles/series-1/entry", `{"rating":0.4}`, http.StatusUnprocessableEntity},
		{"rating not half step", "/v1/titles/series-1/entry", `{"rating":3.7}`, http.StatusUnprocessableEntity},
		{"rating too high", "/v1/titles/series-1/entry", `{"rating":5.5}`, http.StatusUnprocessableEntity},
		{"rating wrong type", "/v1/titles/series-1/entry", `{"rating":"five"}`, http.StatusUnprocessableEntity},
		{"pointer zero season", "/v1/titles/series-1/entry", `{"pointer":{"season":0,"episode":1}}`, http.StatusUnprocessableEntity},
		{"pointer zero episode", "/v1/titles/series-1/entry", `{"pointer":{"season":1,"episode":0}}`, http.StatusUnprocessableEntity},
		{"valid rating", "/v1/titles/series-1/entry", `{"rating":4.5}`, http.StatusOK},
		{"valid status", "/v1/titles/series-1/entry", `{"status":"watchlist"}`, http.StatusOK},
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
	ids := make([]string, 11)
	for i := range ids {
		ids[i] = fmt.Sprintf("series-%d", i+1)
	}
	h, fe := entriesServer(t, ids...)

	for i := 1; i <= 10; i++ {
		rec := do(t, h, http.MethodPatch, fmt.Sprintf("/v1/titles/series-%d/entry", i), "tok-1", `{"status":"rotation"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("adding %d = %d (body %s)", i, rec.Code, rec.Body.String())
		}
	}
	// 11th title: cap hit.
	rec := do(t, h, http.MethodPatch, "/v1/titles/series-11/entry", "tok-1", `{"status":"rotation"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "rotation_full") {
		t.Fatalf("11th rotation = %d body %s, want 409 rotation_full", rec.Code, rec.Body.String())
	}
	// Re-setting an existing rotation title at cap: idempotent, 200.
	rec = do(t, h, http.MethodPatch, "/v1/titles/series-3/entry", "tok-1", `{"status":"rotation"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("idempotent re-set = %d, want 200", rec.Code)
	}
	// Other fields untouched by cap: rating an over-cap title is fine.
	rec = do(t, h, http.MethodPatch, "/v1/titles/series-11/entry", "tok-1", `{"rating":5.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rating at cap = %d, want 200", rec.Code)
	}
	if fe.entries["series-11"] != nil && fe.entries["series-11"].Status == "rotation" {
		t.Fatal("11th title must not have entered rotation")
	}
}

func TestPatchEntryWatchedAndClear(t *testing.T) {
	h, fe := entriesServer(t, "series-1")

	rec := do(t, h, http.MethodPatch, "/v1/titles/series-1/entry", "tok-1", `{"status":"watched","rating":4.0}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("watched = %d", rec.Code)
	}
	var got docstore.Entry
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body: %v", err)
	}
	if got.WatchedAt == nil || got.Rating == nil || *got.Rating != 4.0 {
		t.Fatalf("watched entry = %+v, want watched_at + rating set", got)
	}

	rec = do(t, h, http.MethodPatch, "/v1/titles/series-1/entry", "tok-1", `{"rating":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear rating = %d", rec.Code)
	}
	if fe.entries["series-1"].Rating != nil {
		t.Fatal("rating not cleared")
	}
	if fe.entries["series-1"].WatchedAt == nil {
		t.Fatal("watched_at lost on unrelated update")
	}
}

func TestGetShelf(t *testing.T) {
	h, fe := entriesServer(t, "series-1", "series-2", "series-3")
	seed := func(id string, body string) {
		if rec := do(t, h, http.MethodPatch, fmt.Sprintf("/v1/titles/%s/entry", id), "tok-1", body); rec.Code != http.StatusOK {
			t.Fatalf("seed %s: %d %s", id, rec.Code, rec.Body.String())
		}
	}
	seed("series-1", `{"status":"watchlist"}`)
	seed("series-2", `{"status":"rotation","favorite":true}`)
	seed("series-3", `{"status":"watched","rating":4.5}`)

	cases := []struct {
		shelf string
		want  []string
	}{
		{"watchlist", []string{"series-1"}},
		{"rotation", []string{"series-2"}},
		{"watched", []string{"series-3"}},
		{"favorites", []string{"series-2"}},
		{"ratings", []string{"series-3"}},
	}
	for _, tc := range cases {
		t.Run(tc.shelf, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/v1/me/shelves/"+tc.shelf, "tok-1", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("shelf %s = %d", tc.shelf, rec.Code)
			}
			var body struct {
				Entries []docstore.Entry `json:"entries"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body: %v", err)
			}
			ids := []string{}
			for _, e := range body.Entries {
				ids = append(ids, e.TitleID)
			}
			if fmt.Sprint(ids) != fmt.Sprint(tc.want) {
				t.Fatalf("shelf %s ids = %v, want %v", tc.shelf, ids, tc.want)
			}
			if len(body.Entries) > 0 && body.Entries[0].TMDBID != fe.titles[body.Entries[0].TitleID] {
				t.Fatalf("entry tmdb_id = %d, want %d (fake assignment)", body.Entries[0].TMDBID, fe.titles[body.Entries[0].TitleID])
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
	h2, _ := entriesServer(t, "series-1")
	rec := do(t, h2, http.MethodGet, "/v1/me/shelves/watchlist", "tok-1", "")
	if !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("empty shelf body = %s, want entries:[]", rec.Body.String())
	}
}
