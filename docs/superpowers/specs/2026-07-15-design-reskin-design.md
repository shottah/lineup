# Design-system reskin (issue #38) — design

Date: 2026-07-15
Status: approved (source design user-approved in Claude Design; adaptation
decisions confirmed via Q&A)
Issue: [#38 feat(web): apply Lineup design system](https://github.com/shottah/lineup/issues/38)
Reference: `docs/design/lineup-app.dc.html` (vendored from the Claude
Design project "Lineup: Appointment TV Guide" — the single source of
visual truth; when this spec and the reference disagree on a visual
detail, the reference wins)

## Scope

Reskin the six existing surfaces — chrome (nav/footer), login, search,
title, profile, settings — plus Toast, TitleCard, StarRating. NO new
product behavior beyond three approved deltas (below). The design's guide
screens (calendar + board) are #18's visual spec and are NOT built here;
the `/guide` placeholder page is restyled with tokens only. Branch
`feat/design-reskin`, squash-merge.

## Tokens (verbatim from the reference)

CSS custom properties, switched by `data-lt` on `<html>`:

| var | dark | light |
|---|---|---|
| --bg | #14161a | #f5f4f1 |
| --panel | #1e2127 | #ffffff |
| --panel2 | #191c21 | #eceae6 |
| --ink | #e9e9ec | #22232a |
| --mut | #8f929c | #5f6270 |
| --faint | #5b5e68 | #9da0ab |
| --line | rgba(233,233,236,.09) | rgba(34,35,42,.12) |
| --acc | #9d97cf | #6a63b0 |
| --accInk | #14161a | #ffffff |
| --accSoft | rgba(157,151,207,.14) | rgba(106,99,176,.11) |

Settings validation-error red: `#c96a6a` (both themes).

Exposed to Tailwind v4 via `@theme inline` in `globals.css` as colors
`bg, panel, panel2, ink, mut, faint, line, acc, acc-ink, acc-soft` (so
`bg-panel`, `text-mut`, `border-line`, `bg-acc-soft` etc. work). All
existing `zinc-*`/`dark:` utilities in app code are replaced by token
utilities; component code contains NO raw hex values (the error red
becomes a `--color-danger` token).

## Typography & primitives

- **Albert Sans** (400/500/600/700) via `next/font/google` (self-hosted
  by Next — no external `<link>`, no new dependency), wired as the app's
  sans font. Wordmark/headings: 600 weight, letter-spacing -0.01em.
- Radii: pills `rounded-full` for buttons/chips/tabs; cards 12px
  (`rounded-xl`); larger panels 14px; login card 20px.
- Focus: `outline: 2px solid var(--acc); outline-offset: 2px` on
  focus-visible, globally.
- Segmented control pattern (shared component): container `bg-panel2`
  p-[3px] rounded-full; active segment `bg-ink text-bg` 600 12px;
  inactive transparent `text-mut`.

## Theming (approved delta 1)

User-facing toggle, dark default. `data-lt="dark|light"` lives on
`<html>` (`suppressHydrationWarning`); a tiny inline script in the root
layout `<head>` applies `localStorage["lineup-theme"] ?? "dark"` before
paint (no flash). A `ThemeToggle` pill in the Nav (`☾ Dark` / `☀ Light`,
border-line, bg-panel, text-mut) flips the attribute and persists.
System preference is ignored. Tailwind `dark:` variants are removed from
app code — tokens carry both themes.

## Chrome

- **Header** (authed pages): border-b line; "Lineup" wordmark (links to
  /guide); nav pills Guide · Search · Profile · Settings — active route
  gets `bg-acc-soft text-acc`, inactive `text-mut`; right side: theme
  toggle pill + **avatar menu (approved delta 3)** — 32px circle,
  `bg-acc-soft text-acc border-line`, showing the first letter of the
  user's email; click opens a small panel (bg-panel, border-line,
  rounded-xl) with the email (text-mut, display only) and a Sign out
  item (existing sign-out flow: signOut + queryClient.clear() +
  replace /login). Menu closes on outside click and Escape.
- **Footer** (authed pages): border-t line, 11px text-faint:
  `Metadata from TMDB · Streaming availability from JustWatch · Air
  dates from TVMaze` — this lands #19's attribution requirement early.
- Login renders neither header nor footer (chrome hidden, per design).
- Page composition stays `<AuthGate><Nav/>…<Footer/></AuthGate>`; main
  content column max-w-[1280px] with 32px horizontal padding.

## Per-surface treatments

- **Login:** centered 360px card (panel, border-line, rounded-[20px]);
  CSS antenna mark (44×32 rounded rect, 2px acc border, two rotated
  2×11px aerials); "Lineup" 26px/600; tagline text-mut; full-width
  `bg-ink text-bg` pill button with a 20px circled G; footnote 11px
  text-faint: `One evening at a time. No autoplay, no feeds.` Existing
  auth logic (busy state, error line, redirect effect, appleAuth flag)
  unchanged.
- **Search:** input max-w-[560px] bg-panel border-line rounded-xl
  13px/18px padding; hint copy `Type to search — results appear as you
  type.`; empty copy `No matches. Try a different spelling.` (replaces
  the current copy); grid `repeat(auto-fill,minmax(150px,1fr))` gap 18px.
  Query behavior (debounce, keepPreviousData, retry) unchanged.
