import { fmtTime } from "@/lib/guide";

// Shared time axis for the week grid (design spec §1): one label per hour
// from windowStart to windowEnd, each row exactly `var(--hour-px)` tall so
// it lines up with every DayColumn's absolutely-positioned items and
// gridlines, which size off the same CSS var. Sticky-left on mobile so the
// axis stays visible while the day columns horizontal snap-scroll under it
// (CalendarView); static within the desktop grid.
export function TimeGutter({ windowStart, windowEnd }: { windowStart: number; windowEnd: number }) {
  const hours: number[] = [];
  for (let m = windowStart; m < windowEnd; m += 60) hours.push(m);

  return (
    <div className="sticky left-0 z-20 w-11 shrink-0 bg-bg lg:static lg:z-auto lg:w-auto lg:bg-transparent">
      {/* Spacer matching DayColumn's header block height, so row 0 below
          starts at the same offset as every day column's body. */}
      <div className="h-12" aria-hidden="true" />
      {hours.map((m) => (
        <div
          key={m}
          className="border-t border-line pl-1 text-left text-[10px] text-faint"
          style={{ height: "var(--hour-px)" }}
        >
          <span className="-translate-y-1/2 block">{fmtTime(m)}</span>
        </div>
      ))}
    </div>
  );
}
