# TVMaze client (issue #10) — design

Date: 2026-07-08
Status: approved (user: "That looks fine")
Issue: [#10 feat(api): tvmaze client](https://github.com/shottah/lineup/issues/10)

## Context

TVMaze supplies real episode air dates for currently-airing series
(`title_airings` table; the guide engine pins airing episodes to their air
night). Keyless API, so it is fully buildable and live-testable now — unlike
TMDB (#9), deferred while no API key is acquirable. Built local-first;
independent of the parked cloud work. Branch `feat/10-tvmaze-client` off
`main`, squash-merge.

## Design

`api/internal/tvmaze`, mirroring the repo's client idioms (plain
constructors, wrapped `tvmaze:`-prefixed errors, hermetic tests).

### Surface

- `New() *Client` (production, `https://api.tvmaze.com`) and
  `NewWithBaseURL(base string) *Client` (tests / injection).
- `LookupByIMDB(ctx, imdbID string) (Show, error)` →
  `GET /lookup/shows?imdb=<id>`; TVMaze 404 → `ErrNotFound` (package-level
  sentinel; ingestion (#11) branches on it — "no TVMaze entry" is a normal
  outcome for movies and older shows, not an error to log).
- `Episodes(ctx, showID int) ([]Episode, error)` →
  `GET /shows/{id}/episodes`; unknown show id → `ErrNotFound`.

### Types (minimal — only what the schema consumes)

- `Show{ID int; Name string; Status string}` — `ID` feeds
  `titles.tvmaze_id`, `Status` (e.g. "Running", "Ended") feeds airing
  status.
- `Episode{Season int; Number int; AirDate string}` — `AirDate` is
  `"YYYY-MM-DD"` or `""`. TVMaze returns `""`/`null` for unscheduled
  episodes; both decode to `""` and are tolerated (issue requirement). Date
  parsing is ingestion policy, not the client's.

TVMaze responses carry many more fields; decoding ignores them (standard
`encoding/json` behavior) — deliberate tolerance, pinned by fixtures that
include the full real response shape.

### External-API policy (per v1 design spec, self-contained option (b))

- **Timeout:** dedicated `http.Client{Timeout: 10s}`.
- **Rate limit:** `golang.org/x/time/rate` token bucket,
  `rate.Every(600ms)`, burst 1 (~16 req/10s, under TVMaze's documented 20
  per 10s per IP). Every request waits on the limiter (ctx-aware).
- **Retry:** exactly one retry on 429 or 5xx, honoring `Retry-After`
  (seconds) when present, else 500ms. Second failure returns a wrapped
  status error. 404 and other 4xx never retry.
- A shared multi-client HTTP layer is deliberately NOT built — TMDB (#9) is
  deferred indefinitely; extract commonality when a second client exists.

### Testing (hermetic, no network)

Fixtures in `testdata/` copied from real TVMaze response shapes (extra
fields intact). httptest server cases: lookup happy path; lookup 404 →
`ErrNotFound`; episode parsing incl. `""` and `null` air dates; 429 with
`Retry-After: 0` then success (proves single retry, fast); 500 twice →
error (proves retry cap). Acceptance: `go test ./internal/tvmaze/` green.

## Out of scope

TMDB client (#9), ingestion/refresh logic and TTLs (#11), persisting
airings (#11), any HTTP caching.
