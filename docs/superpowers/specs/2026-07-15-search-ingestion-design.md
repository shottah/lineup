# Search + title ingestion (issue #11) — design

Date: 2026-07-15
Status: approved (user: "write the spec" after section-by-section walkthrough)
Issue: [#11 feat(api): search and title ingestion endpoints](https://github.com/shottah/lineup/issues/11)

## Context

The last API-track issue. `GET /v1/search` proxies TMDB multi-search;
`GET /v1/titles/{kind}/{tmdbID}` ingests a title on first contact and
returns the full title-page payload. Both clients this depends on are on
`main`: `internal/tmdb` (#9) and `internal/tvmaze` (#29); the schema
(`titles`, `title_seasons`, `title_airings`, `providers`,
`title_providers`, `UNIQUE (kind, tmdb_id)`) shipped in #25 with all
three TTL timestamp columns. Everything is hermetically tested (user's
explicit call: no live e2e step; live verification waits for #16).
Local-only buildout — no infra work in this issue. Branch
`feat/11-search-ingestion` off `main`, squash-merge.

## Design

Three layers, matching the codebase's existing seams: store methods
(live-PG-tested), a new `internal/ingest` orchestration package
(fake-driven tests), thin handlers (httptest with fakes).

### Store layer (`api/internal/store/titles.go`)

`Title` mirrors the `titles` row: `ID, TMDBID int64; Kind string;
TVMazeID *int64; Name, Overview, PosterPath string; RuntimeMinutes int;
Airing bool; RefreshedAt, ProvidersRefreshedAt, AiringsRefreshedAt
time.Time`.

- `UpsertTitle(ctx, TitleUpsert) (*Title, error)` — `INSERT … ON
  CONFLICT (kind, tmdb_id) DO UPDATE` over the metadata columns, always
  stamping `refreshed_at = now()`. `tvmaze_id` uses
  `COALESCE(EXCLUDED.tvmaze_id, titles.tvmaze_id)` so a refresh that
  didn't re-run the TVMaze lookup never clobbers a stored id. Provider
  and airing stamps are NOT touched here.
- `GetTitleByTMDB(ctx, kind string, tmdbID int64) (*Title, error)` —
  `(nil, nil)` when no row; ingest branches on nil ("ingest-if-absent").
- `ReplaceSeasons(ctx, titleID int64, seasons []SeasonRow) error` —
  transactional delete-all + insert; `SeasonRow{Number, EpisodeCount int}`.
- `ReplaceProviders(ctx, titleID int64, region string, provs
  []ProviderRow) error` — upserts the `providers` dictionary rows
  (`ON CONFLICT (id) DO UPDATE` name/logo), replaces `title_providers`
  for (title, region) only, stamps `providers_refreshed_at = now()`.
  `ProviderRow{ID int64; Name, LogoPath string}`.
- `ReplaceFutureAirings(ctx, titleID int64, today string, airs
  []AiringRow) error` — deletes rows with `air_date >= today`, inserts
  the given set (caller pre-filters to future), stamps
  `airings_refreshed_at = now()`. Past airings are never touched.
  `AiringRow{Season, Episode int; AirDate string}` (`YYYY-MM-DD`).
- `GetTitleFull(ctx, userID, titleID int64, region string) (*TitleFull,
  error)` — one payload struct: the `Title`, its `[]SeasonRow`, the
  region's `[]ProviderRow` (joined through the dictionary for
  name/logo), and the caller's entry as `*Entry` (LEFT JOIN
  `user_titles`; nil when the user has no row). Reuses the existing
  `Entry` shape from `entries.go` so title page and shelves render the
  same JSON.

TTL stamps live inside the store methods so bookkeeping cannot be
forgotten by a caller.

### Ingest (`api/internal/ingest`)

`Service{TMDB MetadataClient; TVMaze AiringsClient; Store TitleStore;
Now func() time.Time}` — all three interfaces are defined ingest-side
and satisfied by `*tmdb.Client`, `*tvmaze.Client`, `*store.Store`.

`EnsureTitle(ctx, kind string, tmdbID int64, region string)
(*store.Title, error)`:

1. **Validate kind** (`movie`|`series`), else `ErrBadKind`.
2. **Lookup** `GetTitleByTMDB`.
3. **Fresh ingest** (nil row): fetch TMDB details (movie or TV) →
   `UpsertTitle` → `ReplaceSeasons` (series) → `WatchProviders` →
   `ReplaceProviders`. TMDB `ErrNotFound` → `ErrTitleNotFound` (handler
   404); any other TMDB failure is a real error (nothing cached to
   serve). Providers failure on fresh ingest degrades: title lands,
   `providers_refreshed_at` stays epoch so the next hit retries.
4. **TVMaze enrichment** (fresh ingest or metadata refresh): only for
   series TMDB marks airing (`Airing == true`) with a non-empty IMDB id
   and no stored `tvmaze_id` yet — `LookupByIMDB` → store id →
   `Episodes` → filter blank/past air dates → `ReplaceFutureAirings`.
   TVMaze `ErrNotFound` (common: no TVMaze entry) or any TVMaze failure
   degrades the same way: title serves fine, airings retry on the next
   hit because their stamp stays epoch. Ended series skip TVMaze
   entirely — they have no future episodes.
5. **TTL refresh** (row exists), each class independent:
   - metadata (30d): re-fetch details, `UpsertTitle`, `ReplaceSeasons`
     (series), re-run step 4's gate.
   - providers (7d): `WatchProviders` → `ReplaceProviders`.
   - airings (1d): only when `TVMazeID != nil && Airing` — `Episodes` →
     `ReplaceFutureAirings`.
   Any upstream failure during any refresh serves the cached row
   unchanged (stale ≠ 500) — including a TMDB 404 on refresh (title
   pulled from TMDB later doesn't take the cached copy down with it).
6. Return the up-to-date `*store.Title`.

`today` for airing filtering/deletion is `Now().UTC().Format("2006-01-02")`.

TTL constants live in `ingest` (`metadataTTL = 30 * 24 * time.Hour`,
etc.); staleness is `Now().Sub(stamp) > ttl`, so the epoch default means
"immediately stale".

### HTTP layer (`api/internal/httpserver`)

`Deps` gains two nil-guarded fields, following the existing pattern:
`Search SearchClient` (`SearchMulti(ctx, query) ([]tmdb.SearchResult,
error)`) and `Ingest Ingester` (`EnsureTitle(...)`). Routes mount inside
the authed `/v1` group:

- `GET /v1/search?q=` — trimmed empty `q` → 422 (existing error
  envelope); otherwise thin proxy, NO DB writes. Response:
  `{"results":[{"tmdbId","kind","name","overview","posterPath","year"}]}`
  in TMDB's order, first page only. Upstream failure → 502.
- `GET /v1/titles/{kind}/{tmdbID}` — kind not `movie|series` or
  unparseable id → 404 (route vocabulary, matching guide handlers);
  `EnsureTitle(kind, id, userFrom(ctx).Region)`; `ErrTitleNotFound` →
  404; upstream error with no cache → 502; then
  `Store.GetTitleFull(userID, titleID, region)` → 200
  `{"title":…,"seasons":[…],"providers":[…],"entry":…|null}`.

The search handler depends on the tmdb client only through the
`SearchClient` interface — handler tests fake it; no HTTP-level TMDB
stubbing.

### Testing (hermetic — no network, per user decision)

- **ingest** (fakes for all three deps): fresh movie; fresh airing
  series with IMDB id (airings land); fresh series without IMDB id (no
  TVMaze call, no error); fresh ended series (TVMaze skipped); each TTL
  class refreshes independently at its boundary (fake `Now`); fresh +
  each-refresh upstream-down paths (cache served; degraded stamps stay
  epoch); TMDB 404 on refresh serves cache; bad kind.
- **store/titles** (existing live-PG harness): upsert insert-vs-update
  incl. tvmaze_id COALESCE; ReplaceSeasons/ReplaceProviders scoping
  (other regions untouched); ReplaceFutureAirings deletes only `>=
  today`; GetTitleFull joins, region filtering, nil entry vs populated
  entry.
- **handlers** (httptest + fakes): search happy path, empty q 422,
  upstream 502; title happy path (payload shape), bad kind 404, bad id
  404, not-found 404, upstream 502.

Acceptance: `go test ./...` green offline.

## Out of scope

Web search/title pages (#16); rotation cap or entry mutation (shipped in
#30); background/scheduled refresh (TTLs are request-driven in v1);
pagination; Secret Manager / deploy wiring (local-only posture,
2026-07-14); HTTP response caching.
