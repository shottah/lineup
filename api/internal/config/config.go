// Package config loads process configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config holds process configuration. FirebaseProjectID is required; the
// rest are optional (TMDBReadToken: without it search/ingestion routes are
// absent; Port: consumed directly by httpserver.New, which already defaults
// it to 8080 when unset).
type Config struct {
	TMDBReadToken     string
	FirebaseProjectID string
	Port              string
}

// ErrMissingFirebaseProjectID is returned by Load when FIREBASE_PROJECT_ID
// is unset or empty.
var ErrMissingFirebaseProjectID = errors.New("config: FIREBASE_PROJECT_ID is required")

// Load reads configuration from environment variables. It errors when
// FIREBASE_PROJECT_ID is missing; every other field defaults to "" when
// unset.
func Load() (Config, error) {
	cfg := Config{
		TMDBReadToken:     os.Getenv("TMDB_READ_TOKEN"),
		FirebaseProjectID: os.Getenv("FIREBASE_PROJECT_ID"),
		Port:              os.Getenv("PORT"),
	}
	if cfg.FirebaseProjectID == "" {
		return Config{}, ErrMissingFirebaseProjectID
	}
	return cfg, nil
}
