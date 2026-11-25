# Firestore Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Postgres store with Cloud Firestore (document model, string IDs, deny-all rules) and remove Postgres from the codebase.

**Architecture:** A new Go package `api/internal/docstore` implements every store operation against Firestore, built test-first against the Firestore emulator by porting the existing `store` tests. Once complete, the handlers and `main.go` switch to it and the `store` package is deleted. The web client switches its IDs to strings.

**Tech Stack:** Go 1.25, `cloud.google.com/go/firestore` (via `firebase.google.com/go/v4`), Firebase emulators (Auth + Firestore), Next.js 16 / Vitest.

**Spec:** `docs/superpowers/specs/2026-11-25-firestore-migration-design.md` (read it first; data model and operation semantics live there).

## Global Constraints

- Firestore: Native mode, `(default)` database, `us-central1`. Already provisioned in `lineup-app-ae6b`; do not create or delete databases.
- Server auth is ADC as `api-runtime@lineup-app-ae6b.iam.gserviceaccount.com`. No keys, no connection strings.
- `firestore.rules` denies all reads and writes on every path, byte-for-byte the ruleset already released:
  ```
  rules_version = '2';
  service cloud.firestore {
    match /databases/{database}/documents {
      match /{document=**} {
        allow read, write: if false;
      }
    }
  }
  ```
- IDs we mint are strings: user = Firebase UID, title = `{kind}-{tmdbId}`, guide = Firestore auto-ID, guide item = 10 chars `[0-9A-Za-z]` from `crypto/rand`. `tmdb_id` and provider IDs stay numbers.
- JSON field names in API responses do not change, only ID value types. `/v1/me` returns the UID as `id`.
- Firestore field names are camelCase as listed in the spec's Data model.
- Dates are `YYYY-MM-DD` strings; timestamps are Firestore timestamps.
- Firestore emulator port `8085` (8080 is the API); Auth emulator stays `9099`.
- Store tests skip when `FIRESTORE_EMULATOR_HOST` is unset; each test uses its own project ID `demo-<unique>` so tests never share data.
- Past airings are kept forever (`ReplaceFutureAirings` only replaces airings on or after `today`).
- No behaviour changes visible to users; route shapes unchanged.

## Review Focus

- Shelf queries need composite indexes that the emulator does not enforce; a missing index is a production-only 500. Pinned by `TestIndexesCoverShelfQueries` (Task 4) and the shelf smoke check (Task 11).
- Two tabs editing the same guide at once: Firestore transactions retry, and neither edit may be lost. Pinned by `TestConcurrentItemUpdatesBothApply` (Task 6).
- Marking or unmarking a watched item whose title has no entry doc (removed from shelves) must still flip the item and create no entry, as today. Pinned by `TestMarkItemWatchedWithoutEntry` (Task 7).
- `keepIDs` naming items that don't exist must be ignored, not error. Pinned by `TestReplaceUnkeptItemsIgnoresUnknownKeepIDs` (Task 5).
- Path IDs from URLs that are empty, contain `/`, or match Firestore's reserved `__.*__` make the Go client return nil refs or errors; they must read as not found, never panic. Pinned by `TestInvalidIDsAreNotFound` (Task 7) and `TestPathIDRejectsInvalid` (Task 8).

## Known build window

Task 5 changes the `guide` engine's IDs to `string`, which breaks compilation of the old `store` and `httpserver` packages until Task 8 replaces them. In Tasks 5 to 7, verify with the package-scoped commands given, not `go build ./...`. Everything ships as one PR, so `main` never sees this.

## File Structure

```
firebase.json                         modify: firestore rules/indexes + emulator
firestore.rules                       create
firestore.indexes.json                create
api/internal/docstore/
  docstore.go       Store, New, errors, validID
  ids.go            newItemID
  users.go          User + user ops
  titles.go         Title types + title/provider ops
  entries.go        Entry types + entry ops
  guides.go         Guide types, create/read/replace/lookups
  guide_items.go    UpdateGuideItem, DeleteGuideItem, SwapTitle
  watched.go        nextPointer, MarkItemWatched, UnmarkItemWatched
  *_test.go         ported from api/internal/store/*_test.go
  helpers_test.go   testStore, uniqueUID, seed helpers
  indexes_test.go   TestIndexesCoverShelfQueries
api/internal/firestorerules/rules_test.go   deny-all rules test (emulator REST)
api/internal/guide/guide.go          modify: Title.ID, Item.TitleID -> string
api/internal/httpserver/*.go         modify: docstore types, string IDs
api/internal/config/config.go        modify: drop DATABASE_URL
api/cmd/api/main.go                  modify: Firestore client, no migrations
api/internal/store/                  delete (Task 8)
.github/workflows/api-ci.yml         modify: run tests under the emulator
web/src/lib/types.ts, lib/guide.ts, app/guide/*  modify: string IDs
infra/bootstrap.sh, infra/run-service.yaml, infra/README.md,
infra/local-dev.md, README.md        modify
```

