# Guide Card Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the UI Designer spec `docs/design/guide-card-redesign.md` (issue #47): time pill, provider logo chips, poster-derived tints, hover glow/pulse — across CalendarView and BoardView.

**Architecture:** One API commit adds `poster_path` to the guide titles sidecar (mirrors #18's enrichment). One web-lib commit adds the tint/plate extraction module (canvas + hash fallback + cache; pure parts vitest-covered). One web commit applies the designer's exact treatment to both views plus globals.css keyframes/constants.

**Tech Stack:** unchanged; no new dependencies. Branch `feat/guide-card-redesign`.

## Global Constraints

- THE DESIGNER SPEC IS BINDING for all visual values: `docs/design/guide-card-redesign.md` (exact Tailwind classes, @keyframes, extraction algorithm, plate polarity rule, state matrix). Where this plan and that spec disagree on a visual value, the designer spec wins. Implementers MUST read it before coding.
- Gates: api — `gofmt -l .` silent, `go vet ./...`, `go test ./...` with `TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup_test?sslmode=disable'` AND without; web — `pnpm lint && pnpm test && pnpm build`.
- Tokens + the spec's new `--tint-s`/`--tint-l` theme constants only; no other raw color values beyond what the designer spec pins.
- Behavior freeze: query keys, mutations, ItemMenu semantics, watched/pinned logic all unchanged — this is presentation plus one additive API field.
- `prefers-reduced-motion` disables the pulse (static glow remains) — per spec.

---

### Task 1: API — `poster_path` in the guide titles sidecar

**Files:** `api/internal/store/guides.go` (TitleLookup + GuideLookups SQL), `api/internal/store/guides_test.go`, `api/internal/httpserver/guides_test.go` (fake mirror), `web/src/lib/types.ts` (GuideTitleLookup gains `poster_path: string`).

Mirror the #18 sidecar pattern exactly: `TitleLookup` gains `PosterPath string \`json:"poster_path"\``; the titles query selects `t.poster_path`; scan updated; the httpserver fake derives a deterministic value (`fmt.Sprintf("/p%d.jpg", it.TitleID)`); existing sidecar tests extended to assert the field flows (store integration asserts the seeded poster_path; handler test asserts non-empty for seeded items). TDD: assertion first (RED: field undefined), implement, GREEN both Go runs; `pnpm build` confirms the TS addition compiles.

Commit: `feat(api): poster_path in guide title sidecar`

---

### Task 2: Web — tint/plate extraction module

**Files:** Create `web/src/app/guide/posterTint.ts` + `web/src/app/guide/posterTint.test.ts`.

Implement the designer spec's algorithm section verbatim: exported pure functions `hashHue(titleId: number): number` (deterministic fallback hue) and `tintFromHue(hue: number): string` shapes per spec (returning the CSS color strings the spec defines using `--tint-s`/`--tint-l` var composition), plus the async `posterHue(titleId, posterPath): Promise<number>` (canvas dominant-hue sampling per the spec's step list, in-memory Map cache, resolves to hashHue fallback on any failure — missing path, CORS, decode error) and `logoPlate(logoPath): Promise<"dark" | "light">` (luminance sampling per spec, cached, defaulting per spec when sampling fails). Vitest covers the PURE parts only (hashHue determinism/range; tintFromHue string shape; cache behavior with an injected fake extractor if the module structure allows it cheaply) — canvas paths are browser-only and explicitly untested (comment says so).

Gate; commit: `feat(web): poster tint and logo plate extraction`

---

### Task 3: Web — apply the treatment to both views

**Files:** `web/src/app/guide/CalendarView.tsx`, `web/src/app/guide/BoardView.tsx`, `web/src/app/globals.css` (keyframes + `--tint-s`/`--tint-l` in both `[data-lt]` blocks per spec), possibly a small shared `web/src/app/guide/ProviderChip.tsx`.

Per the designer spec's per-element class strings and state matrix: time pill; provider logo chip (w92 raster on the sampled plate, `title`/aria-label = provider name, text fallback); calendar card tinted border + wash with hover glow/pulse (interactive elements only; watched/open states per matrix); board plan cells wash-only (accent border untouched), alternates subordinate wash; keyframes + reduced-motion guard in globals.css. The sub line becomes `S1E5` + chip (per spec). Uses Task 2's module with the hash-first / canvas-upgrade pattern the spec defines.

Gate; commit: `feat(web): guide card redesign — pill, logo chips, tints, hover`

---

### Task 3b: Web — hover quick-action cluster (user addition, 2026-07-17)

**Files:** `web/src/app/guide/CalendarView.tsx` (and a small extracted component if the cluster warrants it, e.g. `web/src/app/guide/SlotQuickActions.tsx`).

Per the BINDING addendum section "Addendum: hover quick actions (watched / pin / remove)" (§A.0–A.10) in `docs/design/guide-card-redesign.md`: segmented pill cluster at card top-right in the time-pill band (never occluding the title), `opacity-0` → `group-hover`/`group-focus-within` reveal with pointer-events gating, suppressed while ItemMenu is open; inline SVG glyphs exactly as specified (check / pin outline↔filled / X with divider); watched & pin as `aria-pressed` toggles reusing ItemMenu's EXACT mutations/toasts/invalidations; remove one-shot destructive (no confirm, mirrors menu); watched card un-dims on hover/focus per addendum; ≥24px hit areas, aria-label + title per button; reduced-motion per addendum. Board views get NO cluster (addendum scoping). Move/Swap/Details stay in ItemMenu only — explicitly out of scope per user.

Gate; commit: `feat(web): hover quick actions on calendar slot cards`

---

### Task 4: Final review + PR (no auto-merge)

Fable whole-branch review (designer-spec fidelity via the state matrix; extraction correctness incl. cache/fallback; no behavior drift in views; a11y of chips; reduced-motion). Fix cycle if needed. Push, PR closing #47. Restart the :8080 API per the VERIFIED procedure (single-port lsof kills, confirm pid death, confirm new listener start time — see memory/ledger 2026-07-16). The user's hover-feel pass gates the merge.
