# Calendar week view → time-grid — design

Date: 2026-07-18
Status: approved (user selected @dnd-kit, 15-min snaps, side-by-side overlap
columns, Board view left untouched — via a four-question preview)
Scope: replace the contents of the guide **Calendar** tab with a
Google-Calendar-style time-grid. The **Board** tab and its code are NOT
touched (a separate future overhaul PR owns it).

## Problem

The current Calendar tab (`web/src/app/guide/CalendarView.tsx`) is 7 stacked
lists of cards, one per day; every card carries its own time pill and all
cards are the same height regardless of runtime. The user wants a real
calendar: a left time axis, items positioned by start time and sized by
duration, and drag-and-drop to re-time and re-day items. Time moves to the
gutter so cards reclaim that space.

## Non-goals

- Board view changes (future PR).
- Fancier Google-style overlap **width expansion** (we do equal-width
  splitting; expansion is a possible v2).
- Fixing the UTC-vs-local "today" seam (pre-existing, ledgered; see §5).
- Enforcing the per-day generate window on manual moves — explicitly
  forbidden: the window gates `generate` only.

---

## 1. Grid structure & vertical scale

One **shared time axis** for the whole week: a left gutter of left-aligned
hour labels (`6 PM`, `7 PM`, …) and 7 day columns to its right, all on the
same axis so equal times line up across days and against the gutter.

### Window (visible time range)

Computed from the union of all **plan** items (`is_plan === true`) across the
week:

- `windowStart = floor(min(start_min) / 60) * 60`
- `windowEnd   = ceil(max(end_min) / 60) * 60`
- `windowHours = (windowEnd - windowStart) / 60`

Empty guide (no plan items) → the grid renders the existing empty/generate
state; no axis. A single 30-min item still yields at least a 1-hour window
(floor/ceil guarantee `windowHours >= 1`).

### Equal-height hours & the scale rule

Every hour is `hourPx` tall. `hourPx` is expressed entirely in CSS from two
inputs — the number of hours (a unitless CSS var `--hours`) and the viewport
— so no JS measurement / ResizeObserver is needed:

```
--hours: <windowHours>;                         /* inline, per render */
--hour-px: max(
  calc(max(100dvh - HEADER_OFFSET, 66dvh) / var(--hours)),
  MIN_HOUR_PX
);
grid height = calc(var(--hours) * var(--hour-px));
```

Behavior this produces (this is the "100vh, min 66vh" rule):

- **Fill the viewport:** the grid targets the usable height below the page
  header (`100dvh - HEADER_OFFSET`).
- **Floor at 66vh:** `max(..., 66dvh)` keeps a sparse week (few hours) filling
  at least two-thirds of the screen with tall, generous cards — no upper cap,
  so short windows deliberately grow to fill.
- **Scroll when dense:** when the window is long enough that equal division
  would drop below `MIN_HOUR_PX`, hours hold at that floor and the grid area
  scrolls vertically instead of crushing items.

Constants (tunable; pinned here so the plan is concrete):
`HEADER_OFFSET = 160px`, `MIN_HOUR_PX = 56px`. A 45-min episode at the floor
is `0.75 × 56 = 42px` tall — legible. (56px was chosen so the common
45-min item clears ~40px even in the densest, scrolling case.)

### Item positioning

Each item is absolutely positioned inside its day column. The pure mapper
emits unitless multipliers; CSS turns them into px against `--hour-px`:

- `topFactor  = (start_min - windowStart) / 60`
- `spanFactor = (end_min - start_min) / 60`
- `style: { top: calc(var(--hour-px) * topFactor),
            height: max(calc(var(--hour-px) * spanFactor), MIN_ITEM_PX) }`

So a 120-min movie is exactly twice the height of a 60-min episode.
`MIN_ITEM_PX = 22px` is a hit-area floor for hand-placed 15-min slivers; a
floored item may visually touch its neighbour, which is acceptable (detail is
on hover / in the menu).

---

## 2. Overlapping items → side-by-side sub-columns

The scheduler never overlaps plan items on a day, but a manual drag can. When
it does, that day's column splits into sub-columns. This is a **pure,
vitest-covered** function — no rendering.

Overlap test (touching edges do NOT overlap):
`overlaps(a, b) = a.start_min < b.end_min && b.start_min < a.end_min`.

