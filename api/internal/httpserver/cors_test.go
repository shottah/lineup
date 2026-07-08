package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shottah/lineup/api/internal/fbauth"
)

func TestCORSPreflight(t *testing.T) {
	srv := New(Deps{
		Verifier: &fbauth.Fake{Tokens: map[string]fbauth.Identity{}},
		Users:    newFakeUsers(),
	})
	cases := []struct {
		name      string
		origin    string
		wantAllow bool
	}{
		{"localhost 3001 allowed", "http://localhost:3001", true},
		{"localhost 3000 allowed", "http://localhost:3000", true},
		{"hosted web.app allowed", "https://lineup-app-ae6b.web.app", true},
		{"hosted firebaseapp.com allowed", "https://lineup-app-ae6b.firebaseapp.com", true},
		{"unknown origin rejected", "https://evil.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/v1/me", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", "GET")
			req.Header.Set("Access-Control-Request-Headers", "Authorization")
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			got := rec.Header().Get("Access-Control-Allow-Origin")
			if tc.wantAllow && got != tc.origin {
				t.Fatalf("ACAO = %q, want %q", got, tc.origin)
			}
			if !tc.wantAllow && got != "" {
				t.Fatalf("ACAO = %q for disallowed origin, want empty", got)
			}
			if tc.wantAllow && rec.Code != http.StatusOK {
				t.Fatalf("preflight status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestCORSActualRequestHeader(t *testing.T) {
	srv := New(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:3001")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3001" {
		t.Fatalf("ACAO on actual request = %q, want origin echoed", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz with Origin = %d, want 200", rec.Code)
	}
}
