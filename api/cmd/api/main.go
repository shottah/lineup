package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	firebase "firebase.google.com/go/v4"

	"github.com/shottah/lineup/api/internal/config"
	"github.com/shottah/lineup/api/internal/docstore"
	"github.com/shottah/lineup/api/internal/fbauth"
	"github.com/shottah/lineup/api/internal/httpserver"
	"github.com/shottah/lineup/api/internal/ingest"
	"github.com/shottah/lineup/api/internal/tmdb"
	"github.com/shottah/lineup/api/internal/tvmaze"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run wires the server and blocks until it fails or a SIGINT/SIGTERM drains
// it; returning (rather than log.Fatal) lets the deferred Firestore Close run.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One app backs both Auth and Firestore. The SDKs honour
	// FIREBASE_AUTH_EMULATOR_HOST and FIRESTORE_EMULATOR_HOST on their own.
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: cfg.FirebaseProjectID})
	if err != nil {
		return err
	}
	fs, err := app.Firestore(ctx)
	if err != nil {
		return err
	}
	defer fs.Close()
	st := docstore.New(fs)

	verifier, err := fbauth.New(ctx, app)
	if err != nil {
		return err
	}

	deps := httpserver.Deps{Users: st, Verifier: verifier, Entries: st, Guides: st}
	// Search/ingestion need a TMDB read token; without one the API boots
	// with those routes absent (404s) rather than half-working.
	if cfg.TMDBReadToken != "" {
		tm := tmdb.New(cfg.TMDBReadToken)
		deps.Search = tm
		deps.Ingest = &ingest.Service{TMDB: tm, TVMaze: tvmaze.New(), Store: st}
		deps.Titles = st
	}
	srv := httpserver.New(deps)

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()

	select {
	case err := <-errc:
		return err // listen failure, e.g. port in use
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
