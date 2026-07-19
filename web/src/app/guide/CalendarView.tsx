"use client";

import { useEffect, useRef, useState, type CSSProperties } from "react";

import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import { restrictToWindowEdges } from "@dnd-kit/modifiers";

import { monthDay, snap15, toTimeGrid } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { DayColumn } from "./DayColumn";
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

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

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

  function handleDragEnd(event: DragEndEvent) {
    const { active, over, delta } = event;
    const itemId = Number(active.id);
    const item = guide.items.find((it) => it.id === itemId);
    if (!item) return;

    const duration = item.end_min - item.start_min;
    const deltaMinutes = Math.round((delta.y / hourPxRef.current) * 60);
    const newStart = clamp(snap15(item.start_min + deltaMinutes), grid.windowStart, grid.windowEnd - duration);
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

  return (
    <DndContext sensors={sensors} modifiers={[restrictToWindowEdges]} onDragEnd={handleDragEnd}>
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
    </DndContext>
  );
}
