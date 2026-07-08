package httpserver

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/store"
)

type Deps struct {
	Store    *store.Store
	Users    UserStore
	Verifier fbauth.TokenVerifier
	Entries  EntryStore
}

func New(d Deps) *http.Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	if d.Verifier != nil && d.Users != nil {
		r.Route("/v1", func(v1 chi.Router) {
			v1.Use(requireAuth(d.Verifier, d.Users))
			v1.Get("/me", handleGetMe)
			v1.Patch("/me", handlePatchMe(d.Users))
			if d.Entries != nil {
				v1.Patch("/titles/{id}/entry", handlePatchEntry(d.Entries))
				v1.Get("/me/shelves/{shelf}", handleGetShelf(d.Entries))
			}
		})
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &http.Server{Addr: ":" + port, Handler: r}
}
