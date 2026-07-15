# Web search + title pages (issue #16) — design

Date: 2026-07-15
Status: approved (user: "go strong" after sectioned walkthrough)
Issue: [#16 feat(web): search and title pages](https://github.com/shottah/lineup/issues/16)

## Context

First real web feature past the #15 auth shell. The API side is complete
and live locally with real TMDB data (`/v1/search`, `/v1/titles/…`, entry
PATCH with the rotation cap). Stack: Next 16.2.10 (App Router — `params`/
`searchParams` are Promises now; see `web/AGENTS.md` and the bundled docs
under `node_modules/next/dist/docs/`), React 19, TanStack Query 5,
Tailwind v4. Branch `feat/16-web-search-title`, squash-merge.

## Design

TanStack Query client components behind thin async server wrappers —
the #15 pattern. Two deliberate simplifications: plain `<img>` for
posters (TMDB `w342` files are pre-sized; `next/image` would add config
and an optimization hop for a CDN-optimized asset), and a homegrown
~40-line toast rather than a dependency (one consumer today).

### Pages

- `web/src/app/search/page.tsx` — default export renders
  `<AuthGate><Nav /><SearchBody /></AuthGate>`. `SearchBody` is
  `"use client"`: an input whose value feeds a 300ms-debounced `q`;
  `useQuery({ queryKey: ["search", q], queryFn: () =>
  api<SearchResponse>(...), enabled: q !== "" })`. States: empty query →
  muted prompt; pending → skeleton/spinner; error → "search is
  unavailable" line; no hits → quiet empty state; hits → responsive grid
  of `TitleCard`. No URL sync of `q` in v1.
- `web/src/app/title/[kind]/[tmdbId]/page.tsx` — `async` server
  component: `const { kind, tmdbId } = await params` (Next 16), renders
  `<AuthGate><Nav /><TitleBody kind tmdbId /></AuthGate>`. `TitleBody`
  queries `["title", kind, tmdbId]` → `GET /v1/titles/{kind}/{tmdbId}`.
  `ApiError` 404 → "title not found" state; other errors → "can't reach
  the catalog right now" state. Layout: poster (`w342`), name, year-less
  overview block, runtime/kind line, providers row (logos `w92` +
  names; "not streaming in your region" when empty), `EntryActions`.
- `Nav` gains a Search link (`/search`) beside the wordmark.

### Components (`web/src/components/`)

- `TitleCard.tsx` — `{ result: SearchResult }`; poster via `posterUrl`,
  gray placeholder block when `poster_path` is empty; name, year, kind
  badge; wraps in `<Link href={/title/${kind}/${tmdb_id}}>`.
- `EntryActions.tsx` — `{ title: Title; entry: Entry | null; kind, tmdbId
  (for invalidation keys) }`. Three status buttons (Watchlist / Rotation /
  Watched) as a radio-with-toggle-off: clicking the active status PATCHes
  `{"status":"none"}`; a favorite heart toggling `{"favorite":bool}`;
  `StarRating`. All through one `useMutation` posting partial bodies to
  `PATCH /v1/titles/{title.id}/entry` — title.id is the INTERNAL id from
  the payload, never the TMDB id. Buttons disabled while in flight; no
  optimistic updates.
- `StarRating.tsx` — `{ value: number | null; onRate(v: number | null) }`.
  Five stars, half-step hit zones (10 targets, 0.5–5.0); clicking the
  current value rates `null` (clear). Pure presentational; parent owns
  the mutation.
- `Toast.tsx` + provider folded into the existing `Providers.tsx`:
  `useToast()` returning `show(message: string)`; renders bottom-center,
  auto-dismisses after 4s, one visible at a time (last wins).

### Data layer

- `lib/types.ts` additions, snake_case, mirroring API JSON verbatim:
  `SearchResult{tmdb_id, kind, name, overview, poster_path, year}`,
  `SearchResponse{results: SearchResult[]}`, `Title{id, tmdb_id, kind,
  name, overview, poster_path, runtime_minutes, airing}`,
  `SeasonRow{number, episode_count}`, `ProviderRow{id, name, logo_path}`,
  `Pointer{season, episode}`, `Entry{title_id, kind, name, poster_path,
  runtime_minutes, airing, status, rating, favorite, pointer, added_at,
  watched_at}`, `TitleFull{title, seasons, providers, entry}` (`entry`
  nullable).
- `lib/tmdb.ts` — `posterUrl(path: string, size: "w92" | "w342"): string
  | null` → `https://image.tmdb.org/t/p/{size}{path}`, null for empty
  path.

### Mutations & errors

On mutation success AND error: invalidate `["title", kind, tmdbId]` and
the `["shelf"]` prefix (the key convention #17 adopts). Error mapping in
`EntryActions`: `ApiError` 409/`rotation_full` → toast with the exact
copy "Rotation is full (8); finish something first."; anything else →
toast "Couldn't save — try again.". A null `entry` renders as status
`none`, no rating, not favorite.

### Verification

`pnpm lint` and `pnpm build` green (exactly what web CI runs). No JS
test infra exists in `web/` and none is added — the issue's acceptance
is the manual loop, run by the user against the live local stack:
search, open a title, watchlist → rotate → rate 4.5 → favorite, and the
409 toast via rotating a 9th title. Render-test infra arrives with the
guide views (#18) where the v1 spec asks for it.

## Out of scope

Shelf pages and settings (#17); guide views (#18); URL-synced search
state; optimistic updates; `next/image`; pagination (API returns page 1
only); season list rendering beyond what the payload trivially offers
(the guide engine consumes seasons; the title page shows count only).
