"use client";

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";

import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  pointerWithin,
  rectIntersection,
  useSensor,
  useSensors,
  type CollisionDetection,
  type DragEndEvent,
  type DragStartEvent,
  type KeyboardCoordinateGetter,
  type Modifier,
} from "@dnd-kit/core";
import { restrictToWindowEdges } from "@dnd-kit/modifiers";

import { dropStartMin, monthDay, nowLineFactor, snapDragDelta, toTimeGrid, type TimeGridItem } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { DayColumn } from "./DayColumn";
import { GridDragPreview } from "./GridDragPreview";
import { TimeGutter } from "./TimeGutter";
import { useGuideItemDrag } from "./useGuideItemDrag";

// Time-grid vertical scale (design spec §1): "fill the viewport, floor at
// 66vh, then scroll" lives entirely in these constants plus the
// --hour-px CSS expression below — no JS measurement/ResizeObserver.
const HEADER_OFFSET = 160;
const MIN_HOUR_PX = 56;
const MIN_ITEM_PX = 22;

// The portion of the viewport available to the grid below the page
// header: shared by --hour-px's numerator and the scroll container's own
// max-height, so "fill the available space, then scroll" is one formula
// rather than two that could drift apart.
const AVAILABLE_H = `max(100dvh - ${HEADER_OFFSET}px, 66dvh)`;

