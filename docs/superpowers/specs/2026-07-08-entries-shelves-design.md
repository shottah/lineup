# Shelves and rotation entry endpoints (issue #12) — design

Date: 2026-07-08
Status: approved (user: "yes", after design presentation)
Issue: [#12 feat(api): shelves and rotation entry endpoints](https://github.com/shottah/lineup/issues/12)

## Context

Shelves are the user's library: watchlist / rotation (cap 8, the guide
engine's primary input) / watched, plus favorites and ratings views. The
`user_titles` table already models everything (status CHECK, rating
NUMERIC(2,1) with a 0.5–5.0 CHECK, favorite, pointer_season/episode
defaulting 1/1, added_at, nullable watched_at) — no migration needed.
Fully local build: Postgres + existing `RequireAuth`; no external APIs.
Branch `feat/12-entries-shelves` off `main`, squash-merge.

## Endpoints (inside the existing `/v1` auth group)

### `PATCH /v1/titles/{id}/entry`

Partial update of the caller's entry for a title; creates the row on first
touch (insert-or-update, like the user upsert).

Body fields, all optional (absent = unchanged): `status`, `rating`,
`favorite`, `pointer:{season,episode}`. `rating` uses RawMessage presence
(the repo's established pattern): absent = unchanged, explicit `null` =
clear, number = set. All fields absent → 422 `nothing to update`.

Validation (422): `status` ∈ `none|watchlist|rotation|watched`; `rating`
half-steps in [0.5, 5.0] (×2 must be an integer 1–10 — DB CHECK covers
range, API owns granularity); `pointer.season` ≥ 1, `pointer.episode` ≥ 1.
Malformed JSON → 400. Non-numeric `{id}` or a title id not in `titles` →
404 `not_found` (FK violation 23503 mapped, not pre-queried).

Rules:
- **Rotation cap 8:** setting `status=rotation` first counts the user's
  OTHER rotation titles (`CountRotation(ctx, userID, excludeTitleID)`) so
  re-setting an already-rotating title is idempotent, never a false 409.
  Count ≥ 8 → `409 {"error":"rotation_full"}`.
- **Watched stamping:** setting `status=watched` stamps
  `watched_at = now()` (re-marking re-stamps — "last watched"). Moving off
  `watched` keeps the timestamp (historical).
- **Accepted v1 race:** count-then-upsert is not transactional; two
  concurrent requests from the same user could reach 9 rotation entries.
  Only the user racing themselves is affected; a transactional guard is
  deferred until multi-device polish matters.

Response 200: the full entry joined with title display data (below).

### `GET /v1/me/shelves/{shelf}`

`shelf` ∈ `watchlist|rotation|watched|favorites|ratings`; anything else →
404. Response `{"entries":[Entry…]}` (wrapped for extensibility).

Filters/ordering: the three status shelves filter `status = shelf`,
ordered `added_at DESC`; `favorites` = `favorite = true` (any status),
`added_at DESC`; `ratings` = `rating IS NOT NULL`, ordered
`rating DESC, added_at DESC`.

## Entry shape (JSON, both endpoints)

`{title_id, kind, name, poster_path, runtime_minutes, airing, status,
rating (number|null), favorite, pointer:{season,episode}, added_at,
watched_at (RFC3339|null)}` — entry + the title columns the profile page
(#17) renders. Produced by a single CTE query (upsert/select RETURNING
joined to `titles`), one round trip.

## Store (`api/internal/store/entries.go`)

- `Entry`, `Pointer`, `EntryUpdate{Status *string; Rating *float64;
  ClearRating bool; Favorite *bool; Pointer *Pointer}` types.
- `UpsertEntry(ctx, userID, titleID int64, u EntryUpdate) (*Entry, error)`
  — INSERT … ON CONFLICT with COALESCE partial semantics (idiom from
  `UpdateUserPrefs`); `watched_at` CASE-stamped; FK 23503 →
  `ErrTitleNotFound` sentinel.
- `CountRotation(ctx, userID, excludeTitleID int64) (int, error)`.
- `Shelf(ctx, userID int64, shelf string) ([]Entry, error)` — validated
  shelf names only; defends with an error on unknown values anyway.

## Handlers (`api/internal/httpserver/entries.go`)

`EntryStore` interface (three methods above) implemented by `*store.Store`;
new `Deps.Entries` field — same narrow-interface pattern as `UserStore`.
Routes registered in the existing `/v1` group.

## Testing

- Hermetic handler tests (fake EntryStore mirroring the SQL semantics):
  cap 409 + idempotent re-set at cap, every 422, 400, 404 (title, shelf),
  watched stamping, rating clear via `null`, every shelf filter — the
  issue's acceptance list.
- Store integration tests (gated on `TEST_DATABASE_URL`): seed `titles`
  rows directly (ingestion #11 isn't needed — only that titles exist),
  exercise upsert partial semantics, count-exclusion, filters and ordering,
  watched_at stamp/preserve.

## Out of scope

Title ingestion (#11), guide generation (#13), transactional cap
enforcement, pagination (shelves are ≤ dozens of rows in v1).
