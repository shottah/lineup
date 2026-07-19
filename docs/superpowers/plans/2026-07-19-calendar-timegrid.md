# Calendar Time-Grid Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the guide Calendar tab with a Google-Calendar-style time-grid: shared time axis, duration-sized items, side-by-side overlap columns, and @dnd-kit drag-to-move with server-enforced past-move rules.

**Architecture:** Pure layout mappers in `lib/guide.ts` (vitest) feed a rewritten `CalendarView` that renders a gutter + absolutely-positioned cards sized via CSS vars; @dnd-kit adds optimistic drag-move on top; the existing `PATCH` move endpoint gains server-side past-move enforcement. Board view is untouched.

**Tech Stack:** Next 16 / React 19 / TanStack Query 5 / Tailwind v4 / vitest (web); Go / pgx / chi (api). New deps: `@dnd-kit/core`, `@dnd-kit/modifiers`.

## Global Constraints

- Binding spec: `docs/superpowers/specs/2026-07-18-calendar-timegrid-design.md`. Where this plan and the spec disagree on a value, the spec wins; implementers read it first.
- Constants (spec §1): `HEADER_OFFSET = 160px`, `MIN_HOUR_PX = 56px`, `MIN_ITEM_PX = 22px`. Snap step = 15 min.
- Board view (`web/src/app/guide/BoardView.tsx`) and its mappers (`toBoardRows`) are NOT modified.
- Window is NEVER enforced on a move — only `generate` gates the per-day window. Do not add a window check to the PATCH path.
- Gates: api — `gofmt -l .` silent, `go vet ./...`, `go test ./...` with `TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup_test?sslmode=disable'` AND without (never the dev `lineup` DB); web — `pnpm lint && pnpm test && npx tsc --noEmit && pnpm build`.
- Query key for the guide is `["guide"]`; the move endpoint is `PATCH /v1/guides/{id}/items/{itemID}` with `{date, start_min}`.

## File Structure

- `web/src/lib/guide.ts` — add `snap15`, `layoutDayColumns`, `toTimeGrid` (+ types). Keep `toCalendarColumns`/`toBoardRows` (board + header still use them until Task 3 removes calendar-only usage).
- `web/src/lib/guide.test.ts` — add the new pure-function suites.
- `api/internal/store/guides.go` — `ErrPastMove`, `ErrPastTarget`, `GuideItemUpdate.Today`, enforcement in `UpdateGuideItem`.
- `api/internal/store/guides_test.go` — move-rule integration tests.
- `api/internal/httpserver/guides.go` — set `Today`, map the two sentinels to 422.
- `api/internal/httpserver/guides_test.go` — handler mapping test (fake returns the sentinel).
- `web/src/app/guide/CalendarView.tsx` — rewritten to the time-grid.
- `web/src/app/guide/TimeGutter.tsx`, `DayColumn.tsx`, `GridItemCard.tsx`, `useGuideItemDrag.ts` — new (Tasks 3–4).
- `web/package.json` — `@dnd-kit/core`, `@dnd-kit/modifiers` (Task 4).

---

### Task 1: Pure layout mappers (`snap15`, `layoutDayColumns`, `toTimeGrid`)

**Files:** Modify `web/src/lib/guide.ts`; Modify `web/src/lib/guide.test.ts`.

**Interfaces:**
- Consumes: existing private helpers in `guide.ts` — `utc(date)`, `eachDate(start,end)`, `titleOf(g,item)`, `providerNameOf(g,item)`, `DOW`, and the types `GuideItem`, `GuideResponse`, `GuideTitleLookup`.
- Produces (later tasks depend on these EXACT names/shapes):
  ```ts
  export function snap15(minutes: number): number
  export type LaidOutItem = { item: GuideItem; colIndex: number; colCount: number };
  export function layoutDayColumns(items: GuideItem[]): LaidOutItem[]
  export type TimeGridItem = {
    item: GuideItem; title: GuideTitleLookup; providerName: string;
    topFactor: number; spanFactor: number; colIndex: number; colCount: number;
  };
  export type TimeGridDay = { date: string; dow: string; isToday: boolean; isPast: boolean; items: TimeGridItem[] };
  export type TimeGrid = { windowStart: number; windowEnd: number; windowHours: number; days: TimeGridDay[] };
  export function toTimeGrid(g: GuideResponse, today: string): TimeGrid
  ```

