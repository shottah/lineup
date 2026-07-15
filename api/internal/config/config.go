// Package config loads process configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config holds process configuration. DatabaseURL and FirebaseProjectID are
// required; the rest are optional until the features that consume them land
// (TMDBReadToken: consumed by ingestion (#11); Port: consumed directly by
// httpserver.New, which already
// defaults it to 8080 when unset).
type Config struct {
	DatabaseURL       string
	TMDBReadToken     string
	FirebaseProjectID string
	Port              string
}

// ErrMissingDatabaseURL is returned by Load when DATABASE_URL is unset or
// empty.
var ErrMissingDatabaseURL = errors.New("config: DATABASE_URL is required")

// ErrMissingFirebaseProjectID is returned by Load when FIREBASE_PROJECT_ID
// is unset or empty.
var ErrMissingFirebaseProjectID = errors.New("config: FIREBASE_PROJECT_ID is required")

// Load reads configuration from environment variables. It errors when
// DATABASE_URL or FIREBASE_PROJECT_ID is missing; every other field defaults
// to "" when unset.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		TMDBReadToken:     os.Getenv("TMDB_READ_TOKEN"),
		FirebaseProjectID: os.Getenv("FIREBASE_PROJECT_ID"),
		Port:              os.Getenv("PORT"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	if cfg.FirebaseProjectID == "" {
		return Config{}, ErrMissingFirebaseProjectID
	}
	return cfg, nil
}
