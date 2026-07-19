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

// A droppable-to-be day column (drag lands in Task 4): the day header
// (dow + date, matching the pre-grid header styling) above a
// position:relative body whose items are absolutely placed by toTimeGrid's
// per-item factors. Past days get a dimmed/locked visual treatment only —
// no interaction changes in this task.
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
        className={`relative border-l border-line ${day.isPast ? "opacity-60 grayscale-[25%]" : ""}`}
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
