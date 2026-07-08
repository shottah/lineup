// api/internal/httpserver/me_test.go
package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shottah/lineup/api/internal/fbauth"
)

func testServer(t *testing.T) (http.Handler, *fakeUsers) {
	t.Helper()
	users := newFakeUsers()
	verifier := &fbauth.Fake{Tokens: map[string]fbauth.Identity{
		"tok-1": {UID: "uid-1", Email: "one@example.com", DisplayName: "One"},
	}}
	srv := New(Deps{Users: users, Verifier: verifier})
	return srv.Handler, users
}

func do(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const validPrefs = `{"windows":{"mon":{"enabled":true,"start":"18:00","end":"22:00"},"tue":{"enabled":true,"start":"19:00","end":"23:00"},"wed":{"enabled":true,"start":"19:00","end":"23:00"},"thu":{"enabled":true,"start":"19:00","end":"23:00"},"fri":{"enabled":true,"start":"19:00","end":"23:00"},"sat":{"enabled":true,"start":"19:00","end":"23:00"},"sun":{"enabled":true,"start":"19:00","end":"23:00"}}}`

func TestGetMe(t *testing.T) {
	h, _ := testServer(t)

	if rec := do(t, h, http.MethodGet, "/v1/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET /v1/me = %d, want 401", rec.Code)
	}

	rec := do(t, h, http.MethodGet, "/v1/me", "tok-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/me = %d, body %s", rec.Code, rec.Body.String())
	}
	var u struct {
		ID            int64           `json:"id"`
		Email         string          `json:"email"`
		Region        string          `json:"region"`
		SchedulePrefs json.RawMessage `json:"schedule_prefs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil {
		t.Fatalf("GET /v1/me body not JSON: %v", err)
	}
	if u.Email != "one@example.com" || u.Region != "US" {
		t.Fatalf("GET /v1/me = %+v", u)
	}
	if !strings.Contains(string(u.SchedulePrefs), `"19:00"`) {
		t.Fatalf("first sign-in did not get default prefs: %s", u.SchedulePrefs)
	}
	if strings.Contains(rec.Body.String(), "uid-1") {
		t.Fatalf("response leaks firebase uid: %s", rec.Body.String())
	}
}

func TestPatchMe(t *testing.T) {
	h, _ := testServer(t)

	cases := []struct {
		name string
		body string
		want int
	}{
		{"update region", `{"region":"GB"}`, http.StatusOK},
		{"update prefs", `{"schedule_prefs":` + validPrefs + `}`, http.StatusOK},
		{"update both", `{"region":"CA","schedule_prefs":` + validPrefs + `}`, http.StatusOK},
		{"invalid prefs shape", `{"schedule_prefs":{"windows":{"mon":{"enabled":true,"start":"25:00","end":"26:00"}}}}`, http.StatusUnprocessableEntity},
		{"empty region", `{"region":""}`, http.StatusUnprocessableEntity},
		{"empty body object", `{}`, http.StatusUnprocessableEntity},
		{"malformed json", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, "/v1/me", "tok-1", tc.body)
			if rec.Code != tc.want {
				t.Fatalf("PATCH /v1/me %s = %d, want %d (body %s)", tc.body, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	rec := do(t, h, http.MethodGet, "/v1/me", "tok-1", "")
	if !strings.Contains(rec.Body.String(), `"CA"`) || !strings.Contains(rec.Body.String(), `"18:00"`) {
		t.Fatalf("PATCH results not persisted: %s", rec.Body.String())
	}
}