---

### Task 1: Firestore scaffolding, emulator harness, rules, CI

**Files:**
- Modify: `firebase.json`, `.github/workflows/api-ci.yml`, `api/go.mod`
- Create: `firestore.rules`, `firestore.indexes.json`, `api/internal/docstore/docstore.go`, `api/internal/docstore/ids.go`, `api/internal/docstore/ids_test.go`, `api/internal/docstore/helpers_test.go`, `api/internal/firestorerules/rules_test.go`

**Interfaces:**
- Produces:
  - `type Store struct{ c *firestore.Client }`; `func New(c *firestore.Client) *Store`
  - `var ErrTitleNotFound = errors.New("docstore: title not found")`, `var ErrGuideNotFound = errors.New("docstore: guide not found")`, `var ErrPastMove = errors.New("docstore: cannot move a past slot")`, `var ErrPastTarget = errors.New("docstore: cannot move a slot into the past")`
  - `func validID(id string) bool`: false for empty, containing `/`, or matching `^__.*__$`
  - `func newItemID() string`
  - test helpers: `func testStore(t *testing.T) *Store`, `func uniqueUID(t *testing.T) string`

- [ ] **Step 1: Config files.** `firebase.json` adds `"firestore": {"rules": "firestore.rules", "indexes": "firestore.indexes.json"}` and `emulators.firestore: {"port": 8085}`, keeping the Auth emulator. `firestore.rules` is the exact text in Global Constraints. `firestore.indexes.json` starts as `{"indexes": [], "fieldOverrides": []}` (Task 4 fills it). Add `cloud.google.com/go/firestore` as a direct dependency in `api/go.mod`.

- [ ] **Step 2: Write failing tests**
  - `TestNewItemIDShape`: 1000 calls; each matches `^[0-9A-Za-z]{10}$`; all distinct.
  - `TestValidID`: true for `"abc"`, `"series-1399"`; false for `""`, `"a/b"`, `"__x__"`.
  - `firestorerules.TestRulesDenyClientAccess`: skips without `FIRESTORE_EMULATOR_HOST`. Using plain `net/http` against `http://$FIRESTORE_EMULATOR_HOST/v1/projects/demo-lineup/databases/(default)/documents/...` with `Authorization: Bearer <unsigned JWT>` (alg `none`, claims `sub`/`user_id` = `"u1"`, `aud`/`iss` for `demo-lineup`): GET `users/u1` returns 403; PATCH `users/u1` returns 403; GET `titles/series-1` returns 403; and the same GET with no Authorization header returns 403.

- [ ] **Step 3: Run, expect failure**
  Run: `cd api && go test ./internal/docstore/ ./internal/firestorerules/`
  Expected: compile errors (undefined `newItemID`, `validID`).

- [ ] **Step 4: Implement** `docstore.go` (Store, New, errors, validID), `ids.go` (newItemID), and `helpers_test.go`. `testStore` skips when `FIRESTORE_EMULATOR_HOST` is unset; otherwise it creates `firestore.NewClient(ctx, "demo-"+<lowercased, sanitized test name + nanos>)`, registers `Close` on cleanup, and returns `New(client)`. `uniqueUID` keeps the current behaviour (`"test-"+name+"-"+time`).

- [ ] **Step 5: CI.** `api-ci.yml` adds `actions/setup-node@v4` (node 20), `actions/setup-java@v4` (temurin 21), and `npm i -g firebase-tools`. Replace `go test ./...` with `firebase emulators:exec --project demo-lineup --only firestore --config ../firebase.json "go test ./..."` (working directory `api`). Add `firebase.json` and `firestore.*` to the workflow's `paths` trigger.

