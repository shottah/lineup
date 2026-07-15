# Web profile shelves + settings (issue #17) — design

Date: 2026-07-15
Status: approved (user: "looking good" after sectioned walkthrough)
Issue: [#17 feat(web): profile shelves and settings](https://github.com/shottah/lineup/issues/17)

## Context

Builds directly on #16's foundations: `Entry`/`TitleFull` types, `TitleCard`,
`StarRating`, `useToast`, and the `["shelf"]` invalidation prefix that
`EntryActions` already fires. Two API contract facts shape the design:
`PATCH /v1/me` accepts only a FULL `schedule_prefs` document (exactly seven
lowercase day keys, zero-padded `HH:MM`, start strictly before end — see
`api/internal/prefs`), and the shelf `Entry` JSON carries no `tmdb_id`, so
shelf cards cannot link to `/title/{kind}/{tmdbId}` without a small API
addition. Branch `feat/17-web-profile-settings`, squash-merge.

## Design

### API prep (one commit, before any web work)

Add `tmdb_id` to the entry payload: `t.tmdb_id` joins into `entryColumns`
(the query already joins `titles`), `scanEntry` and the `Entry` struct gain
the field (`json:"tmdb_id"`), the httpserver `fakeEntries` mirrors it, and
existing entry/shelf tests assert it. Backward-compatible addition; guide
code untouched. TS `Entry` gains `tmdb_id: number`.

### TitleCard prop narrowing

`TitleCard`'s prop becomes the subset it renders:

```ts
export type TitleCardData = {
  tmdb_id: number;
  kind: "movie" | "series";
  name: string;
  poster_path: string;
  year: string; // "" hides the year segment
};
```

`SearchResult` satisfies it structurally — search callers unchanged.
Shelves map `Entry` → `TitleCardData` with `year: ""`. When `year` is
empty the card's meta line shows the kind only (no dash placeholder).
`TitleCard` also gains an optional `badge?: string` rendered as a small
pill over the poster (rotation's next-episode + ratings' value use it).

### `/profile`

`AuthGate` + `Nav` + `ProfileBody` (client). Tab bar — Watchlist ·
In Rotation · Watched · Favorites · Ratings — as local state, default
Watchlist. The active tab renders `ShelfGrid(shelf)`:

- `useQuery({ queryKey: ["shelf", shelf], queryFn: () =>
  api<ShelfResponse>(`/v1/me/shelves/${shelf}`) })` — only the active
  tab's query mounts, so inactive shelves never fetch until visited.
  `ShelfResponse = { entries: Entry[] }` (new TS type).
- Grid of `TitleCard`s (same responsive grid classes as search).
- Rotation tab extras: a capacity meter line above the grid — "n of 8
  rotation slots used" — and, on series cards, `badge` = "Next:
  S{pointer.season}E{pointer.episode}". Movies get no badge.
- Ratings tab: `badge` = the rating value ("4.5★").
- Per-shelf empty states: watchlist "Nothing on your watchlist yet — find
  something in Search" (Search is a link); rotation "Promote watchlist
  titles to build your weekly lineup"; watched/favorites/ratings short
  one-liners in the same voice.
- Pending → same muted "Loading…" line as elsewhere; error → "Couldn't
  load this shelf."

### `/settings`

`AuthGate` + `Nav` + `SettingsBody` (client). Seeded from the existing
`["me"]` query (`api<User>("/v1/me")`); renders a controlled form once
loaded:

- Region `<select>` over a curated list — US, GB, CA, AU, DE, FR, ES, IT,
  NL, SE, BR, MX, JP, KR, IN — with the user's current region selected
  even if it's not in the list (prepended when missing).
- Seven day rows in mon–sun order, each: label (Mon…Sun), enabled
  checkbox, start/end `<input type="time">` (native zero-padded 24h
  matches the API format). Time inputs disabled when the day is disabled.
- Client validation, live: EVERY day (enabled or not) must have
  start < end — `prefs.Validate` server-side enforces it on all seven
  rows regardless of the enabled flag (prefs.go:67-69), so the client
  mirrors that (string compare works for zero-padded HH:MM). Violations
  show an inline red note on the row and disable Save.
- Save: one `useMutation` → `PATCH /v1/me` with
  `{ region, schedule_prefs: { windows: {...all seven days...} } }` (full
  document, per contract). Success → toast "Settings saved", invalidate
  `["me"]`. Error → toast "Couldn't save — try again.". Save disabled
  while pending. Disabled days keep their last times and are sent as-is
  (they still validate, per the rule above).
- Nav gains Profile and Settings links after Search.

### Ride-along polish (ledgered from #16's final review)

- Search query: `placeholderData: keepPreviousData` (import from
  `@tanstack/react-query`) and `retry` capped like the title query (≤2).
- Title page meta line: hide "0 min" (movie with unknown runtime → just
  "Movie") and "0 seasons" (→ just "Series").

### Verification

`pnpm lint` + `pnpm build` green; api tests green (the tmdb_id addition
touches entries tests). Manual acceptance (user, live stack): shelves
show the existing test data (8 rotated titles incl. next-episode badges
and the capacity meter at 8 of 8; the 4.5 rating on Ratings), entry
actions on a title page reflect into shelves without reload, settings
edits persist across reload, invalid start/end blocks Save.

## Out of scope

Guide views (#18); URL-addressable profile tabs; per-day partial prefs
PATCH (API takes full documents); region free-text entry; drag-to-reorder
shelves; pagination (shelves are small in v1).