- [ ] **Step 1: Write the failing tests** — append to `web/src/lib/guide.test.ts`:

```ts
import { snap15, layoutDayColumns, toTimeGrid } from "./guide";
import type { GuideItem, GuideResponse } from "./types";

function gi(over: Partial<GuideItem>): GuideItem {
  return {
    id: 1, date: "2026-07-20", start_min: 1140, end_min: 1200, title_id: 1,
    season: 1, episode: 1, provider_id: 8, is_plan: true, pinned: false,
    edited: false, watched: false, ...over,
  };
}

describe("snap15", () => {
  it("rounds to the nearest 15", () => {
    expect(snap15(1140)).toBe(1140);
    expect(snap15(1147)).toBe(1140);
    expect(snap15(1148)).toBe(1155);
    expect(snap15(1132)).toBe(1140);
  });
});

describe("layoutDayColumns", () => {
  it("gives every non-overlapping item colCount 1", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 1080, end_min: 1140 }),
      gi({ id: 2, start_min: 1140, end_min: 1200 }), // touches edge — not overlap
    ]);
    expect(out.every((o) => o.colCount === 1 && o.colIndex === 0)).toBe(true);
  });

  it("splits two overlapping items into two columns", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 1080, end_min: 1200 }),
      gi({ id: 2, start_min: 1140, end_min: 1260 }),
    ]);
    expect(out.map((o) => o.colCount)).toEqual([2, 2]);
    expect(out.map((o) => o.colIndex).sort()).toEqual([0, 1]);
  });

  it("packs a third non-overlapping-with-first item back into column 0", () => {
    // A[0-120] overlaps B[60-180]; C[120-180] starts when A ends -> reuses A's column
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 120 }),
      gi({ id: 2, start_min: 60, end_min: 180 }),
      gi({ id: 3, start_min: 120, end_min: 180 }),
    ]);
    const byId = new Map(out.map((o) => [o.item.id, o]));
    expect(byId.get(1)!.colCount).toBe(2);
    expect(byId.get(3)!.colIndex).toBe(0); // reused column A vacated
  });

  it("keeps a separate non-overlapping cluster at colCount 1", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 60 }),
      gi({ id: 2, start_min: 30, end_min: 90 }), // cluster A (2 cols)
      gi({ id: 3, start_min: 600, end_min: 660 }), // cluster B alone
    ]);
    expect(out.find((o) => o.item.id === 3)!.colCount).toBe(1);
  });
});

describe("toTimeGrid", () => {
  const g: GuideResponse = {
    id: 1, start_date: "2026-07-20", end_date: "2026-07-21", seed: 0,
    items: [
      gi({ id: 1, date: "2026-07-20", start_min: 1110, end_min: 1200 }), // 18:30-20:00
      gi({ id: 2, date: "2026-07-21", start_min: 1230, end_min: 1350 }), // 20:30-22:30
      gi({ id: 3, date: "2026-07-20", start_min: 0, end_min: 60, is_plan: false }), // alternate: ignored
    ],
    titles: { "1": { name: "A", kind: "series", tmdb_id: 1, poster_path: "" } },
    providers: { "8": { id: 8, name: "Netflix", logo_path: "" } },
  } as unknown as GuideResponse;

  it("computes the window from plan items only, floored/ceiled to the hour", () => {
    const grid = toTimeGrid(g, "2026-07-20");
    expect(grid.windowStart).toBe(1080); // floor(1110/60)*60 = 18:00
    expect(grid.windowEnd).toBe(1380); // ceil(1350/60)*60 = 23:00
    expect(grid.windowHours).toBe(5); // (1380 - 1080) / 60
  });

  it("sets topFactor/spanFactor relative to windowStart", () => {
    const grid = toTimeGrid(g, "2026-07-20");
    const item1 = grid.days[0].items[0];
    expect(item1.topFactor).toBeCloseTo((1110 - 1080) / 60);
    expect(item1.spanFactor).toBeCloseTo((1200 - 1110) / 60);
  });

  it("flags isToday and isPast per day", () => {
    const grid = toTimeGrid(g, "2026-07-21");
    expect(grid.days[0].isPast).toBe(true); // 07-20 < today 07-21
    expect(grid.days[1].isToday).toBe(true);
  });

  it("returns a zeroed window when there are no plan items", () => {
    const empty = { ...g, items: [] } as GuideResponse;
    const grid = toTimeGrid(empty, "2026-07-20");
    expect(grid.windowHours).toBe(0);
    expect(grid.days.every((d) => d.items.length === 0)).toBe(true);
  });
});
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && pnpm test -- guide.test.ts`
Expected: FAIL — `snap15`/`layoutDayColumns`/`toTimeGrid` are not exported.

