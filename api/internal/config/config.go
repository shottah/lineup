// Package config loads process configuration from the environment.
package config

import (
	"errors"
	"os"
)

// Config holds process configuration. DatabaseURL is required; the rest are
// optional until the features that consume them land (TMDBKey: task 9,
// FirebaseProjectID: task 8, Port: consumed directly by httpserver.New,
// which already defaults it to 8080 when unset).
type Config struct {
	DatabaseURL       string
	TMDBKey           string
	FirebaseProjectID string
	Port              string
}

// ErrMissingDatabaseURL is returned by Load when DATABASE_URL is unset or
// empty.
var ErrMissingDatabaseURL = errors.New("config: DATABASE_URL is required")

// Load reads configuration from environment variables. It errors only when
// DATABASE_URL is missing; every other field defaults to "" when unset.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		TMDBKey:           os.Getenv("TMDB_API_KEY"),
		FirebaseProjectID: os.Getenv("FIREBASE_PROJECT_ID"),
		Port:              os.Getenv("PORT"),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, ErrMissingDatabaseURL
	}
	return cfg, nil
}
