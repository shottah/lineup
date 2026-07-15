import type { GuideResponse } from "@/lib/types";

// Placeholder for #18 Task 5: the day-chip + provider/hour grid board
// (step numbering, alternate swap-in) per spec §BoardView. This stub
// only keeps GuideBody's view toggle compiling — Task 5 replaces the
// body entirely; the props are already the ones that task's mapper call
// (toBoardRows(guide, date)) needs.
export function BoardView({ guide, today }: { guide: GuideResponse; today: string }) {
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