- [ ] **Step 3: Implement** — append to `web/src/lib/guide.ts` (after the existing exports; reuse the private `utc`, `eachDate`, `titleOf`, `providerNameOf`, `DOW`):

```ts
export function snap15(minutes: number): number {
  return Math.round(minutes / 15) * 15;
}

export type LaidOutItem = { item: GuideItem; colIndex: number; colCount: number };

// Side-by-side column packing (spec §2). Items are grouped into clusters of
// transitively-overlapping intervals; within a cluster each item takes the
// first column whose previous item has ended (touching edges do NOT overlap).
export function layoutDayColumns(items: GuideItem[]): LaidOutItem[] {
  const sorted = items
    .slice()
    .sort(
      (a, b) =>
        a.start_min - b.start_min ||
        b.end_min - b.start_min - (a.end_min - a.start_min) ||
        a.id - b.id,
    );
  const out: LaidOutItem[] = [];
  let cluster: GuideItem[] = [];
  let clusterMaxEnd = -Infinity;

  const flush = () => {
    const colEnds: number[] = []; // last end_min per open column
    const placed = cluster.map((it) => {
      let col = colEnds.findIndex((end) => end <= it.start_min);
      if (col === -1) {
        col = colEnds.length;
        colEnds.push(it.end_min);
      } else {
        colEnds[col] = it.end_min;
      }
      return { item: it, colIndex: col };
    });
    for (const p of placed) out.push({ ...p, colCount: colEnds.length });
    cluster = [];
    clusterMaxEnd = -Infinity;
  };

  for (const it of sorted) {
    if (cluster.length > 0 && it.start_min >= clusterMaxEnd) flush();
    cluster.push(it);
    clusterMaxEnd = Math.max(clusterMaxEnd, it.end_min);
  }
  if (cluster.length > 0) flush();
  return out;
}

export type TimeGridItem = {
  item: GuideItem;
  title: GuideTitleLookup;
  providerName: string;
  topFactor: number; // (start_min - windowStart) / 60
  spanFactor: number; // (end_min - start_min) / 60
  colIndex: number;
  colCount: number;
};

export type TimeGridDay = {
  date: string;
  dow: string;
  isToday: boolean;
  isPast: boolean;
  items: TimeGridItem[];
};

export type TimeGrid = {
  windowStart: number;
  windowEnd: number;
  windowHours: number;
  days: TimeGridDay[];
};

// Time-grid view model (spec §1): a single shared time window across the week
// (union of PLAN item spans, floored/ceiled to the hour) plus per-day laid-out
// items carrying positioning factors and overlap columns.
export function toTimeGrid(g: GuideResponse, today: string): TimeGrid {
  const planByDate = new Map<string, GuideItem[]>();
  let minStart = Infinity;
  let maxEnd = -Infinity;
  for (const item of g.items) {
    if (!item.is_plan) continue;
    minStart = Math.min(minStart, item.start_min);
    maxEnd = Math.max(maxEnd, item.end_min);
    const list = planByDate.get(item.date) ?? [];
    list.push(item);
    planByDate.set(item.date, list);
  }
  const hasItems = minStart !== Infinity;
  const windowStart = hasItems ? Math.floor(minStart / 60) * 60 : 0;
  const windowEnd = hasItems ? Math.ceil(maxEnd / 60) * 60 : 0;
  const windowHours = hasItems ? (windowEnd - windowStart) / 60 : 0;

  const days = eachDate(g.start_date, g.end_date).map((date) => {
    const d = utc(date);
    const items = layoutDayColumns(planByDate.get(date) ?? []).map(({ item, colIndex, colCount }) => ({
      item,
      title: titleOf(g, item),
      providerName: providerNameOf(g, item),
      topFactor: (item.start_min - windowStart) / 60,
      spanFactor: (item.end_min - item.start_min) / 60,
      colIndex,
      colCount,
    }));
    return { date, dow: DOW[d.getUTCDay()], isToday: date === today, isPast: date < today, items };
  });
  return { windowStart, windowEnd, windowHours, days };
}
```

