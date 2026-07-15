# Web guide views (issue #18) — design

Date: 2026-07-15
Status: approved (user: "write it up" after sectioned walkthrough; two
Q&A decisions recorded below)
Issue: [#18 feat(web): guide calendar and board views with editing](https://github.com/shottah/lineup/issues/18)
Visual reference: the `isGuide` blocks of `docs/design/lineup-app.dc.html`
(reference wins visual disagreements) plus the copy inventory in
`docs/superpowers/specs/2026-07-15-design-reskin-design.md`.

## Context

The last feature of v1. All seven guide endpoints are live; the guide
engine, design tokens, `Segmented`, and toast all shipped. Two user
decisions from brainstorming: the calendar uses the reference's STACKED
slot cards (not the issue text's time-positioned blocks), and the watched
toast is the simple `Watched · {title}` (no API pointer addition).
Branch `feat/18-web-guide-views`, squash-merge.

## Design

### API enrichment (one commit, before web work)

Guide JSON currently carries bare `title_id`/`provider_id` — unrenderable.
The three guide-returning endpoints (`POST /v1/guides`,
`GET /v1/guides/current`, `POST /v1/guides/{id}/regenerate`) gain two
sidecar maps composed in the handler:

```json
{ "id": …, "start_date": …, "end_date": …, "seed": …, "items": […],
  "titles":    { "<title_id>":    { "name": "…", "kind": "movie|series", "tmdb_id": 123 } },
  "providers": { "<provider_id>": { "name": "…", "logo_path": "…" } } }
```

Store: `GuideLookups(ctx, guideID) (map[int64]TitleLookup, map[int64]ProviderRow, error)`
— two queries over the guide's distinct title/provider ids
(`TitleLookup{Name, Kind string; TMDBID int64}`; reuse `ProviderRow`).
Handler: a `guideResponse` wrapper struct embedding `store.Guide` plus the
two maps (JSON object keys are the ids as strings — Go marshals
`map[int64]…` that way natively). Item-level endpoints unchanged. Tests:
handler test asserts the sidecars resolve every item's ids; store
integration test for `GuideLookups`.

### Web data flow

One query: `["guide"]` → `GET /v1/guides/current` (`ApiError` 404 → the
GenerateBar state, not an error state; other errors → token error line,
cached-data-first guard as house pattern). EVERY mutation invalidates
`["guide"]`; mark-watched ALSO invalidates the `["shelf"]` prefix
(ledgered #17 constraint). Create/regenerate responses are not consumed —
invalidation refetches current. Swap pickers reuse the existing
`["shelf", "rotation"]` and `["shelf", "watchlist"]` queries.

TS types (`lib/types.ts`): `GuideItem` (mirror of the Go JSON incl.
`is_plan/pinned/edited/watched`), `Guide`, `GuideTitleLookup`,
`GuideResponse` (guide + `titles`/`providers` as `Record<string, …>`).

### `lib/guide.ts` — pure mappers (vitest-covered)

- `fmtTime(startMin: number): string` — minutes-from-midnight →
  `"8:00 pm"` (12-hour, lowercase am/pm, no leading zero on hours,
  `12:00 pm` for 720, `12:00 am` for 0).
- `toCalendarColumns(g: GuideResponse, today: string): CalendarColumn[]`
  — one column per date in `[start_date, end_date]` (7 typical): `date`,
  `dow` label (`MON`), `sub` (`Jul 20`-style, or `Tonight` when
  `date === today`), `isToday`, `slots`: PLAN items only
  (`is_plan === true`), sorted by `start_min`, each resolved via the
  sidecars into `{item, title, kind, tmdbId, providerName, timeLabel}`.
- `toBoardRows(g: GuideResponse, date: string): BoardDay` — for one date:
  `times`: sorted distinct plan-item start hours (label via `fmtTime` of
  the hour); `rows`: one per provider that has any cell, each cell either
  a plan pick (`step`: 1-based index of that item in the day's
  time-sorted plan sequence) or an alternate (`is_plan === false`) whose
  cell sits in the hour column of its own `start_min`; empty
  `{has:false}` cells elsewhere. `path`: the day's plan titles joined
  ` → ` (for `Your path: …`), empty day → `[]` rows and empty path.
- Mappers never touch Date objects beyond string comparison (dates are
  `YYYY-MM-DD` strings; day-of-week derived via `Date.UTC` parse — the
  one permitted Date use, tested).

Vitest: `devDependency` `vitest` (this issue's mandated infra; the ONE
allowed package.json change), script `"test": "vitest run"`,
`web/src/lib/guide.test.ts` with a realistic two-day fixture (plan +
alternates + watched + pinned + a movie), covering: fmtTime boundaries
(0, 720, 719, 1439); column count from date range; plan-only filtering
and sort; Tonight labeling; board hour derivation; step numbering across
the evening; alternate placement in its hour; path assembly; empty day.
CI: `web-ci.yml` gains `pnpm test` between lint and build.

### `/guide` page composition

`AuthGate > Nav > GuideBody > Footer` (replaces the placeholder body;
`["me"]` no longer needed there).

- **No current guide (404):** GenerateBar — a centered panel card
  (design-token styling): heading `Plan your week`, date input (default
  today), days number input 1–14 (default 7), `Generate` accent pill →
  `POST /v1/guides {start_date, days}` → invalidate `["guide"]`; 422 →
  toast `Couldn't save — try again.`-family copy (`Couldn't generate —
  check the dates.`).
- **Guide present:** header row per reference: `Week of {Month D}`
  (start_date formatted) + `N evenings planned · M nights off` (from
  calendar columns with/without slots); right side: `Segmented`
  Calendar/Board + `↻ Regenerate remaining` pill (`bg-acc-soft text-acc`)
  → `POST regenerate` → invalidate + toast `Re-planned your remaining
  evenings — watched and pinned stayed put`.

### CalendarView

Per reference: desktop `grid-cols-7 gap-2`; below `lg` a horizontal
snap-scroll row (`overflow-x-auto snap-x`, columns `min-w-[160px]
snap-start`). Day headers: `dow` 11px letter-spaced (accent when today),
sub line (`Tonight` accent / date faint). Slot cards: `bg-panel
rounded-xl`, watched → `opacity-50` and `✓ ` title prefix; body: time
label (`8:00 pm` 10px mut), title 13.5px semibold, sub `SxEy ·
{provider}` (movies: `Movie · {provider}`) 10.5px mut; `Pinned` pill
(`bg-acc-soft text-acc`) when `pinned` — ONE pill only: the API cannot
distinguish air-pins from user pins, so the reference's separate `Airs
live` treatment is a recorded v1 divergence. Empty day: dashed
`Night off` box. Card click toggles the inline ItemMenu (one open at a
time, `["#{date}-#{itemId}"]` local state).

### ItemMenu (inline action row, per reference chips)

`panel2` chip buttons, disabled while any guide mutation is pending:

- `✓ Watched` → `POST …/watched` → toast `Watched · {title}`;
  invalidates `["guide"]` AND `["shelf"]`.
- `Pin` / `Unpin` → `PATCH {pinned: !pinned}` → toast `Pinned to {DOW}` /
  `Unpinned` (pin-only PATCH does not set edited — server semantics).
- `Swap` → expands a picker listing rotation ∪ watchlist entries
  (deduped, current title excluded; from the shelf queries; loading/empty
  states inline) → `PATCH {title_id}` → toast `Swapped in {title}`.
  422 `invalid title` → toast `That title can't be swapped in.`
- `Move` → expands day `<select>` (guide dates, DOW labels) + native time
  input (defaults to the item's current values) + `Move` chip →
  `PATCH {date, start_min}` (start_min = HH*60+MM) → toast `Moved`.
- `Details` → `router.push('/title/{kind}/{tmdbId}')` via sidecar.
- `Remove` → `DELETE` → toast `Removed — enjoy the free hour`.

All mutations `onSettled` → invalidate `["guide"]` (watched additionally
`["shelf"]`); errors → generic `Couldn't save — try again.` toast except
the mapped 422 above.

### BoardView

Per reference: day chips (pill row, active ink/bg; defaults to today when
in range, else first day); `{Weekday} evening` heading + `Your path: A →
B → C` (or `Night off — nothing planned`); CSS grid `[120px repeat(n,1fr)]`
— n = the mapper's hour columns; provider name cells 12px semibold; plan
cells `bg-panel border-acc` with the floating step circle
(`bg-acc text-acc-ink`, absolute -top-2) and sub `SxEy · your pick`;
alternate cells `bg-panel2` muted, sub `SxEy · alternate`, click → swap
into that hour's plan item (`PATCH {title_id}` on the plan item sharing
the timeslot — engine guarantees one exists) with the same toasts as
Swap. Caption: `Numbered cards are your evening · tap any alternate to
swap it in`. Mobile: the grid scrolls horizontally inside its own
container.

### Verification

`pnpm lint && pnpm test && pnpm build` green (CI enforces all three);
Go suite green (enrichment tests). Acceptance: user's manual loop on the
live stack — generate from the real 8-title rotation, drive both views,
watched advances the pointer (rotation badge updates via shelf
invalidation), pin/swap/move/remove all round-trip, regenerate preserves
pins/watched.

## Out of scope

Air-pin vs user-pin distinction (needs an API bit; v2 candidate);
optimistic updates; drag-and-drop moving; guide history/multiple guides
UI; pointer/finale copy in the watched toast (decided against); #19's
launch-pass items.
