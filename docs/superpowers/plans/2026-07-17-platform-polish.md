# Platform Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Four user-approved improvement groups: real unwatch toggle, Regenerate mints a new week, swap clears the lingering alternate, plus the reviewer-ledgered mechanical cleanups.

**Architecture:** Two API tasks (guide-item unwatch; regenerate-seed + swap-alternate + tz test fix) and one web task (toggle wiring + cleanups). Branch `fix/platform-polish`.

**Tech Stack:** unchanged; no new dependencies.

## Global Constraints

- Gates: api — `gofmt -l .` silent, `go vet ./...`, `go test ./...` with `TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup_test?sslmode=disable'` AND without (NEVER the dev `lineup` DB); web — `pnpm lint && pnpm test && npx tsc --noEmit && pnpm build`.
- TDD per change; existing endpoint/response shapes stay backward-compatible except where a task states otherwise.
- Query keys, toast copy conventions, and token-only styling rules unchanged on the web side.

---

### Task 1: API — unwatch endpoint

**Files:** `api/internal/httpserver/server.go` (route), `api/internal/httpserver/guides.go` (+iface method), `api/internal/store/guides.go`, tests in both packages.

`DELETE /v1/guides/{id}/items/{itemID}/watched` → store `UnmarkItemWatched(ctx, userID, guideID, itemID)`. Semantics (the binding contract — "undo the mark if its effects are still the latest"):

- Set `guide_items.watched=false` (item must belong to the user's guide; same not-found semantics as MarkItemWatched).
- Read `MarkItemWatched` first (store/guides.go:395+) and mirror it in reverse, conditionally: if the user_titles pointer currently equals `nextPointer(item.season, item.episode)` (i.e. this mark's advance is still the latest), roll it back to `(item.season, item.episode)`; otherwise leave the pointer alone. If `user_titles.status='watched'` (the mark auto-completed the title), set status back to `'rotation'` and clear `watched_at`; otherwise leave status alone. Movies: same status rollback rule (pointer is irrelevant for kind='movie' — mirror whatever MarkItemWatched touches).
- Idempotent: unwatching an unwatched item is a 200 no-op returning the item.
- Handler mirrors handleWatchItem (guides.go:329 area); returns the updated item JSON.

TDD in `store/guides_test.go` (DB): mark→unmark round-trip restores pointer and status; unmark after the pointer moved on (simulate a later mark) leaves pointer untouched; movie auto-complete reversal. Handler test with the fake in `httpserver/guides_test.go` (fake gains the method) for route/status wiring.

Commit: `feat(api): unwatch guide items with conditional rollback`

---

### Task 2: API — regenerate mints a fresh seed; swap clears the lingering alternate; tz test fix

**Files:** `api/internal/httpserver/guides.go`, `api/internal/store/guides.go`, `api/internal/store/titles_test.go`, tests.

1. **Fresh seed:** in the regenerate handler (guides.go:180-201), replace `g.Seed` with `seed := d.Now().UnixNano()`; delete the now-false comment block (lines 180-186) and state the new contract in its place (regenerate = reshuffle; keeps still bind). Persist the new seed: `ReplaceUnkeptItems` gains a `seed int64` param that UPDATEs `guides.seed` in the same transaction, so the response and later regenerates chain from what actually generated the items. Update every caller/fake. Test: regenerate response carries a different seed than the created guide's and the row is persisted (store test); handler test asserts the fake receives a non-equal seed (use a controllable `d.Now`).
2. **Swap clears alternate:** in `UpdateGuideItem`'s swap path (invoked when `upd.TitleID != nil`), within the same transaction delete alternate rows shadowed by the swap: `DELETE FROM guide_items WHERE guide_id=$1 AND date=(the item's date) AND is_plan=false AND title_id=(the swapped-in title)`. Store test: swap a plan item to an alternate's title → that alternate row is gone, other alternates and other dates survive.
3. **tz test fix** (mechanical): `titles_test.go` epoch assertions `ti.ProvidersRefreshedAt.Year() != 1970` fail east/west-of-UTC-shifted local zones (observed: AST gives 1969) — compare `.UTC().Year()` (both stamps). Run that test green against lineup_test.

Commit: `fix(api): regenerate reshuffles with a fresh seed; swap removes shadowed alternate; tz-proof epoch assert`

---

### Task 3: Web — unwatch toggle wiring + mechanical cleanups

**Files:** `web/src/app/guide/useGuideItemMutations.ts`, `ItemMenu.tsx`, `SlotQuickActions.tsx`, `GenerateBar.tsx`, `CalendarView.tsx`, `web/src/lib/guide.ts` (+test), `web/src/app/guide/epLabel.ts` (delete), `epLabel.test.ts` (move/retarget), `ProviderChip.tsx`, `posterTint.ts`.

1. **Unwatch toggle:** `watchedM` in useGuideItemMutations becomes state-aware: `item.watched ? DELETE : POST` on the same path; success toast `Watched · ${title.name}` (existing) vs `Unwatched · ${title.name}`; invalidations unchanged (guide + shelf prefix). ItemMenu's Watched chip label and SlotQuickActions' aria-label/title already model both directions — verify they now truly toggle (aria-pressed flows from item.watched).
2. **GenerateBar guard:** empty/NaN days input must disable submit (same guard style as the existing min/step guards) instead of posting 0 and relying on the 422.
3. **epLabel dedup:** export `epLabel` from `web/src/lib/guide.ts` (it already has a private copy feeding BoardCell.sub); components import from `@/lib/guide`; delete `web/src/app/guide/epLabel.ts`; keep its test cases (retarget the import — into `lib/guide.test.ts` or a retargeted `epLabel.test.ts`, implementer's call).
4. **Drop `CalendarSlot.sub`:** remove the orphaned field from the mapper + its two `guide.test.ts` assertions (`col.sub`/board `cell.sub` are different fields and STAY).
5. **ProviderChip fallback:** standalone variant with `logo_path` but empty name renders the text fallback (or nothing) instead of `role="img" aria-label=""`.
6. **posterTint orchestration dedup:** extract the shared cache/in-flight/fallback shape into one parameterized helper used by both `posterHue` and `logoPlate`; exported API and all test-observable behavior (including peeks) byte-stable — the existing 60-test suite plus the 8 peek tests must pass unmodified.

Commit: `fix(web): unwatch toggle, generate guard, guide cleanups`

---

### Task 4: Final review + PR (held for user acceptance)

Fable whole-branch review: unwatch semantics vs the Task 1 contract (adversarial on the rollback conditionals), regenerate seed persistence + determinism story coherence, swap-alternate transactionality, web toggle a11y (aria-pressed both directions), cleanups' behavior-stability. Live wire smoke against a freshly restarted :8080 (verified restart procedure — LISTEN-pid only, confirm death + fresh start time; memory lsof-multiport-trap has both variants). Push, PR closing nothing (no tracking issue — reference the four groups). User acceptance gates the merge.
