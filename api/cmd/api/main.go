package main

import (
	"context"
	"log"

	"github.com/shottah/lineup/api/internal/config"
	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/httpserver"
	"github.com/shottah/lineup/api/internal/store"
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

	srv := httpserver.New(httpserver.Deps{Store: st, Users: st, Verifier: verifier})
	log.Fatal(srv.ListenAndServe())
}
