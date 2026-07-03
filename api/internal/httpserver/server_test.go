package httpserver

import (
	"net/http/httptest"
	"testing"
)

func TestHealthz(t *testing.T) {
	srv := New(Deps{})
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("got %d want 200", rec.Code)
	}
}