- [ ] **Step 6: Run, expect pass**
  Run: `cd api && firebase emulators:exec --project demo-lineup --only firestore --config ../firebase.json "go test ./internal/docstore/ ./internal/firestorerules/"`
  Expected: PASS, with the rules test actually executed (not skipped; check with `-v`).

- [ ] **Step 7: Commit** `test(docstore): firestore emulator harness, deny-all rules, ci`

### Task 2: Users

**Files:** Create `api/internal/docstore/users.go`, `users_test.go` (port of `store/users_test.go`)

**Interfaces:**
- Produces:
  - `type User struct { ID string \`json:"id"\`; Email string \`json:"email"\`; DisplayName string \`json:"display_name"\`; Region string \`json:"region"\`; SchedulePrefs json.RawMessage \`json:"schedule_prefs"\` }`
  - `func (s *Store) UpsertUserByFirebaseUID(ctx context.Context, uid, email, displayName string, defaultPrefs json.RawMessage) (*User, error)`
  - `func (s *Store) UpdateUserPrefs(ctx context.Context, userID string, region *string, prefsJSON json.RawMessage) (*User, error)`

- [ ] **Step 1: Port** `TestUpsertUserByFirebaseUID` and `TestUpdateUserPrefs` with the same assertions. Add: `ID == uid`; re-upsert with a new email/displayName refreshes them but keeps region and prefs; `json.Marshal(user)` contains `"id":"<uid>"`.
- [ ] **Step 2: Run, expect failure** (undefined methods), using the emulator command from Task 1, scoped to `./internal/docstore/ -run User`.
- [ ] **Step 3: Implement.** Do get-or-create in a transaction on `users/{uid}` with fields `email, displayName, region ("US"), schedulePrefs (map from defaultPrefs), createdAt, updatedAt`. `UpdateUserPrefs` updates only the non-nil fields plus `updatedAt`, and returns an error (wrapped) when the user doc is missing.
- [ ] **Step 4: Run, expect pass.**
- [ ] **Step 5: Commit** `feat(docstore): users`

### Task 3: Titles and providers

**Files:** Create `api/internal/docstore/titles.go`, `titles_test.go` (port of `store/titles_test.go`)

**Interfaces:**
- Produces (same fields and JSON tags as `store/titles.go`, except ID types):
  - `Title{ID string; TMDBID int64; Kind; TVMazeID *int64; Name; Overview; PosterPath; RuntimeMinutes; Airing; RefreshedAt, ProvidersRefreshedAt, AiringsRefreshedAt time.Time}`
  - `TitleUpsert`, `SeasonRow`, `ProviderRow{ID int64; Name; LogoPath}`, `AiringRow`: unchanged shape
  - `TitleFull{Title; Seasons []SeasonRow; Providers []ProviderRow; Entry *Entry}` (`Entry` comes from Task 4; declare `TitleFull` there if needed to avoid a forward reference)
  - `func TitleID(kind string, tmdbID int64) string` returns `kind+"-"+strconv.FormatInt(tmdbID,10)`
  - `UpsertTitle(ctx, u TitleUpsert) (*Title, error)`
  - `GetTitleByTMDB(ctx, kind string, tmdbID int64) (*Title, error)` (nil, nil when absent, as today)
  - `ReplaceSeasons(ctx, titleID string, seasons []SeasonRow) error`
  - `ReplaceProviders(ctx, titleID, region string, provs []ProviderRow) error`
  - `ReplaceFutureAirings(ctx, titleID, today string, airs []AiringRow) error`
  - internal: `getTitles(ctx, ids []string) (map[string]*titleDoc, error)` using one `GetAll`; `titleDoc` carries `seasons map[string]int`, `airings []airing`, `providers map[string][]int64`

- [ ] **Step 1: Port** `TestUpsertTitleInsertAndRefresh`, `TestGetTitleByTMDBAbsent`, `TestReplaceSeasons`, `TestReplaceProvidersRegionScoped`, `TestReplaceFutureAirings`, keeping the assertions. Replace SQL reads with reads through the public methods or `s.c.Doc(...)`. Add `TestReplaceProvidersKeepsProviderDictionary`: after `ReplaceProviders(t, "US", [{500,"P500",...}])` and then `ReplaceProviders(t, "US", [])`, `providers/500` still exists with name `P500`.
- [ ] **Step 2: Run, expect failure.**
- [ ] **Step 3: Implement** per the spec's `titles/{kind}-{tmdbId}` and `providers/{providerId}` sections. `UpsertTitle` preserves `providersRefreshedAt`/`airingsRefreshedAt` on refresh, as today. `ReplaceProviders` writes the title's `providers.<region>` (IDs sorted ascending), sets `providersRefreshedAt`, and upserts each `providers/{id}` in one transaction or batch.
- [ ] **Step 4: Run, expect pass.**
- [ ] **Step 5: Commit** `feat(docstore): titles and providers`