- [ ] **Step 4: Run to verify pass** — `cd web && pnpm test -- guide.test.ts` → PASS. Then `pnpm lint && npx tsc --noEmit`.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/guide.ts web/src/lib/guide.test.ts
git commit -m "feat(web): time-grid layout mappers (snap15, column packing, window)

Claude-Session: https://claude.ai/code/session_01VJLReS68zKnpPZgGrbfdLa"
```

---

### Task 2: API move-rule enforcement

**Files:** Modify `api/internal/store/guides.go`; Modify `api/internal/store/guides_test.go`; Modify `api/internal/httpserver/guides.go`; Modify `api/internal/httpserver/guides_test.go`.

**Interfaces:**
- Consumes: existing `GuideItemUpdate`, `UpdateGuideItem`, `ErrGuideNotFound`, `dateFmt`, handler `d.Now()`.
- Produces: `store.ErrPastMove`, `store.ErrPastTarget`, `GuideItemUpdate.Today string`. Handler maps both to 422.

Enforcement rule (spec §4): a move (`Date != nil || StartMin != nil`) is allowed only when the item's current date `>= Today` (else `ErrPastMove`) AND, if a target date is given, `*Date >= Today` (else `ErrPastTarget`). `Today` is `Now().UTC()` formatted `dateFmt`. Window is NOT checked. Non-move PATCHes (pin/title-swap) skip enforcement entirely.

- [ ] **Step 1: Write the failing store tests** — add to `api/internal/store/guides_test.go` (follow the file's existing seed helpers; use a guide with one past-dated item and one future-dated item). Assert:
  - moving the past item (`Date` to any date, or `StartMin` only) → `errors.Is(err, ErrPastMove)`.
  - moving the future item to a past `Date` → `errors.Is(err, ErrPastTarget)`.
  - moving the future item to another future `Date` + `StartMin` → no error, returned item reflects the move.
  - moving the future item to a `StartMin` outside its day's window (e.g. 300 = 05:00) on a valid future date → no error (window not enforced).
  - a pin-only PATCH (`Pinned` set, `Date`/`StartMin` nil, `Today` set) on the PAST item → no error (enforcement skipped).
  Name the covering tests `TestUpdateGuideItemPastMove*`.

- [ ] **Step 2: Run to verify failure**

Run: `cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup_test?sslmode=disable' go test ./internal/store/ -run TestUpdateGuideItemPastMove -v`
Expected: FAIL — `ErrPastMove`/`ErrPastTarget`/`Today` undefined (compile error).

