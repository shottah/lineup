package main

import (
	"context"
	"log"

	"github.com/shottah/lineup/api/internal/config"
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

	srv := httpserver.New(httpserver.Deps{Store: st})
	log.Fatal(srv.ListenAndServe())
}
