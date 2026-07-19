// Pure mappers from the guide payload to view models. No React, no
// fetching — vitest-covered. Dates are YYYY-MM-DD strings; the only Date
// use is UTC day-of-week/label derivation.

import type { GuideItem, GuideResponse, GuideTitleLookup } from "./types";

const DOW = ["SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"];
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

const UNKNOWN_TITLE: GuideTitleLookup = { name: "Unknown title", kind: "movie", tmdb_id: 0, poster_path: "" };

// "Month D" (e.g. "Jul 20") for a YYYY-MM-DD date — the header's "Week of
// {Month D}" (#18 Task 3) reuses the same month names and UTC parse as
// toCalendarColumns' day labels rather than duplicating them.
export function monthDay(date: string): string {
  const d = utc(date);
  return `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}`;
}

export function fmtTime(startMin: number): string {
  const h24 = Math.floor(startMin / 60) % 24;
  const m = startMin % 60;
  const suffix = h24 < 12 ? "am" : "pm";
  const h12 = h24 % 12 === 0 ? 12 : h24 % 12;
  return `${h12}:${String(m).padStart(2, "0")} ${suffix}`;
}

function utc(date: string): Date {
  return new Date(`${date}T00:00:00Z`);
}

function eachDate(start: string, end: string): string[] {
  const out: string[] = [];
  for (let t = utc(start).getTime(); t <= utc(end).getTime(); t += 86_400_000) {
    out.push(new Date(t).toISOString().slice(0, 10));
  }
  return out;
}

function titleOf(g: GuideResponse, item: GuideItem): GuideTitleLookup {
  return g.titles[String(item.title_id)] ?? UNKNOWN_TITLE;
}

function providerNameOf(g: GuideResponse, item: GuideItem): string {
  return g.providers[String(item.provider_id)]?.name ?? "";
}

// Season/episode label for a slot's sub-line (design spec §5.3): used
// below by toBoardRows' cell sub, and exported so CalendarView can build
// its own sub-line (a flex row of this label plus a separate provider
// chip, rather than toCalendarColumns' pre-joined string) without a
// private copy.
export function epLabel(title: GuideTitleLookup, item: GuideItem): string {
  return title.kind === "series" ? `S${item.season}E${item.episode}` : "Movie";
}

export type CalendarSlot = {
  item: GuideItem;
  title: GuideTitleLookup;
  providerName: string;
  timeLabel: string;
};

export type CalendarColumn = {
  date: string;
  dow: string;
  sub: string;
  isToday: boolean;
  slots: CalendarSlot[];
};

export function toCalendarColumns(g: GuideResponse, today: string): CalendarColumn[] {
  const byDate = new Map<string, GuideItem[]>();
  for (const item of g.items) {
    if (!item.is_plan) continue;
    const list = byDate.get(item.date) ?? [];
    list.push(item);
    byDate.set(item.date, list);
  }
  return eachDate(g.start_date, g.end_date).map((date) => {
    const d = utc(date);
    const slots = (byDate.get(date) ?? [])
      .slice()
      .sort((a, b) => a.start_min - b.start_min)
      .map((item) => {
        const title = titleOf(g, item);
        const providerName = providerNameOf(g, item);
        return {
          item,
          title,
          providerName,
          timeLabel: fmtTime(item.start_min),
        };
      });
    return {
      date,
      dow: DOW[d.getUTCDay()],
      sub: date === today ? "Tonight" : `${MONTHS[d.getUTCMonth()]} ${d.getUTCDate()}`,
      isToday: date === today,
      slots,
    };
  });
}

export type BoardCell =
  | { has: false }
  | {
      has: true;
      item: GuideItem;
      title: GuideTitleLookup;
      sub: string;
      step: number | null;
      swapTargetId?: number;
    };

export type BoardRow = { providerId: number; providerName: string; cells: BoardCell[] };

export type BoardDay = {
  times: { startMin: number; label: string }[];
  rows: BoardRow[];
  path: string[];
};

export function toBoardRows(g: GuideResponse, date: string): BoardDay {
  const dayItems = g.items.filter((it) => it.date === date);
  const plan = dayItems
    .filter((it) => it.is_plan)
    .slice()
    .sort((a, b) => a.start_min - b.start_min);
  if (plan.length === 0) {
    return { times: [], rows: [], path: [] };
  }

  const hourOf = (startMin: number) => Math.floor(startMin / 60) * 60;
  const hours = [...new Set(plan.map((it) => hourOf(it.start_min)))].sort((a, b) => a - b);
  const stepOf = new Map(plan.map((it, i) => [it.id, i + 1]));
  const planBySlot = new Map(plan.map((it) => [it.start_min, it.id]));

  const providerIds = [...new Set(dayItems.map((it) => it.provider_id))];
  const rows: BoardRow[] = providerIds
    .map((pid) => {
      const cells: BoardCell[] = hours.map((hour) => {
        const candidates = dayItems
          .filter((it) => it.provider_id === pid && hourOf(it.start_min) === hour)
          .sort((a, b) => Number(b.is_plan) - Number(a.is_plan) || a.start_min - b.start_min);
        const item = candidates[0];
        if (!item) return { has: false };
        const title = titleOf(g, item);
        const step = item.is_plan ? (stepOf.get(item.id) ?? null) : null;
        const cell: BoardCell = {
          has: true,
          item,
          title,
          step,
          sub: `${epLabel(title, item)} · ${item.is_plan ? "your pick" : "alternate"}`,
        };
        if (!item.is_plan) {
          const target = planBySlot.get(item.start_min);
          if (target !== undefined) cell.swapTargetId = target;
        }
        return cell;
      });
      return {
        providerId: pid,
        providerName: g.providers[String(pid)]?.name ?? "",
        cells,
      };
    })
    .filter((row) => row.cells.some((c) => c.has))
    .sort((a, b) => a.providerName.localeCompare(b.providerName));

  return {
    times: hours.map((h) => ({ startMin: h, label: fmtTime(h) })),
    rows,
    path: plan.map((it) => titleOf(g, it).name),
  };
}

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
