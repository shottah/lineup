# Window Slider Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Settings' per-day time inputs with a dual-thumb range slider + apply-to-all, per issue #43 and `docs/superpowers/specs/2026-07-16-window-slider-design.md`.

**Architecture:** One new presentational component (two stacked native range inputs) + surgery in SettingsBody's day rows; all persistence semantics (full-document PATCH, validation, debounce, unmount flush) untouched.

**Tech Stack:** unchanged; no new dependencies. Branch `feat/window-slider`.

## Global Constraints

- Gate: `cd web && pnpm lint && pnpm test && pnpm build` — clean.
- Spec is binding for: track 960–1440 step 30; min gap 30; `"23:59"` ↔ 1440 sentinel both directions; label `7:00 pm – 11:00 pm` (en dash) via `fmtTime` with 1440 → `12:00 am`; no mutation without user drag; apply-to-all copies window only; edge captions `4pm`/`12am`; aria labels `{day} start`/`{day} end`; tokens only.
- `Apply to all days` is the exact button copy.

---

### Task 1: WindowSlider + SettingsBody surgery (prose — sonnet)

**Files:** Create `web/src/app/settings/WindowSlider.tsx`; Modify `web/src/app/settings/SettingsBody.tsx`.

Read the spec fully, then the current `SettingsBody.tsx` (the row structure, `setWindow`, form state as `HH:MM` strings, debounce/flush effects — none of that logic changes). Implement per spec §WindowSlider and §SettingsBody surgery. The dual-range CSS pattern (stacked `appearance-none pointer-events-none` inputs with thumb-only `pointer-events-auto` via arbitrary variants, plus an absolutely positioned `bg-acc` span bar) is your judgment to get right — verify the thumb variants work in the build (Tailwind v4 arbitrary variants like `[&::-webkit-slider-thumb]:pointer-events-auto` must appear as static class strings). Parse/format helpers (`parseHHMM` with the 23:59→1440 sentinel, `toHHMM` with 1440→"23:59") live in SettingsBody or the component file — NOT in lib/guide (that file is guide-payload mappers; note the boundary).

Keyboard: each range input natively supports arrows; ensure the clamp logic in onChange (not min/max attributes alone) preserves the 30-min gap from both directions.

Gate; commit `feat(web): slider viewing-window editor with apply-to-all`.

### Task 2: Whole-branch review + PR

Fable review (spec fidelity, the sentinel round-trip both directions, no-mutation-on-render, drag→single-debounced-save, a11y of the stacked inputs, apply-to-all single state update); fix cycle if needed; push; PR closing #43. The user feels the drag on the live stack before merging — do NOT auto-merge.
