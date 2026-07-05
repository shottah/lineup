package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pingTimeout bounds the startup connectivity check performed by New. It is
// intentionally short: New is called once at boot, and a slow/unreachable
// database should fail fast rather than hang the process.
const pingTimeout = 5 * time.Second

// Store wraps the application's Postgres connection pool. Later tasks add
// query methods as functions on *Store (e.g. in users.go, titles.go); this
// file only owns the pool lifecycle.
type Store struct {
	Pool *pgxpool.Pool
}

// New opens a connection pool against databaseURL and verifies connectivity
// with a bounded ping before returning. The returned Store's Pool must be
// closed via Close when the caller is done with it.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse pool config: %w", err)
	}

	// Sane defaults for a small API service. ParseConfig already applies
	// pgx's own defaults (e.g. MaxConns based on GOMAXPROCS); we only pin
	// down the knobs that matter for a long-lived server process.
	if cfg.MaxConns < 4 {
		cfg.MaxConns = 4
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: new pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	return &Store{Pool: pool}, nil
}

// Close releases all pooled connections. Safe to call once when the Store
// is no longer needed (e.g. on process shutdown).
func (s *Store) Close() {
	s.Pool.Close()
}
