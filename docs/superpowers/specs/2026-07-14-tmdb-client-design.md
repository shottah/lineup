# TMDB client (issue #9) — design

Date: 2026-07-14
Status: approved (user: "option A is good")
Issue: [#9 feat(api): tmdb client](https://github.com/shottah/lineup/issues/9)

## Context

TMDB supplies search, title metadata, artwork, runtimes, and per-region
flatrate watch providers (JustWatch-powered). Work was deferred on
2026-07-09 for lack of credentials; as of 2026-07-14 both a v3 API key and
a v4 read access token are in hand, unblocking #9 and, by dependency, #11
(ingestion) and #16 (web search pages). The client is fixture-tested — no
network in tests; the token is used once at development time to record
real responses. Branch `feat/9-tmdb-client` off `main`, squash-merge.

## Design

`api/internal/tmdb`, mirroring `internal/tvmaze` idioms (plain
constructors, `tmdb:`-prefixed wrapped errors, `ErrNotFound` sentinel,
hermetic fixture tests).

### Auth

v4 read access token sent as `Authorization: Bearer <token>` on every
request (TMDB's recommended style; keeps the secret out of URLs and
logs). The v3 query-param key is not used. Config field/env rename:
`Config.TMDBKey` / `TMDB_API_KEY` → `Config.TMDBReadToken` /
`TMDB_READ_TOKEN`, touching four files: `config.go`, `config_test.go`,
`infra/run-service.yaml` (env + `secretKeyRef` rename to
`tmdb-read-token`), and the `infra/README.md` checklist item. The token
stays optional at boot (the API runs without TMDB until #11 wires
ingestion in). Locally the token lives in the shell env or a gitignored
file, never in the repo.

**Pre-merge infra step (manual, approval workflow):** merging #9 touches
`api/**`, which triggers Cloud Build → Cloud Deploy applying the renamed
`secretKeyRef` — so before merge, the `tmdb-read-token` secret must exist
in Secret Manager with the real token as a version, and the Cloud Run
runtime service account needs `secretmanager.secretAccessor` on it
(mirroring `tmdb-api-key`). The placeholder `tmdb-api-key` secret is
deleted afterwards. Exact commands live in the implementation plan; each
is shown and approved one at a time per the infra workflow.

### Surface

- `New(readToken string) *Client` (production,
  `https://api.themoviedb.org`) and
  `NewWithBaseURL(base, readToken string) *Client` (tests / injection).
- `SearchMulti(ctx, query string) ([]SearchResult, error)` →
  `GET /3/search/multi?query=<q>`. Keeps `media_type` `movie`/`tv` only
  (drops `person` and anything else); maps `tv` → `series`. First page
  only (20 results) — deliberate for a thin v1 search proxy; no
  pagination plumbing.
- `MovieDetails(ctx, id int64) (Movie, error)` → `GET /3/movie/{id}`.
- `TVDetails(ctx, id int64) (TV, error)` →
  `GET /3/tv/{id}?append_to_response=external_ids` — one round trip for
  details + IMDB id. Seasons exclude `season_number == 0` (specials).
  `RuntimeMinutes` is the first `episode_run_time` entry, else 0.
  `Airing` is `status == "Returning Series"`. `IMDBID` is `""` when
  TMDB has none — a normal outcome ingestion branches on (skips TVMaze),
  not an error.
- `WatchProviders(ctx, kind string, id int64, region string) ([]Provider, error)`
  → `GET /3/{movie|tv}/{id}/watch/providers`. Takes the Lineup kind
  (`"movie"`|`"series"`, mapped to TMDB's `movie`/`tv` path segment);
  any other kind is an immediate error. Returns **flatrate** entries for
  the region only; a region absent from the response, or present without
  `flatrate`, yields an empty slice and nil error (a title having no
  streaming home in your region is data, not failure).

### Types (minimal — only what the schema consumes)

- `SearchResult{TMDBID int64; Kind, Name, Overview, PosterPath, Year string}`
  — `Year` derived from `release_date`/`first_air_date` (`"2019"`, or
  `""` when absent); the search UI uses it for disambiguation.
- `Movie{TMDBID int64; Name, Overview, PosterPath string; RuntimeMinutes int}`
- `Season{Number, EpisodeCount int}`
- `TV{TMDBID int64; Name, Overview, PosterPath string; RuntimeMinutes int; Airing bool; IMDBID string; Seasons []Season}`
- `Provider{ID int64; Name, LogoPath string}` — feeds the `providers`
  table verbatim.

Movie `title` and TV `name` both map to `Name`; `poster_path` is stored
raw (image URL assembly is a frontend concern). TMDB responses carry many
more fields; decoding ignores them — deliberate tolerance, pinned by
fixtures that keep the full real response shape.

### External-API policy (per v1 design spec)

- **Timeout:** dedicated `http.Client{Timeout: 10s}`.
- **Rate limit:** `golang.org/x/time/rate` token bucket, 20 req/s with
  burst 5 — comfortably under TMDB's ~50 req/s guideline while never
  throttling real Lineup traffic. Every request waits on the limiter
  (ctx-aware).
- **Retry:** exactly one retry on 429 or 5xx (the issue names only 429;
  5xx matches the tvmaze house behavior at zero cost), honoring numeric
  `Retry-After` seconds when present (clamped at 30s), else 500ms. 404 →
  `ErrNotFound`, never retried. Other 4xx → wrapped status error.
- **Shared client layer:** the tvmaze spec deferred extracting a common
  HTTP core "when a second client exists". That second client is this
  one; extraction was considered and declined again — the shared surface
  is ~50 lines of plumbing that legitimately diverges (auth header,
  error taxonomy, rate caps). Revisit at a third client.

### Testing (hermetic, no network)

Fixtures recorded from live TMDB v3 using the read token, trimmed to a
few representative results with extra fields intact, committed under
`testdata/`. httptest server cases:

- search happy path — asserts exact path + query **and the Bearer
  header**; proves `person` results dropped and `tv` → `series`;
- movie details happy path;
- tv details — season 0 excluded, IMDB id surfaced from `external_ids`,
  and a fixture variant without an IMDB id decodes to `IMDBID == ""`;
- providers — flatrate-only for the region; region missing from response
  → empty slice, nil error; bad kind → error without a request;
- 404 → `ErrNotFound`;
- 429 with `Retry-After: 0` then success (proves single retry, fast);
- 500 twice → error (proves the retry cap).

Acceptance: `go test ./internal/tmdb/` green with no network.

## Out of scope

Ingestion (`EnsureTitle`), TTL refresh, store methods, and the
`/v1/search` + `/v1/titles` endpoints (#11); pagination; image URL
building. Secret Manager work is limited to the pre-merge rename above —
nothing in this issue *consumes* the deployed secret until #11.
