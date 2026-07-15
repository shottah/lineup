import type { GuideResponse } from "@/lib/types";

// Placeholder for #18 Task 4: the 7-column calendar grid (STACKED slot
// cards, ItemMenu, "Night off" empty days) per spec §CalendarView. This
// stub only keeps GuideBody's view toggle compiling — Task 4 replaces
// the body entirely; the props are already the ones that task's mapper
// call (toCalendarColumns(guide, today)) needs.
export function CalendarView({ guide, today }: { guide: GuideResponse; today: string }) {
  return (
    <div
      data-guide-id={guide.id}
      data-today={today}
      className="rounded-xl border border-dashed border-line py-16 text-center text-sm text-mut"
    >
      Loading view…
    </div>
  );
}
