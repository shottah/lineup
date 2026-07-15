package main

import (
	"context"
	"log"

	"github.com/shottah/lineup/api/internal/config"
	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/httpserver"
	"github.com/shottah/lineup/api/internal/ingest"
	"github.com/shottah/lineup/api/internal/store"
	"github.com/shottah/lineup/api/internal/tmdb"
	"github.com/shottah/lineup/api/internal/tvmaze"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}

	if err := store.Migrate(cfg.DatabaseURL); err != nil {
		log.Fatal(err)
	}

	st, err := store.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	verifier, err := fbauth.New(context.Background(), cfg.FirebaseProjectID)
	if err != nil {
		log.Fatal(err)
	}

	deps := httpserver.Deps{Store: st, Users: st, Verifier: verifier, Entries: st, Guides: st}
	// Search/ingestion need a TMDB read token; without one the API boots
	// with those routes absent (404s) rather than half-working.
	if cfg.TMDBReadToken != "" {
		tm := tmdb.New(cfg.TMDBReadToken)
		deps.Search = tm
		deps.Ingest = &ingest.Service{TMDB: tm, TVMaze: tvmaze.New(), Store: st}
		deps.Titles = st
	}
	srv := httpserver.New(deps)
	log.Fatal(srv.ListenAndServe())
}
