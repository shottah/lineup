package httpserver

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shottah/lineup/api/internal/store"
)

type Deps struct {
	Store *store.Store
}

func New(d Deps) *http.Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &http.Server{Addr: ":" + port, Handler: r}
}
