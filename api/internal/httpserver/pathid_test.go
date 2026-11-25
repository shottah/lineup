package httpserver

import (
	"net/http"
	"testing"
	"time"

	"github.com/shottah/lineup/api/internal/docstore"
)

// TestPathIDRejectsInvalid: {id}/{itemID} values that can't be Firestore
// document IDs (reserved __x__ names, slashes — including %2F-encoded ones)
// 404 before reaching the store. The fakes deliberately know those IDs, so a
// missing guard would surface as a 2xx rather than a coincidental 404.
func TestPathIDRejectsInvalid(t *testing.T) {
	bad := []string{"__x__", "a%2Fb", "%2E%2E", "%2E"}
	known := []string{"__x__", "a/b", "a%2Fb", "..", ".", "ok"}

	fe := newFakeEntries(known...)
	fg := newFakeGuides()
	fg.titles = fixtureTitles()
	for _, gid := range known {
		items := []docstore.GuideItem{}
		for _, iid := range known {
			items = append(items, docstore.GuideItem{ID: iid, Date: "2026-01-09", StartMin: 1140, EndMin: 1170, TitleID: "series-1"})
		}
		fg.setGuide("uid-1", &docstore.Guide{ID: gid, StartDate: "2026-01-05", EndDate: "2026-01-11", Items: items})
	}
	h := New(Deps{Users: newFakeUsers(), Verifier: fakeVerifierWithTok1(), Entries: fe, Guides: fg,
		Now: func() time.Time { return fixedClock }}).Handler

	type call struct{ method, path, body string }
	itemCalls := func(gid, iid string) []call {
		return []call{
			{http.MethodPatch, "/v1/guides/" + gid + "/items/" + iid, `{"pinned":true}`},
			{http.MethodPost, "/v1/guides/" + gid + "/items/" + iid + "/watched", ""},
			{http.MethodDelete, "/v1/guides/" + gid + "/items/" + iid + "/watched", ""},
			{http.MethodDelete, "/v1/guides/" + gid + "/items/" + iid, ""},
		}
	}

	// Sanity: the same routes succeed for a valid ID, so the 404s below
	// come from the guard, not the fakes.
	if rec := do(t, h, http.MethodPatch, "/v1/titles/ok/entry", "tok-1", `{"favorite":true}`); rec.Code != http.StatusOK {
		t.Fatalf("valid title id = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	regen := func(gid string) call { return call{http.MethodPost, "/v1/guides/" + gid + "/regenerate", ""} }
	for _, c := range append([]call{regen("ok")}, itemCalls("ok", "ok")...) {
		if rec := do(t, h, c.method, c.path, "tok-1", c.body); rec.Code >= 300 {
			t.Fatalf("%s %s = %d, want 2xx (body %s)", c.method, c.path, rec.Code, rec.Body.String())
		}
	}

	for _, id := range bad {
		if rec := do(t, h, http.MethodPatch, "/v1/titles/"+id+"/entry", "tok-1", `{"favorite":true}`); rec.Code != http.StatusNotFound {
			t.Errorf("PATCH /v1/titles/%s/entry = %d, want 404", id, rec.Code)
		}
		cs := []call{regen(id)}
		cs = append(cs, itemCalls(id, "ok")...) // bad guide ID
		cs = append(cs, itemCalls("ok", id)...) // bad item ID
		for _, c := range cs {
			if rec := do(t, h, c.method, c.path, "tok-1", c.body); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s = %d, want 404 (body %s)", c.method, c.path, rec.Code, rec.Body.String())
			}
		}
	}
}
