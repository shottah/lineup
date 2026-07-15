package httpserver

import (
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/store"
)

type Deps struct {
	Store    *store.Store
	Users    UserStore
	Verifier fbauth.TokenVerifier
	Entries  EntryStore
	Guides   GuideStore
	Search   SearchClient
	Ingest   Ingester
	Titles   TitleReader
	Now      func() time.Time
}

func New(d Deps) *http.Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	r := chi.NewRouter()
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:3000",
			"http://localhost:3001",
			"https://lineup-app-ae6b.web.app",
			"https://lineup-app-ae6b.firebaseapp.com",
		},
		AllowedMethods: []string{"GET", "POST", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         300,
	}))
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
			if d.Search != nil {
				v1.Get("/search", handleSearch(d.Search))
			}
			if d.Ingest != nil && d.Titles != nil {
				v1.Get("/titles/{kind}/{tmdbID}", handleGetTitle(d.Ingest, d.Titles))
			}
			if d.Guides != nil {
				v1.Post("/guides", handleCreateGuide(d))
				v1.Get("/guides/current", handleCurrentGuide(d))
				v1.Route("/guides/{id}", func(gr chi.Router) {
					gr.Post("/regenerate", handleRegenerate(d))
					gr.Patch("/items/{itemID}", handlePatchItem(d))
					gr.Delete("/items/{itemID}", handleDeleteItem(d))
					gr.Post("/items/{itemID}/watched", handleWatchItem(d))
				})
			}
		})
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return &http.Server{Addr: ":" + port, Handler: r}
}
