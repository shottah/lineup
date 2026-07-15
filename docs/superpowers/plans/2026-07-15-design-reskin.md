# Design-System Reskin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the approved Claude Design system to the six existing web surfaces, per issue #38 and `docs/superpowers/specs/2026-07-15-design-reskin-design.md`.

**Architecture:** Token-first: CSS custom properties switched by `data-lt` on `<html>`, exposed to Tailwind v4 via `@theme inline`; every surface is then restyled onto those tokens. Three approved behavior deltas: theme toggle (dark default), settings auto-save, avatar menu. No other behavior changes.

**Tech Stack:** Next 16.2.10 / React 19 / Tailwind v4 / TanStack Query 5 / pnpm. Albert Sans via `next/font/google` (no new dependency).

**PLAN STYLE NOTE (deliberate deviation):** unlike prior plans, tasks here specify structure, tokens, and exact copy in prose rather than complete TSX — the pixel truth lives in `docs/design/lineup-app.dc.html` (in-repo), which implementers MUST read alongside the spec. Where this plan pins a value (token, copy string, size), it is exact. Implementers exercise visual judgment translating the reference's inline styles into Tailwind token utilities; the review gates check fidelity against the same reference.

## Global Constraints

- Branch `feat/design-reskin`. `web/` only. No new dependencies; `package.json` may change ONLY if `next/font` requires nothing (it doesn't — do not touch package.json).
- Every task gate: `cd web && pnpm lint && pnpm build` — clean.
- Required reading per task: `docs/superpowers/specs/2026-07-15-design-reskin-design.md` (spec) and `docs/design/lineup-app.dc.html` (visual reference; reference wins visual disagreements).
- Tokens exactly as the spec's table; Tailwind color names `bg, panel, panel2, ink, mut, faint, line, acc, acc-ink, acc-soft, danger`. After the reskin no `zinc-` class and no `dark:` variant remains under `web/src` (grep-verified in the final task).
- NO raw hex in component code — token utilities only.
- All existing behavior preserved except the three approved deltas (theme toggle, settings auto-save, avatar menu). Query keys, mutation bodies, validation rules, aria patterns: unchanged unless a task says otherwise.
- Copy strings pinned in the spec/tasks are exact, including typographic characters (— · ← ☾ ☀ ★ ♥ ↻ ✓).

---

### Task 1: Token foundation, font, theme toggle, Toast, Footer

**Files:**
- Modify: `web/src/app/globals.css`
- Modify: `web/src/app/layout.tsx`
- Create: `web/src/components/ThemeToggle.tsx`
- Create: `web/src/components/Footer.tsx`
- Modify: `web/src/components/Providers.tsx` (Toast pill restyle only)

**Interfaces:**
- Produces: token utilities usable everywhere (`bg-bg, bg-panel, bg-panel2, text-ink, text-mut, text-faint, border-line, bg-acc, text-acc, text-acc-ink, bg-acc-soft, text-danger` and their variants); `<ThemeToggle />` (self-contained pill button); `<Footer />` (attribution bar). Later tasks rely on all three.

- [ ] **Step 1: globals.css** — define `[data-lt="dark"]` and `[data-lt="light"]` blocks with the spec's exact custom-property values (plus `--danger:#c96a6a` in both); an `@theme inline` block mapping each to Tailwind colors (e.g. `--color-panel: var(--panel);`); global `button:focus-visible, input:focus-visible, select:focus-visible, a:focus-visible { outline: 2px solid var(--acc); outline-offset: 2px; }`; body defaults `background: var(--bg); color: var(--ink);` with a 250ms background transition. Remove any prior zinc-based body styling.
- [ ] **Step 2: layout.tsx** — load Albert Sans via `next/font/google` (`Albert_Sans`, weights 400/500/600/700, `variable`), attach to `<html>`; set the font as the sans default (via the font variable in `@theme inline`: `--font-sans: var(--font-albert-sans), system-ui, sans-serif;` — coordinate the variable name with globals.css); add `suppressHydrationWarning` and a `<script>` in `<head>` (dangerouslySetInnerHTML, one statement) that sets `document.documentElement.dataset.lt = localStorage.getItem("lineup-theme") || "dark"` inside a try/catch defaulting to dark. Update `metadata` untouched otherwise.
- [ ] **Step 3: ThemeToggle.tsx** — `"use client"`; reads current theme from `document.documentElement.dataset.lt` into state on mount (default `"dark"`); renders the pill (`border border-line bg-panel text-mut rounded-full px-3.5 py-[7px] text-xs font-medium whitespace-nowrap`) with label `☾ Dark` / `☀ Light`; `aria-label="Toggle theme"`; click flips the `<html>` attribute, persists to `localStorage["lineup-theme"]`, updates state.
- [ ] **Step 4: Footer.tsx** — plain component: `border-t border-line px-8 py-[18px] text-[11px] text-faint` containing exactly `Metadata from TMDB · Streaming availability from JustWatch · Air dates from TVMaze`.
- [ ] **Step 5: Toast restyle** in Providers.tsx — the toast div becomes `fixed bottom-7 left-1/2 z-[60] -translate-x-1/2 whitespace-nowrap rounded-full bg-ink px-[22px] py-[11px] text-[13px] font-medium text-bg shadow-[0_6px_24px_rgba(0,0,0,.35)]`. Logic untouched.
- [ ] **Step 6: Gate + commit** — `cd web && pnpm lint && pnpm build`; commit all five files: `feat(web): design tokens, Albert Sans, theme toggle, footer, toast restyle`

---

### Task 2: Chrome (Nav + avatar menu) and login; guide placeholder token pass

**Files:**
- Modify: `web/src/components/Nav.tsx` (full redesign)
- Modify: `web/src/app/login/page.tsx` (full redesign)
- Modify: `web/src/app/guide/page.tsx` (token colors only)
- Modify: `web/src/app/page.tsx` (if it carries zinc styling, token pass)

**Interfaces:**
- Consumes: Task 1 tokens, `ThemeToggle`, `Footer`.
- Produces: redesigned `Nav` (used by every authed page); pages must render `<Footer />` after their body — Nav does NOT render it (composition stays per-page; the four page files touched across Tasks 2–4 each add `<Footer />` as the last child inside `AuthGate`).

- [ ] **Step 1: Nav** — header `flex items-center justify-between gap-4 border-b border-line px-8 py-[18px]`; left: wordmark `Lineup` (Link to /guide, `text-[19px] font-semibold tracking-[-0.01em] text-ink`) + nav pills (Guide /guide, Search /search, Profile /profile, Settings /settings): `rounded-full px-[15px] py-[7px] text-[13px] font-medium`, active route (`usePathname()`, exact or prefix for /title→none) `bg-acc-soft text-acc`, inactive `text-mut`; right: `<ThemeToggle />` + avatar button: 32px `rounded-full border border-line bg-acc-soft text-acc text-[13px] font-semibold` showing the uppercased first character of `user.email` (fallback "?"); clicking toggles a dropdown (`absolute right-8 top-14 z-50 rounded-xl border border-line bg-panel p-2 shadow-lg`, min-w 200px) containing the email (`px-3 py-2 text-xs text-mut`, truncated) and a `Sign out` button (`w-full rounded-lg px-3 py-2 text-left text-[13px] text-ink hover:bg-panel2`) running the existing sign-out flow (signOutUser → queryClient.clear() → router.replace("/login")). Close on outside click (document listener) and Escape. Keep `"use client"`.
- [ ] **Step 2: Login** — per reference: full-viewport centering (min-h-[80vh]); 360px card `rounded-[20px] border border-line bg-panel px-9 py-10 text-center`; the CSS antenna mark (44×32 `rounded-lg border-2 border-acc relative` with two absolutely-positioned 2×11px `bg-acc` aerials rotated ∓28° above); `Lineup` 26px/600; tagline `Your week of TV, planned like a lineup.` text-mut 14px; Google button `mt-[22px] flex w-full items-center justify-center gap-2.5 rounded-full bg-ink px-5 py-3 text-sm font-semibold text-bg disabled:opacity-50` with a 20px `rounded-full bg-bg text-ink text-xs font-bold` "G" circle; footnote `One evening at a time. No autoplay, no feeds.` 11px text-faint mt-3.5; keep existing busy/error/appleAuth/redirect logic and the error line (retoken to `text-danger`).
- [ ] **Step 3: Guide placeholder + root page** — replace zinc/dark: classes with token equivalents (`text-ink`, `text-mut`); add `<Footer />` to the guide page composition. No content changes.
- [ ] **Step 4: Gate + commit** — `feat(web): design-system chrome, avatar menu, login`

---

### Task 3: Search + Title pages (TitleCard, StarRating, segmented control, provider chips)

**Files:**
- Create: `web/src/components/Segmented.tsx`
- Modify: `web/src/components/TitleCard.tsx`
- Modify: `web/src/components/StarRating.tsx`
- Modify: `web/src/components/EntryActions.tsx`
- Modify: `web/src/app/search/SearchBody.tsx`, `web/src/app/search/page.tsx`
- Modify: `web/src/app/title/[kind]/[tmdbId]/TitleBody.tsx`, `.../page.tsx`

**Interfaces:**
- Consumes: Task 1 tokens + Footer.
- Produces: `Segmented({ options: {value, label}[], value, onChange, disabled?, ariaLabel })` — container `flex gap-0.5 rounded-full bg-panel2 p-[3px]` with `role="group"`, option buttons `rounded-full px-4 py-[7px] text-xs font-semibold whitespace-nowrap` active `bg-ink text-bg` inactive `text-mut`, `aria-pressed` per option. Task 4 reuses it? (No — profile tabs differ; Segmented is used by EntryActions now and the guide view toggle in #18.)

- [ ] **Step 1: Segmented.tsx** as specified above.
- [ ] **Step 2: TitleCard** — poster `rounded-xl` (image untouched); no-poster: `aspect-[2/3] rounded-xl border border-dashed border-line bg-panel2` centered column with initials (first letters of up to three words, uppercase, `text-xl font-semibold text-faint`) and, when `showNoPosterCaption` isn't suppressed, `No poster` 10.5px text-faint (design shows the caption in search; shelf variant shows initials only — add optional prop `captionless?: boolean`, profile passes true); badge pill `absolute left-2 top-2 z-10 rounded-full bg-acc px-[9px] py-[3px] text-[10px] font-semibold text-acc-ink`; name `mt-[9px] text-[13.5px] font-semibold text-ink` truncate + group-hover underline (keep); meta `text-[11.5px] text-mut` — format `{year} · {Kind}` with Kind capitalized (`Movie`/`Series`), year empty → Kind alone. Card wrapper drops its border (design cards are borderless posters) — `group relative block text-left`.
- [ ] **Step 3: StarRating** — same mechanics; sizes to 26px boxes, base star `text-panel2`, fill `text-acc`, font-size 22px/line-height 26px; the value label moves OUT of the component? No — keep label inside, restyled `text-[12.5px] font-medium text-mut min-w-[60px]`, text `4.5 / 5` when set, `Not rated` when null (changed from bare number).
- [ ] **Step 4: EntryActions** — replace the three status buttons with `Segmented` (values watchlist/rotation/watched, labels Watchlist/Rotation/Watched; active = current status; clicking active PATCHes none — preserve exact mutation semantics incl. 409 handling); favorite becomes `h-[38px] w-[38px] rounded-full border border-line bg-panel text-base` heart, `text-acc` when favorite else `text-faint`; keep aria-pressed/labels/disabled semantics; StarRating below as before.
- [ ] **Step 5: SearchBody** — input `w-full max-w-[560px] rounded-xl border border-line bg-panel px-[18px] py-[13px] text-[15px] text-ink placeholder:text-faint` (drop the ring classes; global focus-visible covers focus); hint state copy exactly `Type to search — results appear as you type.` (text-faint 13.5px, replaces current prompt); no-results copy exactly `No matches. Try a different spelling.` (replaces the curly-quoted line); error line retokened `text-danger`; grid `grid gap-[18px] [grid-template-columns:repeat(auto-fill,minmax(150px,1fr))]`; page adds `<Footer />`.
- [ ] **Step 6: TitleBody** — add `← Back` button above (`text-[13px] font-medium text-mut`, onClick `router.back()`); poster column 220px `rounded-[14px]`; title 32px/600 tracking-tight; meta line 13.5px text-mut (keep existing content rules, prepend year if present in payload? — year is NOT in the title payload; keep existing meta content, just retokened); overview `text-[14.5px] leading-relaxed text-mut`; `WHERE TO WATCH` label `text-[10.5px] font-semibold tracking-[.14em] text-faint mb-2`; provider chips `flex items-center gap-2 rounded-full border border-line bg-panel py-1.5 pl-[7px] pr-3.5`: 22px `rounded-md` square holding the provider logo image (fallback: initial on `bg-acc-soft text-acc text-[11px] font-bold`), name `text-[12.5px] font-medium text-ink`, qualifier `Stream` `text-[11px] text-faint`; empty copy `Not streaming in your region.` → change to `Not streaming in your region` (no period, per reference); loading/error lines retokened; page adds `<Footer />`.
- [ ] **Step 7: Gate + commit** — `feat(web): design-system search and title pages`

---

### Task 4: Profile + Settings

**Files:**
- Modify: `web/src/app/profile/ProfileBody.tsx`, `web/src/app/profile/page.tsx`
- Modify: `web/src/app/settings/SettingsBody.tsx`, `web/src/app/settings/page.tsx`

**Interfaces:**
- Consumes: tokens, TitleCard (badge + captionless), Footer, useToast.

- [ ] **Step 1: ProfileBody** — heading `Your shelves` 22px/600; **all five shelves fetch on mount** (five `useQuery`s keyed `["shelf", name]`; extract a `useShelf(name)` helper in-file) so tab pills show counts: label + count (`font-normal opacity-65`), active `bg-ink text-bg`, inactive `bg-panel2 text-mut`, `aria-pressed`, `whitespace-nowrap`; active tab renders its grid (data already cached). Rotation meter: `flex items-center gap-3.5 self-start rounded-[14px] border border-line bg-panel px-[18px] py-3.5` with eight `h-[15px] w-[22px] rounded-[4px] border` segments (filled `bg-acc-soft border-acc`, empty `border-line`) + `n of 8 rotation slots used` 13px/500 text-mut. Badges: rotation series `Next: {SxEy}`; ratings `★ {value}` (e.g. `★ 4.5` — star first). Empty states: dashed `rounded-[14px] border border-dashed border-line p-9 text-center text-[13.5px] text-faint max-w-[480px]`; copy — rotation `Your rotation is empty — promote titles from your watchlist.`, watched `Nothing marked watched yet.`, favorites `Nothing favorited yet — tap the heart on any title.`, ratings `No ratings yet — rate titles from their page.`, watchlist keeps its Search-link copy. Cards use `captionless` no-poster variant. Page adds `<Footer />`.
- [ ] **Step 2: SettingsBody** — heading `Settings` 22px/600, column max-w-[620px] gap-6. Region card: `flex items-center justify-between gap-4 rounded-[14px] border border-line bg-panel px-5 py-4`; left column label `Region` 14px/600 + `Sets where-to-watch availability` 12px text-mut; right: select `rounded-[10px] border border-line bg-panel2 px-3 py-2 text-[13px] font-medium text-ink` whose options are the curated codes displayed as names (US=United States, GB=United Kingdom, CA=Canada, AU=Australia, DE=Germany, FR=France, ES=Spain, IT=Italy, NL=Netherlands, SE=Sweden, BR=Brazil, MX=Mexico, JP=Japan, KR=South Korea, IN=India; unknown current region prepended as its raw code). Viewing window: section label `Viewing window` 14px/600 + intro `When you're free to watch each night — your guide only schedules inside these hours.` 12.5px text-mut; day rows as panel cards `rounded-xl border border-line bg-panel px-4 py-2.5`: switch button (`role="switch"`, `aria-checked`, 38×22 rounded-full, `bg-acc` on / `bg-panel2` off, white 16px knob translating 16px, 150ms transitions), day name `w-[86px] text-[13px] font-semibold` (`text-ink` on / `text-faint` off), time inputs `rounded-lg border border-line bg-panel2 px-2 py-[5px] text-[12.5px] font-medium text-ink disabled:opacity-50`, `to` separator text-faint, inline error `End must be after start.` (`text-[11.5px] font-medium text-danger`, indented past the switch). **Auto-save (approved delta):** REMOVE the Save button and the form element's submit flow; a `useEffect` watches form state — when it differs from the last-saved snapshot AND `invalidDays.length === 0`, debounce 600ms then fire the existing mutation (full `{region, schedule_prefs}` document); skip while a mutation is in flight (re-queue after settle via effect deps); success toast `Settings saved` + invalidate `["me"]`, error toast `Couldn't save — try again.`. Validation logic itself (all rows, cleared-input invalid) unchanged. Keep checkbox→switch aria migration (`role="switch"` + `aria-checked` replaces the checkbox input). Page adds `<Footer />`.
- [ ] **Step 3: Gate + commit** — `feat(web): design-system profile and settings with auto-save`

---

### Task 5: Sweep, zinc purge check, PR

- [ ] **Step 1:** `cd web && pnpm lint && pnpm build` and `grep -rn "zinc-\|dark:" web/src && echo LEFTOVERS || echo clean` — must print `clean`.
- [ ] **Step 2:** smoke: `curl -s -o /dev/null -w '%{http_code}\n'` for `/login`, `/search`, `/profile`, `/settings` on :3001 — all 200.
- [ ] **Step 3:** push + PR (`feat(web): apply Lineup design system`, closes #38, writing-github-content style, note the three approved deltas and the #18 visual-spec handoff). User's visual pass (both themes) is the acceptance gate before merge.

## Execution notes

- Task order strict 1→5. Implementers MUST read the spec and the design reference before writing code; the reference wins visual disagreements.
- The final whole-branch review happens before Task 5 Step 3.
