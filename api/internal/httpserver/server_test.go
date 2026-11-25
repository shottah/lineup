package httpserver

import (
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	srv := New(Deps{})
	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("got %d want 200", rec.Code)
	}
}
