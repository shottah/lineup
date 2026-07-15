# Rotation cap 10 + watchlist quick-actions (issue #41) — design

Date: 2026-07-15
Status: approved (user request verbatim; design presented in-session)
Issue: [#41 feat: rotation cap to 10; watchlist hover quick-actions](https://github.com/shottah/lineup/issues/41)

## Scope

1. **Rotation cap 8 → 10.** Sites: `rotationCap` const + comment
   (api/internal/httpserver/entries.go:23-24), its cap test
   (TestPatchEntryRotationCap — seeds to cap, asserts the over-cap PATCH
   409s), the client 409 toast in `EntryActions.tsx` (`Rotation is full
   (10); finish something first.`), and `ROTATION_CAP` in
   `ProfileBody.tsx` (meter segments and copy derive from it). Recorded
   divergence: the design reference's meter shows 8 segments; product
   decision supersedes.
2. **Watchlist hover quick-actions.** Watchlist tab only: each card gets
   a `group relative` wrapper with an overlay pill row (bottom of
   poster) visible on hover and keyboard focus-within: ♥ favorite toggle
   (acc when active, no toast), `＋ Rotation` (PATCH status rotation →
   toast `Added to rotation`; 409 → the cap toast), `✕` remove (PATCH
   status none → toast `Removed from watchlist`). Mutations use the
   internal `entry.title_id`; invalidate the `["shelf"]` prefix and the
   exact `["title", entry.kind, String(entry.tmdb_id)]`. Buttons
   disabled while pending; `Couldn't save — try again.` fallback.
   Hover-only is a recorded v1 limitation: touch flows use the title
   page's EntryActions.

## Out of scope

Quick actions on other shelves; drag interactions; changing the engine
(cap-agnostic); the design reference (stays at 8 segments as historical
artifact).

## Verification

Go suite (cap test at 10) with `TEST_DATABASE_URL=…lineup_test…` and
hermetic; `pnpm lint && pnpm test && pnpm build`. Manual: hover a
watchlist card → three actions work; 11th rotation add → cap toast.