- [ ] **Step 3: Implement store** — in `api/internal/store/guides.go`:

Add sentinels near `ErrGuideNotFound`:

```go
// ErrPastMove is returned when the moved item's current date is already in
// the past; ErrPastTarget when the requested target date is in the past.
var ErrPastMove = errors.New("store: cannot move a past slot")
var ErrPastTarget = errors.New("store: cannot move a slot into the past")
```

Add `Today` to `GuideItemUpdate` (after `SetEdited`):

```go
	// Today (YYYY-MM-DD, UTC) gates move enforcement; empty disables it.
	// Only consulted when the update is a move (Date or StartMin set).
	Today string
```

In `UpdateGuideItem`, after `defer tx.Rollback(ctx)` and BEFORE the main UPDATE, insert:

```go
	if (upd.Date != nil || upd.StartMin != nil) && upd.Today != "" {
		var currentDate string
		err := tx.QueryRow(ctx, `
SELECT gi.date::text FROM guide_items gi
JOIN guides g ON g.id = gi.guide_id
WHERE gi.id = $3 AND gi.guide_id = $2 AND g.user_id = $1
FOR UPDATE OF gi`, userID, guideID, itemID).Scan(&currentDate)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGuideNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("store: update guide item: load date: %w", err)
		}
		if currentDate < upd.Today {
			return nil, ErrPastMove
		}
		if upd.Date != nil && *upd.Date < upd.Today {
			return nil, ErrPastTarget
		}
	}
```

(`date::text` yields `YYYY-MM-DD`, which lexically compares correctly against `Today`.)

- [ ] **Step 4: Implement handler** — in `api/internal/httpserver/guides.go` `handlePatchItem`, set `Today` on the `upd` literal:

```go
		upd := store.GuideItemUpdate{Date: body.Date, StartMin: body.StartMin, Pinned: body.Pinned,
			SetEdited: body.Date != nil || body.StartMin != nil || body.TitleID != nil,
			Today:     d.Now().UTC().Format(dateFmt)}
```

And add cases to the error switch after the `UpdateGuideItem` call (before the generic `err != nil`):

```go
		case errors.Is(err, store.ErrPastMove):
			writeJSONError(w, http.StatusUnprocessableEntity, "cannot move past slot")
			return
		case errors.Is(err, store.ErrPastTarget):
			writeJSONError(w, http.StatusUnprocessableEntity, "cannot move into the past")
			return
```

- [ ] **Step 5: Handler mapping test** — in `api/internal/httpserver/guides_test.go`, make the fake store's `UpdateGuideItem` return `store.ErrPastMove` for a sentinel input and assert the response is `422` with body `{"error":"cannot move past slot"}`. (Mirror an existing handler-error test's structure.)

- [ ] **Step 6: Run to verify pass**

```bash
cd api && gofmt -l . && go vet ./... \
  && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup_test?sslmode=disable' go test ./... \
  && go test ./...
```
Expected: all green (both DB modes).

- [ ] **Step 7: Commit**

```bash
git add api/internal/store/guides.go api/internal/store/guides_test.go api/internal/httpserver/guides.go api/internal/httpserver/guides_test.go
git commit -m "feat(api): enforce past-move rules on guide item PATCH

Claude-Session: https://claude.ai/code/session_01VJLReS68zKnpPZgGrbfdLa"
```

---

### Task 3: Grid rendering (no drag yet)

**Files:** Rewrite `web/src/app/guide/CalendarView.tsx`; Create `web/src/app/guide/TimeGutter.tsx`, `web/src/app/guide/DayColumn.tsx`, `web/src/app/guide/GridItemCard.tsx`. Do NOT touch `BoardView.tsx`, `GuideBody.tsx`'s toggle, `ItemMenu.tsx`, `SlotQuickActions.tsx`, `ProviderChip.tsx`, `usePosterHue.ts`.