### Task 4: Entries, shelves, indexes, GetTitleFull

**Files:** Create `api/internal/docstore/entries.go`, `entries_test.go` (port), `indexes_test.go`; modify `firestore.indexes.json`, `titles.go` (`GetTitleFull`), `titles_test.go`

**Interfaces:**
- Produces:
  - `Pointer{Season, Episode int}`; `Entry` with the same fields and tags as today but `TitleID string`; `EntryUpdate` unchanged
  - `UpsertEntry(ctx, userID, titleID string, u EntryUpdate) (*Entry, error)` returns `ErrTitleNotFound` when `titles/{titleID}` is missing or `titleID` is invalid
  - `CountRotation(ctx, userID, excludeTitleID string) (int, error)`
  - `Shelf(ctx, userID, shelf string) ([]Entry, error)` (shelf values as today)
  - `GetTitleFull(ctx, userID, titleID, region string) (*TitleFull, error)`

- [ ] **Step 1: Port** `TestUpsertEntryLifecycle`, `TestCountRotationExcludes`, `TestShelfFiltersAndOrdering`, `TestGetTitleFullEntryAndEmptySlices`, keeping the assertions. Seed via `UpsertTitle` and `UpsertUserByFirebaseUID`. Add `TestUpsertEntryUnknownTitle` (`ErrTitleNotFound`).
- [ ] **Step 2: Write `TestIndexesCoverShelfQueries`.** Parse `../../../firestore.indexes.json` and assert that collection group `entries`, scope `COLLECTION`, has exactly these composite indexes: `[status ASC, addedAt DESC]`, `[favorite ASC, addedAt DESC]`, `[rating DESC, addedAt DESC]`.
- [ ] **Step 3: Run, expect failure.**
- [ ] **Step 4: Implement.**
  - `UpsertEntry` is a transaction: read the title doc (missing means `ErrTitleNotFound`) and the entry; create it with defaults (`status "none"`, `favorite false`, pointer 1/1, `addedAt` now) or apply the PATCH. `ClearRating` deletes `rating`; setting status to `watched` sets `watchedAt`.
  - `Shelf` uses the three query shapes from the spec, then joins the title fields from one `getTitles` call.
  - `CountRotation` uses a count aggregation.
  - `GetTitleFull` returns seasons sorted by number and the region's providers sorted by name (hydrated from `providers/{id}`), with empty slices rather than nil.
  - Write the three indexes into `firestore.indexes.json`.
- [ ] **Step 5: Run, expect pass.**
- [ ] **Step 6: Commit** `feat(docstore): entries, shelves, and indexes`

### Task 5: Engine string IDs; guide create, read, replace, lookups

**Files:** Modify `api/internal/guide/guide.go`, `guide_test.go`; create `api/internal/docstore/guides.go`, `guides_test.go` (port, part 1)

**Interfaces:**
- Consumes: `getTitles` (Task 3), entry docs (Task 4), `newItemID` and `validID` (Task 1)
- Produces:
  - `guide.Title.ID string`, `guide.Item.TitleID string` (`Provider`/`Providers` stay `int64`); engine behaviour unchanged, since its `<` sorts already work on strings
  - `Guide{ID string; StartDate, EndDate string; Seed int64; Items []GuideItem}` with today's JSON tags
  - `GuideItem{ID string; Date; StartMin; EndMin; TitleID string; Season; Episode; ProviderID int64; IsPlan; Pinned; Edited; Watched}` with today's JSON tags
  - `TitleLookup` unchanged
  - `GuideInputTitles(ctx, userID, region string) ([]guide.Title, error)` (sorted by title ID)
  - `CreateGuideReplacingOverlaps(ctx, userID, startDate, endDate string, seed int64, items []guide.Item) (*Guide, error)`
  - `GuideWithItems(ctx, userID, guideID string) (*Guide, error)`
  - `CurrentGuide(ctx, userID, today string) (*Guide, error)`
  - `ReplaceUnkeptItems(ctx, userID, guideID string, keepIDs []string, newItems []guide.Item, seed int64) (*Guide, error)`
  - `GuideLookups(ctx, items []GuideItem) (map[string]TitleLookup, map[int64]ProviderRow, error)` (signature change: takes the already-loaded items, so there is no re-read and no ownership concern)
  - internal: `sortItems([]GuideItem)` by `date, startMin, isPlan desc, titleID`; `defaultRuntime(kind string, raw int) int` (copy as-is)

