package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/store"
)

// fakeUsers implements UserStore in memory.
type fakeUsers struct {
	byUID     map[string]*store.User
	nextID    int64
	upsertErr error
	updateErr error
}

func newFakeUsers() *fakeUsers {
	return &fakeUsers{byUID: map[string]*store.User{}, nextID: 1}
}

func (f *fakeUsers) UpsertUserByFirebaseUID(_ context.Context, uid, email, displayName string, defaultPrefs json.RawMessage) (*store.User, error) {
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	if u, ok := f.byUID[uid]; ok {
		u.Email = email
		if displayName != "" {
			u.DisplayName = displayName
		}
		cp := *u
		return &cp, nil
	}
	u := &store.User{ID: f.nextID, FirebaseUID: uid, Email: email, DisplayName: displayName, Region: "US", SchedulePrefs: defaultPrefs}
	f.nextID++
	f.byUID[uid] = u
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) UpdateUserPrefs(_ context.Context, userID int64, region *string, prefsJSON json.RawMessage) (*store.User, error) {
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	for _, u := range f.byUID {
		if u.ID == userID {
			if region != nil {
				u.Region = *region
			}
			if prefsJSON != nil {
				u.SchedulePrefs = prefsJSON
			}
			cp := *u
			return &cp, nil
		}
	}
	return nil, errors.New("not found")
}

func authedHandler(t *testing.T) (http.Handler, *fakeUsers) {
	t.Helper()
	users := newFakeUsers()
	verifier := &fbauth.Fake{Tokens: map[string]fbauth.Identity{
		"tok-1": {UID: "uid-1", Email: "one@example.com", DisplayName: "One"},
	}}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u == nil {
			t.Fatal("userFrom returned nil inside authed handler")
		}
		w.WriteHeader(http.StatusOK)
	})
	return requireAuth(verifier, users)(inner), users
}

func TestRequireAuth(t *testing.T) {
	h, users := authedHandler(t)

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"missing header", "", http.StatusUnauthorized},
		{"not bearer", "Basic abc", http.StatusUnauthorized},
		{"unknown token", "Bearer nope", http.StatusUnauthorized},
		{"valid token", "Bearer tok-1", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusUnauthorized {
				var body map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "unauthorized" {
					t.Fatalf("401 body = %s, want {\"error\":\"unauthorized\"}", rec.Body.String())
				}
			}
		})
	}

	if users.byUID["uid-1"] == nil {
		t.Fatal("valid request did not upsert the user")
	}
}

func TestRequireAuthUpsertFailure(t *testing.T) {
	users := newFakeUsers()
	users.upsertErr = errors.New("db down")
	verifier := &fbauth.Fake{Tokens: map[string]fbauth.Identity{"tok-1": {UID: "u", Email: "e@x.com"}}}
	h := requireAuth(verifier, users)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("inner handler must not run when upsert fails")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
