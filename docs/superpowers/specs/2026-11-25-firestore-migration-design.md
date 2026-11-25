# Firestore migration design

Date: 2026-11-25
Status: draft, awaiting approval

## Intent

Lineup should run entirely on serverless Google services and cost close to
nothing at rest. Cloud Run (API) and Firebase App Hosting (web) are already
serverless. The database was the only always-on piece: a Postgres 16 VM
(`lineup-db`), which has now been deleted along with its firewall rules and
backup bucket (PR #61). This project replaces Postgres with Cloud Firestore.

Constraints from the owner:

- Google-only. No third-party databases or hosting.
- No VMs.
- Firestore protected by Security Rules.

## Goals

- The Go API stores all data in Firestore (Native mode, `(default)` database,
  `us-central1`). The free daily quota only applies to `(default)`.
- Cloud Run authenticates to Firestore with Application Default Credentials as
  the `api-runtime` service account. No connection strings, no key files.
- The API remains the only path to the data. Firestore Security Rules deny all
  client (browser/mobile SDK) access.
- Every existing store behaviour is preserved, proven by the existing store
  test suite ported to run against the Firestore emulator, including the
  cross-user isolation tests (`2de827a`).
- All application IDs become strings (owner's decision), in the API JSON and
  in the web client.
- Postgres is fully removed: code, dependencies, migrations, config, secrets,
  docs.

## Non-goals

- Migrating data. The VM is gone with no backups, and the app was never live
  (no App Hosting backend, no registered web app, placeholder Firebase config).
  Firestore starts empty; users are created on first sign-in and titles are
  re-ingested from TMDB on demand.
- Direct client reads from Firestore. Considered and deferred (see
  Approaches). If a feature later needs realtime sync, it opens read-only
  rules for that one collection; writes stay server-side.
- Automatic deploys (Cloud Build trigger, App Hosting backend). That is the
  next project, after this one, so the trigger doesn't ship API revisions
  that crash at boot.
- Changing any user-facing behaviour or the HTTP route shapes.

## Anti-goals

- Two authorization implementations for the same data (Go code and rules
  both granting reads). One path per operation.
- A normalized "table per collection" port that multiplies billed reads and
  writes and burns the free quota.
- Weakening isolation: no store method may read or write another user's data;
  "not found" and "not yours" stay indistinguishable (`ErrGuideNotFound`).

## Approaches considered

**Data layout**

- **A. Aggregate (document-style) model. Picked by the owner.** One document
  per thing the app reads together; titles embed seasons/airings/providers,
  guides embed their items. Fewest billed operations, multi-row invariants
  collapse into single-document transactions.
- B. Mirror the Postgres tables as collections. Most mechanical port, but
  10 to 50 times more billed operations (a week's regenerate rewrites ~50 item
  docs; opening a guide reads every item, title, season, provider) and wider
  transactions. Rejected.
- Dropped: A with guide items as a subcollection. Only pays off for per-item
  client listeners, which we deferred.

**IDs**

- **String IDs. Picked by the owner**, over keeping numeric IDs via
  random 53-bit integers and per-guide counters. Cleaner fit with Firestore
  document IDs; costs a web client change in the same project.

**Client access**

- **Server-only, deny-all rules. Agreed.** Over client-direct and the hybrid
  (server writes, client reads). The guide view is a composed response,
  writes have multi-document invariants, search needs the TMDB secret, and a
  single path keeps one authorization implementation that the isolation tests
  cover.

## Data model

All field names camelCase. Dates are `YYYY-MM-DD` strings (lexicographic order
equals date order; matches the current API and engine). Timestamps are
Firestore timestamps.

### `users/{firebaseUid}`

The Firebase UID is the user ID; the separate internal numeric user ID goes
away. The UID stays out of the API JSON (`store.User.ID` keeps `json:"-"`, as
today); the web never reads `user.id`. (Author's call: preserves the current
deliberate exclusion.)

```
email, displayName, region (default "US"), schedulePrefs (map),
createdAt, updatedAt
```

### `users/{uid}/entries/{titleId}`

One per title the user has interacted with (today's `user_titles`).

```
titleId, status ("none"|"watchlist"|"rotation"|"watched"),
rating (number 0.5..5.0, absent when unset), favorite (bool),
pointerSeason, pointerEpisode, addedAt, watchedAt (absent when unset)
```

Shelf queries: `status == X order by addedAt desc`; `favorite == true order by
addedAt desc`; `rating > 0 order by rating desc, addedAt desc`. Shelf
responses that need title fields fetch the referenced title docs with one
batched `GetAll`. `CountRotation` uses a count aggregation on
`status == "rotation"`, subtracting the excluded title if present.

### `titles/{kind}-{tmdbId}`

Shared catalog (e.g. `series-1399`, `movie-603`). The ID is deterministic, so
ingestion upserts by ID and `GetTitleByTMDB` is a direct get, no query.

```
kind, tmdbId (number), tvmazeId (number, optional), name, overview,
posterPath, runtimeMinutes, airing (bool),
refreshedAt, providersRefreshedAt, airingsRefreshedAt,
seasons:   map season number (as string key) -> episode count
airings:   array of {season, episode, airDate}
providers: map region -> array of provider IDs (numbers, sorted ascending)
```

`ReplaceSeasons`, `ReplaceProviders`, `ReplaceFutureAirings` become field
updates on the title doc. `ReplaceFutureAirings` replaces only airings on or
after `today` and keeps past airings forever, exactly as the current code
does; the engine relies on past airings (`passAirPins`, `schedulable`).

### `providers/{providerId}`

Shared, append-only provider dictionary, same semantics as today's
`providers` table: `ReplaceProviders` upserts `{name, logoPath}` for every
provider it sees and never deletes. `GuideLookups` reads the distinct
`providerId`s of a guide's items from here with one batched `GetAll`, so a
guide made before a provider refresh (or before a region change) still gets
names and logos. (Author's call: preserves current behaviour; embedding
names in titles would have blanked them for stale items.)

### `users/{uid}/guides/{guideId}`

`guideId` is a Firestore auto-ID.

```
startDate, endDate, seed (number), createdAt,
items: array of {
  id, date, startMin, endMin, titleId, season, episode,
  providerId (number), isPlan, pinned, edited, watched
}
```

Item `id`: a short random ID (e.g. 10 base62 chars), unique within its guide
(regenerate on collision). Items are returned sorted in Go by
`date, startMin, isPlan desc, titleId`, matching today's SQL order.

A week of items is far below the 1 MiB document limit.

### IDs that stay numeric

`tmdbId` and `providerId` are TMDB's identifiers, not our document IDs, so
they stay numbers in Firestore and in the API JSON. Everything we mint
(`user.id`, `title.id`, `guide.id`, item `id`, `title_id` references) becomes a
string.

## Operations mapping

The store package keeps its role and method set; signatures change from
`int64` IDs to `string`. The handler interfaces (`GuideStore`, `UserStore`,
`EntryStore`, `TitleReader`, ingest `TitleStore`) keep their shape with string
IDs. `httpserver.Deps.Store` stops holding the concrete type; handlers depend
only on the interfaces.

Firestore transactions (Go client `RunTransaction`) require all reads before
writes; each operation below is written that way. Ownership is structural:
every user-scoped read goes through `users/{uid}/...`, so a guide ID from
another user simply doesn't exist under the caller's path.

- `UpsertUserByFirebaseUID`: transaction get-or-create on `users/{uid}`;
  refresh email/displayName on existing docs as today.
- `UpdateUserPrefs`: update on `users/{uid}`.
- `UpsertEntry`: transaction on `users/{uid}/entries/{titleId}` (create with
  defaults, apply PATCH semantics).
- `CreateGuideReplacingOverlaps`: transaction. Query the user's guides with
  `endDate >= start`, filter `startDate <= end` in Go, delete those, create
  the new guide with items.
- `CurrentGuide`: query `endDate >= today`, pick newest by `createdAt` (then
  ID) in Go. A user has few guides; no composite index needed.
- `GuideWithItems`: get `users/{uid}/guides/{id}`.
- `ReplaceUnkeptItems`: transaction: read guide, keep listed item IDs, append
  new items with fresh IDs, set seed.
- `UpdateGuideItem`: transaction: read guide, enforce past-move rules,
  apply PATCH (duration preservation, provider recompute on swap), then drop
  same-date alternates (`isPlan == false`) of the swapped-in title, as today.
- `DeleteGuideItem`: transaction: remove the item from the array;
  `ErrGuideNotFound` when the guide or item is missing.
- `MarkItemWatched` / `UnmarkItemWatched`: transaction reading the guide,
  the entry, and the title (for `kind` and `seasons`), then writing the guide
  and the entry. Pointer and status rules are unchanged, including
  never-regress, past-finale completion, and the independent pointer/status
  rollback on unmark.
- `GuideInputTitles`: query entries with `status == "rotation"`, batched
  `GetAll` of their titles, build engine titles from embedded fields.
- `GuideLookups`: batched `GetAll` of the distinct titles and the distinct
  providers (`providers/{id}`) in the guide's items.
- `SwapTitle`: get entry (status rotation/watchlist) and title; providers for
  the region sorted ascending.
- `GetTitleFull`: get title plus the caller's entry.

## Security

**Server identity.** Cloud Run runs as `api-runtime@<project>`; the Firestore
client gets short-lived tokens from the metadata server (ADC). New IAM grant:
`roles/datastore.user` on `api-runtime` (added to `bootstrap.sh`).

**User identity.** Unchanged: browser sends a Firebase ID token, `requireAuth`
verifies it and resolves the user by UID. (Optional, builder's call to
propose separately: `VerifyIDTokenAndCheckRevoked`.)

**Rules.** `firestore.rules` denies all reads and writes for every path.
Server SDKs bypass rules, so rules only guard direct client access, which the
public web config would otherwise allow. Rules have an automated test against
the emulator proving an authenticated client cannot read or write
`users/{its own uid}/...` or `titles/...`.

**Indexes.** `firestore.indexes.json` holds the composite indexes the shelf
queries need. Rules and indexes deploy with `firebase deploy --only firestore`.

## Infrastructure changes

- `bootstrap.sh`: enable `firestore.googleapis.com`; create the `(default)`
  database (Native, `us-central1`) if absent; grant `roles/datastore.user` to
  `api-runtime`. Verified 2026-11-25: the Firestore API is not yet enabled on
  `lineup-app-ae6b`, so no database of any mode or location exists; mode and
  location are permanent once created, so the script must pass both
  explicitly.
- `firebase.json`: add `firestore` (rules, indexes) and the Firestore
  emulator alongside the existing Auth emulator.
- `run-service.yaml`: remove `DATABASE_URL` and its transitional comment.
- TMDB secret: the manifests and `bootstrap.sh` use `tmdb-read-token`, but
  the project only has `tmdb-api-key`, and it holds the `REPLACE_ME`
  placeholder (checked by length/prefix, value not printed). Standardize on
  `tmdb-read-token` (created by `bootstrap.sh`); delete `tmdb-api-key`. Loading
  the real token stays a manual step in `infra/README.md`.
- After the Firestore revision is deployed and healthy: delete the
  `database-url` and `db-password` secrets.
- Docs: `infra/README.md`, `infra/local-dev.md` (Firestore emulator replaces
  the Docker Postgres), root `README.md` stack line.

## API code changes

- `config`: drop `DATABASE_URL`; `FIREBASE_PROJECT_ID` stays required and also
  names the Firestore project.
- `main.go`: one Firebase app serving both Auth and Firestore; no migrations.
- Remove `store/migrate.go`, `store/migrations/`, `pgx`, `golang-migrate`.
- `guide` engine package: title and item title IDs become `string`; provider
  stays `int64`. Its tests update accordingly; engine behaviour is unchanged.
  Determinism note: anywhere the engine orders or seeds by title ID must stay
  deterministic with string IDs (sort strings).

## Web client changes

- `lib/types.ts` and consumers: our IDs become `string` (`tmdb_id` and
  provider IDs stay `number`).
- `posterTint.ts` `hashHue`: replace the numeric Knuth hash with a stable
  string hash mapped to 0 to 359 (e.g. FNV-1a). Colours will shift once;
  acceptable since there are no users. Update its tests.
- Drag/mutation hooks, `BoardView` swap vars, and any route params typed as
  numbers.

## Testing

- Store tests run against the Firestore emulator, skipped when
  `FIRESTORE_EMULATOR_HOST` is unset (same pattern as today's
  `TEST_DATABASE_URL`). Each test isolates itself (unique project ID per test
  or emulator reset between tests).
- The port is test-first: adapt each existing store test to string IDs and the
  emulator, watch it fail, then implement.
- `api-ci.yml` runs Go tests under `firebase emulators:exec --only firestore`
  (needs Node + Java in the job), so store tests run in CI for the first time;
  today they are always skipped there.
- Rules test (above) runs in CI too.
- Handler and engine tests keep running without the emulator.

## Rollout

1. Land PR #61 (VM infra retirement). This work stacks on it.
2. Land this project's PR(s).
3. Run the updated `bootstrap.sh` (Firestore API, database, IAM) and
   `firebase deploy --only firestore` (rules, indexes).
4. Deploy the API manually through the existing Cloud Deploy pipeline; check
   `/healthz` and a signed-in smoke test against the emulator-free stack.
5. Delete `database-url`, `db-password`, `tmdb-api-key`.

## Left to the builder

- Package layout inside `store` (one file per aggregate is fine) and how the
  Firestore client is injected.
- Exact item-ID generator and alphabet.
- Where the rules test lives and its tooling (e.g. `@firebase/rules-unit-testing`
  in a small package under `infra/firestore/` or alongside `firebase.json`).
- Whether to split into more than one PR (e.g. API + infra, then web), as long
  as `main` never has an API that can't boot against its documented config.