- [ ] **Step 1: Engine.** Change the ID types; update `guide_test.go` literals to strings. Run `cd api && go test ./internal/guide/`. Expected: PASS with no assertion changes beyond the literals.
- [ ] **Step 2: Port** `TestGuideInputTitles`, `TestGuideInputTitlesDefaultRuntime`, `TestGuideLifecycle`, `TestReplaceUnkeptItems`, `TestCreateGuideOverlapTouchAdjacency`, `TestGuideLookups`. Add `TestReplaceUnkeptItemsIgnoresUnknownKeepIDs` (keep `["nope"]` with 2 new items gives exactly the 2 new items, no error). Add `TestCurrentGuidePicksNewest` (two guides covering today; returns the later `createdAt`).
- [ ] **Step 3: Run, expect failure.** Run with the emulator, scoped to `./internal/docstore/ ./internal/guide/`.
- [ ] **Step 4: Implement** per the spec's Operations mapping (transactions read before writing; overlap = `endDate >= start` query, then filter `startDate <= end` in Go). Every new item gets `newItemID()`, regenerated if it collides within the guide.
- [ ] **Step 5: Run, expect pass** (same scoped command).
- [ ] **Step 6: Commit** `feat(docstore): guides create, read, and replace; engine string ids`

### Task 6: Guide item mutations and swap

**Files:** Create `api/internal/docstore/guide_items.go`, `guide_items_test.go` (port)

**Interfaces:**
- Produces:
  - `GuideItemUpdate`: today's fields, with `TitleID *string`, `SwapProviders []int64`
  - `SwapInfo{TitleID string; Kind string; Runtime int; PointerSeason, PointerEpisode int; Providers []int64}`
  - `UpdateGuideItem(ctx, userID, guideID, itemID string, upd GuideItemUpdate) (*GuideItem, error)`
  - `DeleteGuideItem(ctx, userID, guideID, itemID string) error`
  - `SwapTitle(ctx, userID, titleID, region string) (*SwapInfo, error)` (`ErrTitleNotFound` unless the entry status is rotation or watchlist)

- [ ] **Step 1: Port** `TestUpdateDeleteGuideItem`, `TestSwapTitle`, `TestSwapTitleAppliesRuntimeDefault`, `TestUpdateGuideItemSwapProvider`, `TestUpdateGuideItemSwapClearsShadowedAlternate`, `TestUpdateGuideItemSwapClearsAlternateDateWide`, `TestUpdateGuideItemPastMove`, `TestUpdateGuideItemMoveNotFound`.
- [ ] **Step 2: Write `TestConcurrentItemUpdatesBothApply`.** One guide with items A and B; two goroutines concurrently `UpdateGuideItem` A to `Pinned=true` and B to `StartMin+30`; afterwards `GuideWithItems` shows both changes.
- [ ] **Step 3: Run, expect failure.**
- [ ] **Step 4: Implement** per the spec; all semantics match the SQL in `store/guides.go` (`UpdateGuideItem` comment block and `COALESCE` logic), done in Go on the item inside one transaction.
- [ ] **Step 5: Run, expect pass.**
- [ ] **Step 6: Commit** `feat(docstore): guide item updates, deletes, and swaps`

### Task 7: Watched state and isolation

**Files:** Create `api/internal/docstore/watched.go`, `watched_test.go` (port), `isolation_test.go` (port)

**Interfaces:**
- Produces: `nextPointer(itemSeason, itemEpisode int, counts map[int]int) (Pointer, bool)` (copy as-is), `MarkItemWatched(ctx, userID, guideID, itemID string) (*GuideItem, error)`, `UnmarkItemWatched(ctx, userID, guideID, itemID string) (*GuideItem, error)`

