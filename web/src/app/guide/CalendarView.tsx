"use client";

import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";

import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  pointerWithin,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragStartEvent,
  type Modifier,
} from "@dnd-kit/core";
import { restrictToWindowEdges } from "@dnd-kit/modifiers";

import { dropStartMin, monthDay, snapDeltaY, toTimeGrid } from "@/lib/guide";
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
// while --hour-px stays CSS-driven for layout — resolvedHourPx below is a
// JS-side mirror of that same formula used only for the drag math. Also
// owns the "one ItemMenu open at a time" state, same as the pre-grid
// CalendarView, keyed by `${date}-${itemId}`.
export function CalendarView({ guide, today }: { guide: GuideResponse; today: string }) {
  const [openKey, setOpenKey] = useState<string | null>(null);
  const grid = toTimeGrid(guide, today);
  const columns = grid.days.map((d) => ({ date: d.date, dow: d.dow }));

  const dragM = useGuideItemDrag({ guideId: guide.id });

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

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 6 } }),
    useSensor(KeyboardSensor),
  );

  // Snaps the drag's vertical delta to quarter-hour steps at the current
  // hour scale. Applied at the DndContext level so the same snapped delta
  // reaches the DragOverlay transform, onDragMove (the preview's time
  // label), and onDragEnd — visuals and drop math can't drift apart.
  // Stable identity: reads the hour scale through the ref at drag time.
  const snapToQuarterHour = useCallback<Modifier>(
    ({ transform }) => ({ ...transform, y: snapDeltaY(transform.y, hourPxRef.current) }),
    [],
  );

  // Which item the DragOverlay is showing; null when no drag is active.
  const [activeId, setActiveId] = useState<number | null>(null);

  // body[data-dragging] drives the drag-time CSS in globals.css (pulse
  // freeze + pointer-events off on cards) — one attribute flip instead of
  // re-rendering every card. Cleared on end/cancel and on unmount (a
  // route change mid-drag must not strand the attribute).
  useEffect(() => () => document.body.removeAttribute("data-dragging"), []);

  function handleDragStart(event: DragStartEvent) {
    setActiveId(Number(event.active.id));
    document.body.setAttribute("data-dragging", "");
  }

  function endDrag() {
    setActiveId(null);
    document.body.removeAttribute("data-dragging");
  }

  function handleDragEnd(event: DragEndEvent) {
    endDrag();
    const { active, over, delta } = event;
    const itemId = Number(active.id);
    const item = guide.items.find((it) => it.id === itemId);
    if (!item) return;

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

    // Same date, same start — a no-op drag (or the click that opens
    // ItemMenu, which never reaches here past the activation distance
    // anyway): don't fire the mutation.
    if (newDate === item.date && newStart === item.start_min) return;

    dragM.mutate({ itemId, date: newDate, startMin: newStart });
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

  const gridStyle = {
    "--hours": grid.windowHours,
    "--hour-px": `max(calc(${AVAILABLE_H} / var(--hours)), ${MIN_HOUR_PX}px)`,
    "--win-start": grid.windowStart,
    maxHeight: AVAILABLE_H,
    overflowY: "auto",
  } as CSSProperties;

  const activeGridItem =
    activeId === null
      ? null
      : (grid.days.flatMap((d) => d.items).find((gi) => gi.item.id === activeId) ?? null);

  return (
    <DndContext
      sensors={sensors}
      // Resolve the target day from the pointer position, not the card's
      // rectangle overlap — a card is nearly a column wide, so rect
      // intersection flips the day before the cursor crosses the border.
      collisionDetection={pointerWithin}
      modifiers={[restrictToWindowEdges, snapToQuarterHour]}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      onDragCancel={endDrag}
    >
      <div
        className="flex gap-0 overflow-x-auto pb-2 snap-x lg:grid lg:grid-cols-[auto_repeat(7,minmax(0,1fr))] lg:gap-2"
        style={gridStyle}
      >
        <TimeGutter windowStart={grid.windowStart} windowEnd={grid.windowEnd} />
        {grid.days.map((day) => (
          <DayColumn
            key={day.date}
            guide={guide}
            day={day}
            hourCount={grid.windowHours}
            columns={columns}
            minItemPx={MIN_ITEM_PX}
            openKey={openKey}
            onToggleOpen={(key) => setOpenKey((cur) => (cur === key ? null : key))}
            onClose={() => setOpenKey(null)}
          />
        ))}
      </div>
      {/* The moving visual: a position-fixed overlay in its own stacking
          context, so it can't be clipped by this container's overflow
          (the source card, which no longer moves, stays behind as a
          dimmed placeholder). dropAnimation off: the optimistic cache
          update already lands the card in its new slot on release, and
          animating the overlay on top of that reads as a double motion. */}
      <DragOverlay dropAnimation={null}>
        {activeGridItem && (
          <GridDragPreview
            guide={guide}
            gridItem={activeGridItem}
            hourPxRef={hourPxRef}
            windowStart={grid.windowStart}
            windowEnd={grid.windowEnd}
          />
        )}
      </DragOverlay>
    </DndContext>
  );
}
