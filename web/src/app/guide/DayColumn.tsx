"use client";

import { useDroppable } from "@dnd-kit/core";

import { monthDay, type TimeGridDay } from "@/lib/guide";
import type { GuideResponse } from "@/lib/types";

import { GridItemCard } from "./GridItemCard";

export type DayColumnProps = {
  guide: GuideResponse;
  day: TimeGridDay;
  // Number of hour rows in the shared window — used only to draw one
  // gridline per hour; the actual pixel math comes from the --hour-px/
  // --hours CSS vars set by CalendarView on a shared ancestor.
  hourCount: number;
  columns: { date: string; dow: string }[];
  minItemPx: number;
  openKey: string | null;
  onToggleOpen: (key: string) => void;
  onClose: () => void;
};

// A droppable day column (design spec §3/§4): the day header (dow + date,
// matching the pre-grid header styling) above a position:relative body
// whose items are absolutely placed by toTimeGrid's per-item factors.
// Disabled as a drop target when the day is past — dnd-kit excludes
// disabled droppables from collision detection, so a drag simply can't
// resolve `over` to a past column. Past days also get the pre-existing
// dimmed/grayscale treatment, which doubles as the "locked" affordance.
export function DayColumn({
  guide,
  day,
  hourCount,
  columns,
  minItemPx,
  openKey,
  onToggleOpen,
  onClose,
}: DayColumnProps) {
  const gridlines = Array.from({ length: hourCount }, (_, i) => i);
  const { setNodeRef, isOver } = useDroppable({ id: day.date, disabled: day.isPast });

  return (
    <div className="flex min-w-[160px] flex-col snap-start lg:min-w-0">
      <div className={`flex h-12 flex-col items-center justify-center px-1 text-center ${day.isPast ? "opacity-60" : ""}`}>
        <div className={`text-[11px] font-semibold tracking-[0.1em] ${day.isToday ? "text-acc" : "text-ink"}`}>
          {day.dow}
        </div>
        <div className={`text-[10.5px] ${day.isToday ? "text-acc" : "text-faint"}`}>
          {day.isToday ? "Tonight" : monthDay(day.date)}
        </div>
      </div>

      <div
        ref={setNodeRef}
        className={`relative border-l border-line ${day.isPast ? "opacity-60 grayscale-[25%]" : ""} ${
          isOver && !day.isPast ? "bg-acc-soft/40" : ""
        }`}
        style={{ height: "calc(var(--hours) * var(--hour-px))" }}
      >
        {gridlines.map((i) => (
          <div
            key={i}
            aria-hidden="true"
            className="absolute inset-x-0 border-t border-line"
            style={{ top: `calc(var(--hour-px) * ${i})` }}
          />
        ))}

        {day.items.length === 0 && (
          <div className="m-1.5 rounded-xl border border-dashed border-line px-2.5 py-5 text-center text-[11.5px] font-medium text-faint">
            Night off
          </div>
        )}

        {day.items.map((gridItem) => {
          const key = `${day.date}-${gridItem.item.id}`;
          return (
            <GridItemCard
              key={key}
              guide={guide}
              gridItem={gridItem}
              columnDate={day.date}
              columnDow={day.dow}
              columns={columns}
              minItemPx={minItemPx}
              isPast={day.isPast}
              open={openKey === key}
              onToggleOpen={() => onToggleOpen(key)}
              onClose={onClose}
            />
          );
        })}
      </div>
    </div>
  );
}