**Interfaces:**
- Consumes Task 1: `toTimeGrid(guide, today)`, its `TimeGrid`/`TimeGridDay`/`TimeGridItem` types, `MIN_HOUR_PX`/`MIN_ITEM_PX` constants (define them in `CalendarView.tsx` per spec §1), `fmtTime` for gutter labels.
- Produces: `<CalendarView guide today />` unchanged prop signature (GuideBody keeps calling it the same way).

Read the spec §1, §2, §5 in full first. Implement the STATIC grid (this task adds NO dragging — moves still work through the existing `ItemMenu` Move picker, which `GridItemCard` keeps mounting):

1. **CalendarView** builds `const grid = toTimeGrid(guide, today)`. Empty case (`grid.windowHours === 0`): render the existing "Night off"/empty treatment (no axis). Otherwise render a flex row: `<TimeGutter>` then the 7 `<DayColumn>`s. Set the CSS custom properties on the grid wrapper so children can size against them:
   - `style={{ "--hours": grid.windowHours, "--hour-px": "max(calc(max(100dvh - 160px, 66dvh) / var(--hours)), 56px)", "--win-start": grid.windowStart }}`.
   - The scrollable grid body height is `calc(var(--hours) * var(--hour-px))`; the whole grid area is vertically scrollable (`overflow-y-auto`) and, below `lg`, horizontally snap-scrolls the day columns with the gutter sticky-left (spec §5). Desktop `lg+`: gutter + `grid-cols-7`.
2. **TimeGutter** renders one label per hour from `windowStart` to `windowEnd` (step 60), each a row of height `var(--hour-px)`, label via `fmtTime(min)` (left-aligned, `text-faint text-[10px]`), top-aligned to its hour line. Include faint hour gridlines spanning the columns (a repeating background or per-hour border) for visual alignment.
3. **DayColumn** takes a `TimeGridDay`, renders the day header (dow + date, `isToday` accented, matching the current header styling) and a `position: relative` body of height `calc(var(--hours) * var(--hour-px))`. Each `TimeGridItem` renders a `<GridItemCard>` absolutely positioned:
   - `top: calc(var(--hour-px) * ${topFactor})`
   - `height: max(calc(var(--hour-px) * ${spanFactor}), 22px)`
   - `left: ${(colIndex / colCount) * 100}%`, `width: calc(${100 / colCount}% - 4px)` (4px gutter between overlap columns).
   - Past days (`isPast`) get a subtle dimmed/locked treatment on the column body (spec §4-client) — visual only in this task.
4. **GridItemCard** is the poster-tinted card from the current `CalendarSlotCard` MINUS the time pill (spec §5): keep `usePosterHue` tint + hover glow/pulse classes, the title (with watched ✓ + dim), the `epLabel`+`ProviderChip` sub-line, the Pinned badge, the `SlotQuickActions` hover cluster, and the click-to-open `ItemMenu` (pass through the same props it needs — `columns`/`columnDate`/`columnDow` derived from the grid days). It must fit its absolute box: internal `overflow-hidden`, title `line-clamp` so short/narrow cards truncate gracefully.

Preserve all existing card behavior and query/mutation wiring (this is presentation only). Verify the CSS `max()/calc()` var expressions appear as static strings and the build resolves them.

Gate (`pnpm lint && pnpm test && npx tsc --noEmit && pnpm build`); commit `feat(web): calendar time-grid rendering with overlap columns`.

---

### Task 4: @dnd-kit drag-to-move + optimistic mutation + client past-guards

**Files:** Modify `web/package.json` (add `@dnd-kit/core`, `@dnd-kit/modifiers`); Modify `web/src/app/guide/CalendarView.tsx`, `DayColumn.tsx`, `GridItemCard.tsx`; Create `web/src/app/guide/useGuideItemDrag.ts`.

