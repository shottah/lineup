# Guide endpoints (issue #14) — design

Date: 2026-07-09
Status: approved (user: "proceed", after design presentation)
Issue: [#14 feat(api): guide endpoints](https://github.com/shottah/lineup/issues/14)

## Context

Wires the pure engine (#13) to Postgres and HTTP. Everything upstream
exists on `main`: engine, shelves/entries (#12), auth (#8), TVMaze (#10).
Fully local build. Branch `feat/14-guide-endpoints`, squash-merge.

Pre-ingestion reality (†): `title_providers` is empty until #11 lands, so
hydrated titles carry no providers and the engine schedules nothing —
creating a guide legitimately yields zero items. Tests seed providers
directly; production behaves once ingestion populates them.

## Layering decision (†)

`store` imports `internal/guide` (pure types, zero deps) — hydration
returns `[]guide.Title` and persistence accepts `[]guide.Item` directly,
avoiding a parallel mapping layer. The engine stays import-free.

## Store (`store/guides.go`), all ownership-scoped (other users' rows → ErrNotFound)

- `Guide{ID, StartDate, EndDate, Seed, Items []GuideItem}` /
  `GuideItem{ID, Date, StartMin, EndMin, TitleID, Season, Episode,
  ProviderID, IsPlan, Pinned, Edited, Watched}` — dates as "YYYY-MM-DD"
  strings end-to-end (`to_char` in SQL).
- `GuideInputTitles(userID, region)` — rotation entries → `[]guide.Title`:
  titles joined with region-filtered `title_providers`, `title_seasons`
  (SeasonEpisodes), `title_airings` (AirDates, airing titles), pointer from
  `user_titles`.
- `CreateGuideReplacingOverlaps(userID, start, end, seed, items)` — tx:
  delete the user's guides overlapping [start,end], insert guide, bulk
  insert items, return persisted guide.
- `CurrentGuide(userID, today)` — newest guide with `end_date >= today`.
- `GuideWithItems(userID, guideID)`.
- `ReplaceUnkeptItems(userID, guideID, keepIDs, newItems)` — tx: delete the
  guide's items NOT in keepIDs, insert newItems, return refreshed guide.
- `UpdateGuideItem(userID, itemID, upd)` — move/swap/pin partial update.
- `DeleteGuideItem(userID, itemID)`.
- `MarkItemWatched(userID, itemID)` — tx: set `watched`; series: advance
  `user_titles` pointer to the episode after the item's, rolling seasons
  via `title_seasons`; past the finale → title auto-moves to the `watched`
  shelf (status + `watched_at`, #12 semantics). Movies → watched shelf
  immediately. Pointer never regresses: advancement only applies when the
  item's episode is ≥ the current pointer (†).
- `SwapTitle(userID, titleID)` — validates membership in rotation OR
  watchlist; returns kind, runtime, and the user's pointer (a swapped-in
  series lands on the user's next episode †; movies 0/0).

## Endpoints (inside `/v1`, RequireAuth; errors in the house style)

- `POST /v1/guides` `{start_date, days}` — days 1–14 else 422; ISO date
  else 422. Hydrate → prefs windows → `[]guide.Day` (dates mapped to
  weekday keys) → `Generate` with seed = `Now().UnixNano()` → persist with
  overlap replacement → 201 guide JSON.
- `GET /v1/guides/current` — 404 `not_found` when none current.
- `POST /v1/guides/{id}/regenerate` — keep set = items `pinned OR edited
  OR watched OR date < today`; fresh hydration; `Generate` with the
  guide's stored seed and keeps mapped to `guide.Item`; engine output
  minus the echoed keeps becomes the new item set. 200 refreshed guide.
- `PATCH /v1/guides/{id}/items/{itemID}` `{date?, start_min?, title_id?,
  pinned?}` — move (date/start) and swap (title_id) set `edited`;
  pin-only does not. Swap validates via `SwapTitle`, recomputes
  `end_min = start_min + runtime`, sets season/episode to the swapped
  title's pointer (series) or 0/0 (movie). Unknown/foreign item → 404;
  swap target not in rotation/watchlist → 422 `invalid title`.
- `DELETE /v1/guides/{id}/items/{itemID}` → 204.
- `POST /v1/guides/{id}/items/{itemID}/watched` → 200 updated item;
  triggers the pointer logic above.

## Clock seam (†)

`Deps.Now func() time.Time` (default `time.Now`) supplies today's date
(UTC, `YYYY-MM-DD`) for current-guide, past-item keeps, and seeds. Tests
inject a fixed clock.

## Prefs helper

`prefs.Windows(raw) (map[string]prefs.ParsedWindow, error)` in the prefs
package (owns the JSON shape): weekday key → `{Enabled, StartMin, EndMin}`
with HH:MM→minutes conversion. Handlers map calendar dates to weekday keys
(Go `time.Weekday` → mon..sun) and build `[]guide.Day`; disabled or absent
weekdays become zero-Window days. A user with `{}` prefs (pre-#8 default
rows) gets all days disabled → empty guide, not an error (†).

## Testing

- Hermetic handler tests (fake GuideStore + fixed clock): ownership 404s,
  create validation, regenerate keep-set assembly (pinned/edited/watched/
  past kept; future unpinned replaced), swap end_min recompute + 422,
  pin-only not edited, watched → pointer advance / rollover / past-finale
  auto-complete (fake mirrors store semantics), current 404.
- Store integration tests (TEST_DATABASE_URL): hydration shape (providers
  region-filtered, airings only for airing titles), overlap replacement,
  ReplaceUnkeptItems, MarkItemWatched rollover + auto-complete against
  real `title_seasons`, ownership isolation between two users.

## Out of scope

Ingestion (#11), alternates-from-watchlist hydration (recorded #13
divergence — alternates still come from the rotation pool), web guide UI
(#18), timezone-aware "today" (server UTC in v1 †).