Algorithm `layoutDayColumns(items) -> Array<{ item, colIndex, colCount }>`:

1. Sort items by `start_min`, then by longer duration first (stable tiebreak
   on `id` for determinism).
2. Walk the sorted list accumulating a **cluster**: a maximal run where each
   item overlaps at least one already in the cluster (track the cluster's
   running max `end_min`; a new item whose `start_min >= clusterMaxEnd`
   closes the cluster and starts a new one).
3. Within a cluster, greedily assign each item to the first sub-column whose
   last item's `end_min <= item.start_min`; if none, open a new sub-column.
4. `colCount` for every item in a cluster = that cluster's sub-column count
   (peak concurrency). Each item carries its own `colIndex`.

Render: `width = 1/colCount` of the day column (as a percentage), horizontal
offset `= colIndex/colCount`. Non-overlapping items are `colCount === 1`
(full width) — the normal scheduled day is unchanged visually.

---

## 3. Drag-and-drop (@dnd-kit)

Add `@dnd-kit/core` and `@dnd-kit/modifiers` (and `@dnd-kit/utilities` if a
transform helper is needed). One `DndContext` wraps the grid.

- **Draggables:** each item card (`useDraggable`, id = `String(item.id)`),
  disabled when the item is in the past (§4).
- **Droppables:** each day column (`useDroppable`, id = the column's `date`),
  disabled when that date is in the past (§4).
- **Vertical delta → new start:** the drag's vertical translation converts to
  minutes via `hourPx` and snaps to 15:
  `newStart = clamp(snap15(item.start_min + round(deltaY / hourPx * 60)),
  windowStart, windowEnd - duration)` where
  `snap15(m) = round(m / 15) * 15`.
- **Drop column → new date.** A drag can change time only (same column), day
  only (dropped at same vertical), or both.
- **On drop:** one `PATCH /v1/guides/{id}/items/{itemID}` with
  `{ date: <column date>, start_min: newStart }` — the existing move endpoint.
- **Optimism:** the drag mutation does an `onMutate` optimistic cache write
  (cancel `["guide"]`, snapshot, move the item to its new date/start_min in
  the cached `GuideResponse`, return snapshot), `onError` rollback to the
  snapshot, `onSettled` invalidate `["guide"]`. The card lands instantly and
  only snaps back if the server rejects.
- **`hourPx` for the delta math:** read from the same CSS var via
  `getComputedStyle` on the grid element at drag start, or thread the resolved
  number down from the component that sets `--hours` (preferred — compute the
  resolved `hourPx` in JS with the same `max()/clamp` formula off
  `window.innerHeight`, store in a ref, keep it in sync with a resize
  listener). The CSS var stays the single source for layout; JS mirrors the
  formula only for the drag delta.
- **Keyboard:** dnd-kit's `KeyboardSensor` gives accessible keyboard dragging
  for free. The existing click-to-open **ItemMenu** (with its Move date/time
  picker) stays as the non-drag path and the a11y fallback.

---

## 4. Move-rule enforcement (new — does not exist today)

The `PATCH` handler currently validates only date format and `start_min`
range; it will move past items or move items into the past. Both rules are
enforced in **both** layers.

### Rules

- **Cannot move a past slot:** the item's current `date < today` → reject.
- **Cannot move a future slot into the past:** target `date < today` →
  reject.
- (Together: a move is allowed only when both the item's current date and the
  target date are `>= today`.)
- **Window is NOT checked on move** — any `start_min` in `[0, 1440]` is
  accepted regardless of the day's generate window.

### Server

In `store.UpdateGuideItem`, when the update is a **move** (`Date != nil ||
StartMin != nil`), load the item's current `date` under the existing
transaction and enforce both rules against `today = Now().UTC()` (matching how
`handleRegenerate` already defines "today"). Return a new sentinel
`ErrPastMove`; the handler maps it to `422 {"error":"cannot move past slot"}`
(and a distinct message for the past-target case). Title-swap / pin PATCHes
(no date/start_min) are unaffected.

### Client

- Past items render dimmed and are non-draggable (`useDraggable` disabled).
- Past day columns are disabled droppables (no valid drop target; the column
  shows a subtle "locked" affordance).
- These are UX guards; the server is the real enforcement.

### The UTC/local seam (pre-existing, out of scope)

Server "today" is UTC; the client's is local. Around local midnight they can
disagree by a few hours (a slot could look draggable client-side but be
rejected server-side, or vice-versa). This is the already-ledgered
UTC-vs-local `today` issue; the optimistic rollback handles the mismatch
gracefully (the card snaps back with the 422). Not expanded here.

---

## 5. Card & responsive changes

- **Remove the per-item time pill** from the card (time now lives in the
  gutter) — this is the real-estate win. Cards keep: poster tint + hover
  glow/pulse, title, `S1E5`/Movie + provider chip, Pinned badge, the hover
  quick-actions cluster, and the click-to-open ItemMenu.
- Narrow overlap cards truncate the title; full detail remains on hover / in
  the menu.
- **Responsive:** a 7-column grid is unusable on a phone. Below `lg`, the day
  columns become a horizontal snap-scroll (one/two days visible) with the
  **gutter sticky on the left**, preserving the shared axis. This mirrors the
  current mobile snap-scroll. Desktop (`lg+`) is the full 7-column grid.

---

## 6. Architecture / file structure

- `web/src/lib/guide.ts` — new pure exports (vitest-covered):
  - `snap15(min): number`
  - `layoutDayColumns(items): Array<{ item, colIndex, colCount }>`
  - `toTimeGrid(g, today): { windowStart, windowEnd, windowHours,
    days: Array<{ date, dow, isToday, isPast,
    items: Array<{ item, title, providerName, topFactor, spanFactor,
    colIndex, colCount }> }> }`
  - Keep `toCalendarColumns` only if still referenced elsewhere; otherwise
    remove it and its tests with the CalendarView rewrite.
- `web/src/app/guide/CalendarView.tsx` — rewritten: owns the `DndContext`,
  sets `--hours` and resolves `hourPx`, renders the gutter + day columns.
  Kept lean by extracting:
  - `TimeGutter.tsx` — the hour-label rail.
  - `DayColumn.tsx` — a droppable column; positions its items absolutely.
  - `GridItemCard.tsx` — a draggable card (the poster-tinted card minus the
    time pill; reuses `usePosterHue`, `ProviderChip`, `SlotQuickActions`,
    `ItemMenu`).
  - `useGuideItemDrag.ts` — the optimistic move mutation + snap/delta math.
- `api/internal/store/guides.go` — `ErrPastMove`; move enforcement in
  `UpdateGuideItem`.
- `api/internal/httpserver/guides.go` — map `ErrPastMove` → 422.
- `web/package.json` — add `@dnd-kit/core`, `@dnd-kit/modifiers`.

---

## 7. Testing

- **Pure mappers (primary coverage):** `snap15` boundaries; `layoutDayColumns`
  (no overlap → all colCount 1; two overlapping → 2 columns; a 3-deep cluster;
  touching edges do NOT split; a separate non-overlapping item stays
  colCount 1; determinism of ordering); `toTimeGrid` window math (floor/ceil,
  single-item ≥1h, isPast/isToday flags, topFactor/spanFactor values).
- **Server enforcement (Go, `lineup_test` DB only):** move a past item →
  422/ErrPastMove; move a future item to a past date → 422; valid future→future
  move → 200; a window-violating `start_min` on a valid date → still 200
  (window not enforced); a non-move PATCH (pin/title) on a past item → still
  allowed.
- **Optimistic move:** a small vitest around the cache-mutation helper
  (snapshot → move → rollback) if it is factored as a pure function; the
  wiring itself is covered by the final review + manual pass.
- **Drag interaction:** dnd-kit is trusted; validated by the final
  whole-branch review and the user's manual drag-feel pass. Held from
  auto-merge for that pass.

## 8. Rollout / plan shape

One spec, one branch (`feat/calendar-timegrid`), plan tasks in order:
(1) pure layout mappers + vitest; (2) API move enforcement + Go tests;
(3) grid rendering (gutter, columns, positioned cards, vertical scale,
overlap layout) with Board untouched; (4) @dnd-kit wiring + optimistic move +
client past-guards; (5) fable whole-branch review + PR, held for the user's
manual drag-feel pass.