**Interfaces:**
- Consumes Task 1 (`snap15`, `TimeGrid`), Task 2 (the 422 the server now returns on illegal moves), Task 3 (the rendered grid + the resolved `hourPx`).
- Produces: drag behavior; no new exported types other than the hook.

Read spec §3 and §4-client. Implement:

1. **Install deps:** `cd web && pnpm add @dnd-kit/core @dnd-kit/modifiers` (record the resolved versions; commit the lockfile).
2. **`hourPx` in JS:** compute `resolvedHourPx = Math.max(Math.max(window.innerHeight - 160, window.innerHeight * 0.66) / windowHours, 56)` (mirror of the CSS formula) in `CalendarView`, kept in a ref and updated on `resize`. Used ONLY for the drag delta→minutes math; CSS remains the layout source of truth.
3. **DndContext** in `CalendarView` wrapping the grid, with a `PointerSensor` using `activationConstraint: { distance: 6 }` (so small movements stay clicks that open the menu) and a `KeyboardSensor`. Restrict movement with `@dnd-kit/modifiers` as helpful (e.g. `restrictToWindowEdges`).
4. **GridItemCard** becomes a `useDraggable` (id = `String(item.id)`), `disabled` when its day `isPast`. Apply the drag transform to the card. Dragging must not swallow the click that opens `ItemMenu` (the activation distance handles this).
5. **DayColumn** becomes a `useDroppable` (id = `date`), `disabled` when `isPast`.
6. **onDragEnd** (in `CalendarView`): given `active` (the item) and `over` (the column) and `event.delta.y`:
   - `duration = item.end_min - item.start_min`.
   - `newStart = clamp(snap15(item.start_min + Math.round(delta.y / resolvedHourPx * 60)), windowStart, windowEnd - duration)`.
   - `newDate = over ? String(over.id) : item.date`.
   - If `newDate === item.date && newStart === item.start_min`, do nothing (a no-op drag / click).
   - Otherwise call the move mutation from `useGuideItemDrag`.
7. **`useGuideItemDrag`** — an optimistic move mutation (spec §3):
   - `mutationFn`: `api(`/v1/guides/${guideId}/items/${itemId}`, { method: "PATCH", body: JSON.stringify({ date, start_min }) })`.
   - `onMutate`: `queryClient.cancelQueries({ queryKey: ["guide"] })`, snapshot the cached `GuideResponse`, write a new one with the target item's `date`/`start_min`/`end_min` updated (end_min = start_min + duration), return `{ previous }`.
   - `onError`: restore `previous`; surface the server message via the existing toast helper (the 422 "cannot move…" text).
   - `onSettled`: `invalidateQueries({ queryKey: ["guide"] })`.
8. Client past-guards are the `disabled` flags in 4/5 (dimmed non-draggable past cards, disabled past droppables) — the server (Task 2) is the real enforcement; the optimistic rollback handles any UTC/local edge (spec §4).

Gate (`pnpm lint && pnpm test && npx tsc --noEmit && pnpm build`); commit `feat(web): drag-to-move calendar items with optimistic update`.

---

### Task 5: Final review + PR (held for manual drag-feel pass)

Fable whole-branch review: spec fidelity (§1 scale formula in the built CSS, §2 overlap packing correctness incl. the pure-function edge cases, §3 drag math + optimistic rollback, §4 server AND client enforcement, §5 card/responsive), no Board/ItemMenu behavior drift, a11y of the draggable cards + keyboard sensor, and a live wire smoke: mint an emulator user, generate a guide on isolated data, PATCH-move a future item (200), attempt to move a past item and a move-into-past (both 422), confirm a window-violating start_min on a valid date still succeeds. Run all gates in both DB modes. Fix cycle if needed. Push, PR referencing the spec. Restart the :8080 API per the verified procedure (LISTEN-scoped `lsof -tiTCP:8080 -sTCP:LISTEN`, confirm pid death + fresh start time — memory lsof-multiport-trap). Do NOT auto-merge: the user's manual drag-feel pass gates the merge.