// The Calendar tab: one shared time axis (TimeGutter) plus 7 day columns,
// each item absolutely positioned by start time/duration (toTimeGrid).
// Desktop lg+ renders gutter + a 7-column grid; below lg the day columns
// horizontal snap-scroll with the gutter sticky-left. Owns the DndContext
// (design spec §3): drag delta/day drop resolve to a move mutation here,
// while --hour-px stays CSS-driven for layout — hourPxRef below is a
// JS-side mirror of that same formula used only for the drag math. Also
// owns the minute-ticking "now" clock behind today's current-time line.
export function CalendarView({ guide, today }: { guide: GuideResponse; today: string }) {
  const grid = useMemo(() => toTimeGrid(guide, today), [guide, today]);

  const dragM = useGuideItemDrag({ guideId: guide.id });

  // The "now" clock: minutes since local midnight plus the local date
  // (and yesterday's), ticking once a minute. The dates come from the
  // client clock, NOT the server-rendered `today` prop — a tab left open
  // past midnight must move the line to the new day's column, while the
  // frozen prop keeps styling the header labels exactly as served.
  // Starts null (no line) so the server render and the client's first
  // paint agree — the line appears in the mount effect, never during
  // hydration.
  const [now, setNow] = useState<{ min: number; today: string; yesterday: string } | null>(null);
  useEffect(() => {
    const localDate = (d: Date) =>
      `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    const tick = () => {
      const d = new Date();
      const y = new Date(d);
      y.setDate(d.getDate() - 1);
      setNow({ min: d.getHours() * 60 + d.getMinutes(), today: localDate(d), yesterday: localDate(y) });
    };
    tick();
    const id = window.setInterval(tick, 60_000);
    return () => window.clearInterval(id);
  }, []);

  // Which column carries the line, and where. Today's column gets the
  // plain factor; for windows that run past midnight (windowEnd > 1440),
  // the early-morning hours belong to YESTERDAY's column at nowMin +
  // 1440 — at 00:15 under a window ending 02:00, "now" sits in the
  // 24:15 row of the previous day.
  const nowFactorFor = (date: string): number | null => {
    if (now === null) return null;
    if (date === now.today) return nowLineFactor(now.min, grid.windowStart, grid.windowEnd);
    if (date === now.yesterday) return nowLineFactor(now.min + 1440, grid.windowStart, grid.windowEnd);
    return null;
  };

  // Mirrors the --hour-px CSS formula in JS (design spec §3) so the drag's
  // vertical pixel delta can be converted to minutes; CSS remains the
  // single source of truth for actual layout. Recomputed on resize and
  // whenever the window's hour count changes (e.g. after a regenerate).
  const hourPxRef = useRef(MIN_HOUR_PX);
  useEffect(() => {
    if (grid.windowHours === 0) return;
    const updateHourPx = () => {
      hourPxRef.current = Math.max(
        Math.max(window.innerHeight - HEADER_OFFSET, window.innerHeight * 0.66) / grid.windowHours,
        MIN_HOUR_PX,
      );
    };
    updateHourPx();
    window.addEventListener("resize", updateHourPx);
    return () => window.removeEventListener("resize", updateHourPx);
  }, [grid.windowHours]);

  // Keyboard drags move one 15-min slot per vertical arrow press (the
  // default 25px step skips slots at the 56px/hour floor, where a step is
  // 14px) and one day column per horizontal press (measured from a live
  // droppable rect + the lg:gap-2 gutter, so a single press actually
  // reaches the neighboring column).
  const keyboardCoordinates = useCallback<KeyboardCoordinateGetter>((event, { currentCoordinates, context }) => {
    const slot = hourPxRef.current / 4;
    const firstRect = context.droppableRects.values().next().value;
    const col = (firstRect?.width ?? 160) + 8;
    switch (event.code) {
      case "ArrowDown":
        return { ...currentCoordinates, y: currentCoordinates.y + slot };
      case "ArrowUp":
        return { ...currentCoordinates, y: currentCoordinates.y - slot };
      case "ArrowRight":
        return { ...currentCoordinates, x: currentCoordinates.x + col };
      case "ArrowLeft":
        return { ...currentCoordinates, x: currentCoordinates.x - col };
    }
    return undefined;
  }, []);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: keyboardCoordinates }),
  );

  // The grid item captured at drag start. Captured, not re-resolved from
  // the live guide on each render: a mid-drag cache update (error
  // rollback, background refetch) must not shift the drag's reference
  // point or vanish the overlay under the user. State drives the overlay;
  // the ref mirror feeds the (stable-identity) snap modifier.
  const [active, setActive] = useState<TimeGridItem | null>(null);
  const activeRef = useRef<TimeGridItem | null>(null);

  // The vertical scroll container plus its scroll positions at drag
  // start: dnd-kit adds ancestor scroll to event.delta AFTER modifiers
  // run, so to keep the ghost glued to the (scrolled) gridlines the snap
  // modifier has to translate into content space itself.
  const scrollElRef = useRef<HTMLDivElement | null>(null);
  const dragScrollTopRef = useRef(0);
  const dragWinYRef = useRef(0);

  // Snaps the ghost onto the absolute 15-min grid (snap15 of start+delta
  // via snapDragDelta — NOT a quantized delta, which would track parallel
  // to the grid for items scheduled at off-grid starts). Runs before
  // restrictToWindowEdges so the viewport clamp is applied last and the
  // ghost can never be nudged back off-screen by the snap.
  const snapToGrid = useCallback<Modifier>(({ transform }) => {
    const gi = activeRef.current;
    if (!gi) return transform;
    const scrollDelta =
      (scrollElRef.current ? scrollElRef.current.scrollTop - dragScrollTopRef.current : 0) +
      (window.scrollY - dragWinYRef.current);
    const contentY = transform.y + scrollDelta;
    return {
      ...transform,
      y: snapDragDelta(gi.item.start_min, contentY, hourPxRef.current) - scrollDelta,
    };
  }, []);

  // pointerWithin makes the target day follow the cursor rather than the
  // card's near-column-wide rectangle — but it returns nothing when the
  // pointer sits in the day header, a column gap, or the gutter, and
  // keyboard drags have no pointer coordinates at all. Fall back to rect
  // intersection in those cases so a drop always resolves to the column
  // the ghost visibly occupies.
  const collisionDetection = useCallback<CollisionDetection>((args) => {
    const within = pointerWithin(args);
    return within.length > 0 ? within : rectIntersection(args);
  }, []);

  // body[data-dragging] drives the drag-time CSS in globals.css (pulse
  // freeze + pointer-events off on cards) — one attribute flip instead of
  // re-rendering every card. Cleared on end/cancel and on unmount (a
  // route change mid-drag must not strand the attribute).
  useEffect(() => () => document.body.removeAttribute("data-dragging"), []);

  function handleDragStart(event: DragStartEvent) {
    const id = Number(event.active.id);
    const gi = grid.days.flatMap((d) => d.items).find((g) => g.item.id === id) ?? null;
    if (!gi) return;
    setActive(gi);
    activeRef.current = gi;
    dragScrollTopRef.current = scrollElRef.current?.scrollTop ?? 0;
    dragWinYRef.current = window.scrollY;
    document.body.setAttribute("data-dragging", "");
  }

  function endDrag() {
    setActive(null);
    activeRef.current = null;
    document.body.removeAttribute("data-dragging");
  }

  function handleDragEnd(event: DragEndEvent) {
    const gi = active;
    endDrag();
    if (!gi) return;
    const { over, delta } = event;
    const item = gi.item;

    const duration = item.end_min - item.start_min;
    const newStart = dropStartMin(
      item.start_min,
      delta.y,
      hourPxRef.current,
      grid.windowStart,
      grid.windowEnd,
      duration,
    );
    const newDate = over ? String(over.id) : item.date;

    // Same date, same start — a no-op drag: don't fire the mutation.
    if (newDate === item.date && newStart === item.start_min) return;

    dragM.mutate({ itemId: item.id, date: newDate, startMin: newStart });
  }

  if (grid.windowHours === 0) {
    return (
      <div className="flex gap-2 overflow-x-auto pb-2 snap-x lg:overflow-visible lg:grid lg:grid-cols-7">
        {grid.days.map((day) => (
          <div key={day.date} className="flex min-w-[160px] flex-col gap-1.5 snap-start lg:min-w-0">
            <div className="px-1 pt-1 pb-2 text-center">
              <div className={`text-[11px] font-semibold tracking-[0.1em] ${day.isToday ? "text-acc" : "text-ink"}`}>
                {day.dow}
              </div>
              <div className={`text-[10.5px] ${day.isToday ? "text-acc" : "text-faint"}`}>
                {day.isToday ? "Tonight" : monthDay(day.date)}
              </div>
            </div>
            <div className="rounded-xl border border-dashed border-line px-2.5 py-5 text-center text-[11.5px] font-medium text-faint">
              Night off
            </div>
          </div>
        ))}
      </div>
    );
  }

  // The CSS vars live on a wrapper ABOVE the scroll container so the
  // DragOverlay (a sibling of the scroll container, outside its clipping
  // context) can still size the ghost with calc(var(--hour-px) * span).
  const varStyle = {
    "--hours": grid.windowHours,
    "--hour-px": `max(calc(${AVAILABLE_H} / var(--hours)), ${MIN_HOUR_PX}px)`,
    "--win-start": grid.windowStart,
  } as CSSProperties;

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={collisionDetection}
      modifiers={[snapToGrid, restrictToWindowEdges]}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={endDrag}
    >
      <div style={varStyle}>
        <div
          ref={scrollElRef}
          className="flex gap-0 overflow-x-auto pb-2 snap-x lg:grid lg:grid-cols-[auto_repeat(7,minmax(0,1fr))] lg:gap-2"
          style={{ maxHeight: AVAILABLE_H, overflowY: "auto" }}
        >
          <TimeGutter windowStart={grid.windowStart} windowEnd={grid.windowEnd} />
          {grid.days.map((day) => (
            <DayColumn
              key={day.date}
              guide={guide}
              day={day}
              hourCount={grid.windowHours}
              minItemPx={MIN_ITEM_PX}
              nowFactor={nowFactorFor(day.date)}
            />
          ))}
        </div>
        {/* The moving visual: a position-fixed overlay in its own stacking
            context, so it can't be clipped by the scroll container (the
            source card, which no longer moves, stays behind as a dimmed
            placeholder). dropAnimation off: the optimistic cache update
            already lands the card in its new slot on release, and
            animating the overlay on top of that reads as a double
            motion. */}
        <DragOverlay dropAnimation={null}>
          {active && (
            <GridDragPreview
              guide={guide}
              gridItem={active}
              hourPxRef={hourPxRef}
              windowStart={grid.windowStart}
              windowEnd={grid.windowEnd}
              minItemPx={MIN_ITEM_PX}
            />
          )}
        </DragOverlay>
      </div>
    </DndContext>
  );
}
