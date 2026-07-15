# Rotation Cap 10 + Watchlist Quick-Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cap 8→10 across API and web; hover quick-actions on profile watchlist cards, per issue #41 and `docs/superpowers/specs/2026-07-15-cap10-watchlist-actions-design.md`.

**Architecture:** Constant change with test updates; one new overlay component wired into ShelfGrid's watchlist branch, mirroring EntryActions' mutation semantics.

**Tech Stack:** unchanged. Working directory: `/Users/matthew/Github/lineup/.claude/worktrees/rotation-cap-watchlist-actions` (a worktree — ALL commands run here; branch `feat/rotation-cap-watchlist-actions`; never touch main or the primary checkout).

## Global Constraints

- Gates: `cd api && gofmt -l . && go vet ./... && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup_test?sslmode=disable' go test ./... && go test ./...`; `cd web && pnpm lint && pnpm test && pnpm build`.
- Pinned copy exact: `Rotation is full (10); finish something first.` / `Added to rotation` / `Removed from watchlist` / `Couldn't save — try again.`
- No new deps; tokens only; typographic characters exact (♥ ＋ ✕ —).

---

### Task 1: API cap → 10

**Files:** Modify `api/internal/httpserver/entries.go`, `api/internal/httpserver/entries_test.go`.

- [ ] **Step 1 (RED first):** in `entries_test.go`, `TestPatchEntryRotationCap`: change `ids := make([]int64, 9)` → `ids := make([]int64, 11)`; the fill loop `for i := int64(1); i <= 8; i++` → `i <= 10`; the over-cap request path `/v1/titles/9/entry` → `/v1/titles/11/entry`; the comment `// 9th title: cap hit.` → `// 11th title: cap hit.`; the failure message `"9th rotation = %d body %s, want 409 rotation_full"` → `"11th rotation = ..."` (same shape). Leave the idempotent re-set case (title 3) untouched. Run `cd api && go test ./internal/httpserver/ -run TestPatchEntryRotationCap` — FAILS (the 9th add now expects 200 but gets 409… i.e., the fill loop hits the old cap at i=9).
- [ ] **Step 2:** in `entries.go` replace:

```go
// rotationCap is fixed at 8 in v1 (design spec).
const rotationCap = 8
```

with:

```go
// rotationCap was 8 in the v1 design spec; raised to 10 (issue #41).
const rotationCap = 10
```

- [ ] **Step 3 (GREEN):** the focused test passes; then the full Go gate (both runs, lineup_test URL).
- [ ] **Step 4: Commit** — `feat(api): raise rotation cap to 10`

---

### Task 2: Web — cap copy/meter + watchlist quick-actions

**Files:** Modify `web/src/components/EntryActions.tsx`, `web/src/app/profile/ProfileBody.tsx`; Create `web/src/app/profile/WatchlistQuickActions.tsx`.

- [ ] **Step 1:** `EntryActions.tsx`: `show("Rotation is full (8); finish something first.")` → `show("Rotation is full (10); finish something first.")`.
- [ ] **Step 2:** `ProfileBody.tsx`: `const ROTATION_CAP = 8;` → `const ROTATION_CAP = 10;`.
- [ ] **Step 3:** Create `web/src/app/profile/WatchlistQuickActions.tsx`:

```tsx
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api, ApiError } from "@/lib/api";
import type { Entry, EntryStatus } from "@/lib/types";

type EntryPatch = { status?: EntryStatus; favorite?: boolean };

// Hover/keyboard quick actions for a watchlist card: favorite toggle,
// add to rotation, remove from watchlist. Mirrors EntryActions' mutation
// semantics. Hover-only by design (recorded v1 limitation) — touch flows
// use the title page's full action set.
export function WatchlistQuickActions({ entry }: { entry: Entry }) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  const mutation = useMutation({
    mutationFn: (patch: EntryPatch) =>
      api<Entry>(`/v1/titles/${entry.title_id}/entry`, {
        method: "PATCH",
        body: JSON.stringify(patch),
      }),
    onSuccess: (_data, patch) => {
      if (patch.status === "rotation") {
        show("Added to rotation");
      }
      if (patch.status === "none") {
        show("Removed from watchlist");
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409 && err.code === "rotation_full") {
        show("Rotation is full (10); finish something first.");
      } else {
        show("Couldn't save — try again.");
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ["shelf"] });
      queryClient.invalidateQueries({
        queryKey: ["title", entry.kind, String(entry.tmdb_id)],
      });
    },
  });

  const busy = mutation.isPending;

  return (
    <div className="pointer-events-none absolute inset-x-2 bottom-2 z-10 flex justify-center gap-1.5 opacity-0 transition-opacity group-focus-within:pointer-events-auto group-focus-within:opacity-100 group-hover:pointer-events-auto group-hover:opacity-100">
      <button
        type="button"
        disabled={busy}
        aria-pressed={entry.favorite}
        aria-label={entry.favorite ? "Remove from favorites" : "Add to favorites"}
        onClick={() => mutation.mutate({ favorite: !entry.favorite })}
        className={`rounded-full border border-line bg-panel/90 px-2.5 py-1.5 text-sm backdrop-blur disabled:opacity-50 ${
          entry.favorite ? "text-acc" : "text-faint"
        }`}
      >
        ♥
      </button>
      <button
        type="button"
        disabled={busy}
        aria-label="Add to rotation"
        onClick={() => mutation.mutate({ status: "rotation" })}
        className="rounded-full border border-line bg-panel/90 px-2.5 py-1.5 text-xs font-medium text-ink backdrop-blur disabled:opacity-50"
      >
        ＋ Rotation
      </button>
      <button
        type="button"
        disabled={busy}
        aria-label="Remove from watchlist"
        onClick={() => mutation.mutate({ status: "none" })}
        className="rounded-full border border-line bg-panel/90 px-2.5 py-1.5 text-xs font-medium text-mut backdrop-blur disabled:opacity-50"
      >
        ✕
      </button>
    </div>
  );
}
```

- [ ] **Step 4:** in `ProfileBody.tsx`, import `WatchlistQuickActions` (from `./WatchlistQuickActions`) and replace the grid body:

```tsx
      {data.entries.map((e) => (
        <TitleCard key={e.title_id} title={cardData(e)} badge={badgeFor(shelf, e)} captionless />
      ))}
```

with:

```tsx
      {data.entries.map((e) =>
        shelf === "watchlist" ? (
          <div key={e.title_id} className="group relative">
            <TitleCard title={cardData(e)} badge={badgeFor(shelf, e)} captionless />
            <WatchlistQuickActions entry={e} />
          </div>
        ) : (
          <TitleCard key={e.title_id} title={cardData(e)} badge={badgeFor(shelf, e)} captionless />
        ),
      )}
```

- [ ] **Step 5 (gate):** `cd web && pnpm lint && pnpm test && pnpm build` — clean.
- [ ] **Step 6: Commit** — `feat(web): rotation cap 10; watchlist hover quick-actions`

---

### Task 3: Final review + PR + merge

Whole-branch review; fix cycle if needed; push; PR (`feat: rotation cap 10 and watchlist quick-actions`, closes #41); merge per the user's standing authorization; restart the :8080 API afterward (Go change).