- **TitleCard:** poster rounded-xl (real TMDB images, unchanged);
  no-poster treatment per design: panel2 dashed-line box with 20px
  initials (first letters of up to 3 words) and `No poster` caption in
  the search context; badge pill top-left `bg-acc text-acc-ink` 10px/600.
  Meta line `{year} · {Kind}` capitalized ("2022 · Series"); year absent
  → kind only. Whole card is the link (existing).
- **Title page:** `← Back` text button (router.back(), falls back to
  /search) above; poster 220px rounded-[14px]; 32px/600 title; meta line
  `{year?} · Movie · 1h 46m` / `· Series · N seasons · Next: SxEy`-style
  (runtime formatted `Xh Ym` when ≥ 60 min, else `Xm`; keep existing
  0-hiding); overview 14.5px/1.6 text-mut; `WHERE TO WATCH` 10.5px
  letter-spaced label + provider chips: bg-panel border-line pill with a
  22px rounded square holding the real provider logo (fallback: initial
  on acc-soft), name 12.5px/500, `Stream` qualifier 11px text-faint;
  empty state `Not streaming in your region`; shelf actions become the
  segmented control (Watchlist / Rotation / Watched, active = ink/bg);
  favorite = 38px circle border-line bg-panel heart, acc when active,
  faint otherwise; StarRating 26px stars, panel2 base / acc fill,
  percentage-width fill (existing mechanism), label `4.5 / 5` or
  `Not rated` 12.5px text-mut. All mutation logic unchanged.
- **Profile:** `Your shelves` 22px/600 heading; tab pills with counts
  (`Watchlist 5`, count 400-weight 65% opacity; active `bg-ink text-bg`,
  inactive `bg-panel2 text-mut`). **Counts require all five shelf
  queries mounted; the reskin switches /profile from lazy-per-tab to
  five parallel `useQuery`s** (payloads are small; invalidation already
  refetches all) — approved consequence of the design. Rotation tab:
  capacity meter card (panel, border-line, rounded-[14px]) with eight
  22×15 rounded-[4px] segments — filled `bg-acc-soft border-acc`, empty
  `border-line` — plus `n of 8 rotation slots used`; badges `Next: S2E5`
  (rotation series) and `★ 4.5` (ratings tab — NOTE: star-first, changed
  from the current `4.5★`); empty states in dashed rounded-[14px] boxes,
  copy per design (`Your rotation is empty — promote titles from your
  watchlist.`, `Nothing favorited yet — tap the heart on any title.`,
  `No ratings yet — rate titles from their page.`, watchlist copy
  unchanged with its Search link).
- **Settings (approved delta 2 — auto-save):** Region card (panel,
  rounded-[14px]): label + `Sets where-to-watch availability` sub;
  select bg-panel2 with display names for the curated codes (US→United
  States, GB→United Kingdom, CA, AU, DE, FR, ES, IT, NL, SE, BR, MX,
  JP, KR, IN; unknown stored region shown as its raw code). Viewing
  window: intro copy `When you're free to watch each night — your guide
  only schedules inside these hours.`; one panel card per day: switch
  toggle (38×22 pill, white 16px knob, `bg-acc` on / `bg-panel2` off,
  `role="switch"`), day name 86px (600, faint when off), native time
  inputs (bg-panel2), `to` separator, inline error `End must be after
  start.` in danger color. The Save button is REMOVED: any change
  auto-saves after a 600ms debounce IF every row is valid (invalid rows
  show errors and hold the save; validation rules unchanged — all rows,
  cleared inputs invalid); success toasts `Settings saved`, failure
  `Couldn't save — try again.`; only one PATCH in flight (latest state
  wins). `["me"]` invalidation on success unchanged.
- **Toast:** fixed bottom-center pill `bg-ink text-bg` 13px/500 with
  shadow; behavior unchanged.
- **Guide placeholder:** token colors only; content untouched (#18).

## Copy inventory for #18 (from the design prototype — recorded here so
the guide build inherits the voice)

`Night off` · `Airs live` · `Pinned` · `✓ Watched / Pin / Swap / Details
/ Remove` (slot actions) · `↻ Regenerate remaining` · `N evenings
planned · M nights off` · `Your path: A → B → C` · `Numbered cards are
your evening · tap any alternate to swap it in` · toasts: `Watched ·
{title} moves to {SxEy}`, `Pinned to {DAY}` / `Unpinned`, `Swapped in
{title}`, `Removed — enjoy the free hour`, `Re-planned N upcoming slots
— watched and pinned stayed put`, `Added to rotation · n of 8`.

## Verification

`pnpm lint` + `pnpm build` green; no `zinc-` classes and no `dark:`
variants remain under `web/src`; both themes eyeballed via the toggle in
the user's manual pass (acceptance: every existing flow still works —
search, title actions, shelves, settings — now in the new skin, both
themes, no behavior regressions beyond the three approved deltas).

## Out of scope

Guide calendar/board (#18); any API change; mobile-specific guide
layouts; Apple sign-in; removing the `/guide` placeholder.
