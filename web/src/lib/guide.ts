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

function epLabel(title: GuideTitleLookup, item: GuideItem): string {
  return title.kind === "series" ? `S${item.season}E${item.episode}` : "Movie";
}

export type CalendarSlot = {
  item: GuideItem;
  title: GuideTitleLookup;
  providerName: string;
  timeLabel: string;
  sub: string;
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
          sub: providerName ? `${epLabel(title, item)} · ${providerName}` : epLabel(title, item),
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