- [ ] **Step 1: Port** `TestNextPointer`, `TestMarkItemWatched`, `TestMarkItemWatchedExactPointerAdvance`, `TestUnmarkItemWatched` (all subtests), `TestGuideCrossUserIsolation` (every operation, user B against user A's guide, item and title IDs gives `ErrGuideNotFound`/`ErrTitleNotFound` and A's data is unchanged).
- [ ] **Step 2: Write** `TestMarkItemWatchedWithoutEntry` (series item with no entry doc: the item becomes watched, and the entry doc still doesn't exist afterwards; same for unmark) and `TestInvalidIDsAreNotFound`. For guide/item/title IDs `""`, `"a/b"` and `"__x__"`, every method that takes them returns `ErrGuideNotFound` (or `ErrTitleNotFound` for title-keyed methods) without panicking.
- [ ] **Step 3: Run, expect failure.**
- [ ] **Step 4: Implement** per the spec: a single transaction that reads the guide, entry and title, then writes the guide and entry. Add a `validID` guard at the top of every exported method that takes an ID (back-fill Tasks 2 to 6).
- [ ] **Step 5: Run, expect pass.**
- [ ] **Step 6: Commit** `feat(docstore): watched state; isolation and invalid-id guards`

### Task 8: Cutover: handlers, config, main; delete Postgres

**Files:** Modify `api/internal/httpserver/*.go` and `*_test.go`, `api/internal/config/config.go` and `config_test.go`, `api/cmd/api/main.go`, `api/internal/fbauth/fbauth.go`, `api/internal/ingest/ingest.go` and `ingest_test.go`, `api/go.mod`/`go.sum`; delete `api/internal/store/`

**Interfaces:**
- Consumes: everything in `docstore`
- Produces: `httpserver.Deps` without the `Store` field; handler interfaces with string IDs and docstore types; `GuideStore.GuideLookups(ctx, items []docstore.GuideItem)`; `func pathID(r *http.Request, name string) (string, bool)` (ok only when `validID`)

- [ ] **Step 1: Update handler tests and fakes** to string IDs and docstore types. Add `TestPathIDRejectsInvalid`: routes called with an `{id}` of `__x__` (and `%2F`-encoded slash) return 404.
- [ ] **Step 2: Update handlers.** Replace `strconv.ParseInt` ID parsing with `pathID`, keeping `tmdbID` parsing numeric. Change `ingest.TitleStore` signatures to string title IDs.
- [ ] **Step 3: Config and main.** `Config` drops `DatabaseURL` and `ErrMissingDatabaseURL` (update `config_test.go` and `envKeys`). `main.go` builds one `firebase.NewApp(ctx, &firebase.Config{ProjectID: cfg.FirebaseProjectID})`, gets `app.Firestore(ctx)` (closed on shutdown), wraps it in `docstore.New`, and runs no migrations. Change `fbauth.New` to accept the existing `*firebase.App` so one app is shared.
- [ ] **Step 4: Delete** `api/internal/store/`, run `go mod tidy` (removes `pgx`, `golang-migrate`, `pgerrcode`). Migrations are embedded, so `api/Dockerfile` needs no change.
- [ ] **Step 5: Verify.**
  Run: `cd api && go vet ./... && firebase emulators:exec --project demo-lineup --only firestore --config ../firebase.json "go test ./..."`
  Expected: vet clean; all packages PASS.
  Run: `grep -rn "pgx\|DATABASE_URL\|golang-migrate" api/`
  Expected: no output.
- [ ] **Step 6: Commit** `refactor(api)!: switch to firestore docstore and remove postgres`

### Task 9: Web string IDs

**Files:** Modify `web/src/lib/types.ts`, `web/src/lib/guide.ts`, `web/src/lib/guide.test.ts`, `web/src/app/guide/{BoardView.tsx,CalendarView.tsx,useGuideItemDrag.ts,useGuideItemDrag.test.ts,useGuideItemMutations.ts,SlotQuickActions.tsx,usePosterHue.ts,posterTint.ts,posterTint.test.ts}`, plus any other file `tsc` flags

**Interfaces:**
- Produces: `hashHue(titleId: string): number` in `0..359`; `usePosterHue(titleId: string, posterPath: string)`; `peekPosterHue(titleId: string)`. Provider-keyed functions keep `number`.

- [ ] **Step 1: Update `posterTint.test.ts`.** `hashHue("series-1399")` is stable across calls and in `[0, 360)`; `hashHue("series-1399") !== hashHue("series-1400")`; it equals a pinned value computed by FNV-1a 32-bit of the UTF-8 string, mod 360 (compute the constant once in the test and hard-code it).
- [ ] **Step 2: Run, expect failure.** Run: `cd web && pnpm run test`
- [ ] **Step 3: Implement.** In `types.ts`, `User.id`, `Title.id`, `Entry.title_id`, guide/item `id` and `title_id`, and the lookup map keys for titles become `string`; `tmdb_id`, `provider_id` and `ProviderRow.id` stay `number`. Implement FNV-1a in `hashHue` and drop the BigInt comment. In `CalendarView.tsx:188`, replace `Number(event.active.id)` with `String(event.active.id)`. Fix the remaining type errors.
- [ ] **Step 4: Verify.** Run: `cd web && pnpm run lint && pnpm run test && pnpm run build`. Expected: all pass.
- [ ] **Step 5: Commit** `feat(web): string ids for titles, guides, and items`

### Task 10: Infra and docs

**Files:** Modify `infra/bootstrap.sh`, `infra/run-service.yaml`, `infra/README.md`, `infra/local-dev.md`, `README.md`

- [ ] **Step 1: `bootstrap.sh`.**
  - Enable `firestore.googleapis.com`.
  - If `gcloud firestore databases describe --database="(default)"` fails, create it with `--location=us-central1 --type=firestore-native`.
  - Grant `roles/datastore.user` to `api-runtime` (in the existing role loop).
- [ ] **Step 2: `run-service.yaml`.** Remove the `DATABASE_URL` env entry and its transitional comment.
- [ ] **Step 3: Docs.**
  - `infra/README.md`: drop the migration-in-progress note; list Firestore in what bootstrap provisions; add `firebase deploy --only firestore --project lineup-app-ae6b` to re-provisioning.
  - `local-dev.md`: replace the Docker Postgres sections with `firebase emulators:start --only auth,firestore --project demo-lineup` and the API env `FIRESTORE_EMULATOR_HOST=localhost:8085 FIREBASE_AUTH_EMULATOR_HOST=localhost:9099 FIREBASE_PROJECT_ID=demo-lineup`; the store test command becomes the Task 8 emulators:exec command.
  - Root `README.md` stack: `Postgres` becomes `Cloud Firestore`.
- [ ] **Step 4: Verify.** Run: `bash -n infra/bootstrap.sh && python3 -c "import yaml;yaml.safe_load(open('infra/run-service.yaml'))" && grep -rn -i "postgres\|DATABASE_URL\|database-url" infra README.md`. Expected: syntax OK; grep shows no references.
- [ ] **Step 5: Commit** `chore(infra): provision firestore in bootstrap; drop postgres docs`

### Task 11: Rollout (needs the owner for the final step)

- [ ] **Step 1: Deploy rules and indexes.** Run `firebase deploy --only firestore --project lineup-app-ae6b`. Expected: rules unchanged, 3 indexes created. Then `gcloud firestore indexes composite list --project lineup-app-ae6b` shows them `READY` (wait if `CREATING`).
- [ ] **Step 2: Open the PR** against `main` (stacked on #61) and link it to the thread. CI green, including store tests under the emulator.
- [ ] **Step 3: After merge, deploy the API.** From repo root on the merged `main`, run `gcloud builds submit --config=infra/cloudbuild.yaml --substitutions=SHORT_SHA=$(git rev-parse --short HEAD) --project lineup-app-ae6b .` (the manual pipeline run in `infra/README.md`). Expected: release created; the Cloud Deploy rollout succeeds.
- [ ] **Step 4: Smoke test.** `curl https://lineup-api-zzwkjc5sdq-uc.a.run.app/healthz` returns 200 with `{"ok":true}`. Revision logs show no Firestore permission errors. An unauthenticated `/v1/me` returns 401.
- [ ] **Step 5: Ask the owner to confirm, then delete the legacy secrets** `database-url`, `db-password` and `tmdb-api-key` (`gcloud secrets delete <name> --project lineup-app-ae6b -q`). Confirm with `gcloud secrets list` that only `tmdb-read-token` and the GitHub OAuth secret remain.
