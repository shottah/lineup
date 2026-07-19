import { describe, expect, it } from "vitest";

import type { GuideItem, GuideResponse, GuideTitleLookup } from "./types";
import {
  epLabel,
  fmtTime,
  layoutDayColumns,
  monthDay,
  snap15,
  toBoardRows,
  toCalendarColumns,
  toTimeGrid,
} from "./guide";

// Two-day fixture: day 1 has a movie plan (20:00 on provider 8), a
// watched+pinned series plan (21:30 on provider 9), an alternate sharing
// the 20:00 slot (provider 9), and an alternate for the 21:30 slot
// (provider 8); day 2 is a night off. Title 5 is missing from the
// sidecar (fallback path).
const g: GuideResponse = {
  id: 1,
  start_date: "2026-07-20",
  end_date: "2026-07-21",
  seed: 42,
  items: [
    { id: 11, date: "2026-07-20", start_min: 1200, end_min: 1320, title_id: 1, season: 0, episode: 0, provider_id: 8, is_plan: true, pinned: false, edited: false, watched: false },
    { id: 12, date: "2026-07-20", start_min: 1290, end_min: 1350, title_id: 2, season: 2, episode: 5, provider_id: 9, is_plan: true, pinned: true, edited: false, watched: true },
    { id: 13, date: "2026-07-20", start_min: 1200, end_min: 1260, title_id: 3, season: 1, episode: 3, provider_id: 9, is_plan: false, pinned: false, edited: false, watched: false },
    { id: 14, date: "2026-07-20", start_min: 1290, end_min: 1350, title_id: 5, season: 4, episode: 1, provider_id: 8, is_plan: false, pinned: false, edited: false, watched: false },
  ],
  titles: {
    "1": { name: "Past Lives", kind: "movie", tmdb_id: 666277, poster_path: "/pl.jpg" },
    "2": { name: "Severance", kind: "series", tmdb_id: 95396, poster_path: "/sev.jpg" },
    "3": { name: "Slow Horses", kind: "series", tmdb_id: 95480, poster_path: "/sh.jpg" },
  },
  providers: {
    "8": { id: 8, name: "Netflix", logo_path: "/n.jpg" },
    "9": { id: 9, name: "Apple TV+", logo_path: "/a.jpg" },
  },
};

describe("fmtTime", () => {
  it("formats 12-hour times", () => {
    expect(fmtTime(0)).toBe("12:00 am");
    expect(fmtTime(719)).toBe("11:59 am");
    expect(fmtTime(720)).toBe("12:00 pm");
    expect(fmtTime(1230)).toBe("8:30 pm");
    expect(fmtTime(1439)).toBe("11:59 pm");
  });
});

describe("monthDay", () => {
  it("formats YYYY-MM-DD as Month D", () => {
    expect(monthDay("2026-07-20")).toBe("Jul 20");
    expect(monthDay("2026-01-01")).toBe("Jan 1");
  });
});

describe("epLabel", () => {
  const baseItem: GuideItem = {
    id: 1,
    date: "2026-07-20",
    start_min: 1200,
    end_min: 1260,
    title_id: 2,
    season: 2,
    episode: 5,
    provider_id: 9,
    is_plan: true,
    pinned: false,
    edited: false,
    watched: false,
  };

  it("formats series as SxEy", () => {
    const title: GuideTitleLookup = { name: "Severance", kind: "series", tmdb_id: 1, poster_path: "" };
    expect(epLabel(title, baseItem)).toBe("S2E5");
  });

  it("labels movies as Movie regardless of season/episode", () => {
    const title: GuideTitleLookup = { name: "Past Lives", kind: "movie", tmdb_id: 2, poster_path: "" };
    expect(epLabel(title, { ...baseItem, season: 0, episode: 0 })).toBe("Movie");
  });
});

describe("toCalendarColumns", () => {
  const cols = toCalendarColumns(g, "2026-07-20");

  it("emits one column per date in range", () => {
    expect(cols.map((c) => c.date)).toEqual(["2026-07-20", "2026-07-21"]);
    expect(cols[0].dow).toBe("MON");
    expect(cols[1].dow).toBe("TUE");
  });

  it("marks today as Tonight", () => {
    expect(cols[0].isToday).toBe(true);
    expect(cols[0].sub).toBe("Tonight");
    expect(cols[1].isToday).toBe(false);
    expect(cols[1].sub).toBe("Jul 21");
  });

  it("keeps plan items only, sorted by start", () => {
    expect(cols[0].slots.map((s) => s.item.id)).toEqual([11, 12]);
    expect(cols[1].slots).toEqual([]);
  });

  it("resolves sidecar titles and times", () => {
    const [movie] = cols[0].slots;
    expect(movie.title.name).toBe("Past Lives");
    expect(movie.timeLabel).toBe("8:00 pm");
  });
});

describe("toBoardRows", () => {
  const day = toBoardRows(g, "2026-07-20");

  it("derives hour columns from plan items", () => {
    expect(day.times.map((t) => t.startMin)).toEqual([1200, 1260]);
    expect(day.times.map((t) => t.label)).toEqual(["8:00 pm", "9:00 pm"]);
  });

  it("numbers plan picks through the evening and dims alternates", () => {
    const apple = day.rows.find((r) => r.providerName === "Apple TV+")!;
    const netflix = day.rows.find((r) => r.providerName === "Netflix")!;
    // Netflix: plan movie at 8pm (step 1), alternate at 9:30→9pm column.
    expect(netflix.cells[0]).toMatchObject({ has: true, step: 1 });
    expect(netflix.cells[1]).toMatchObject({ has: true, step: null });
    // Apple: alternate at 8pm, plan series at 9:30→9pm column (step 2).
    expect(apple.cells[0]).toMatchObject({ has: true, step: null });
    expect(apple.cells[1]).toMatchObject({ has: true, step: 2 });
  });

  it("gives alternates a swap target (the plan item sharing their slot)", () => {
    const apple = day.rows.find((r) => r.providerName === "Apple TV+")!;
    const netflix = day.rows.find((r) => r.providerName === "Netflix")!;
    expect((apple.cells[0] as { swapTargetId?: number }).swapTargetId).toBe(11);
    expect((netflix.cells[1] as { swapTargetId?: number }).swapTargetId).toBe(12);
  });

  it("falls back for missing sidecar entries", () => {
    const netflix = day.rows.find((r) => r.providerName === "Netflix")!;
    const alt = netflix.cells[1];
    expect(alt.has && alt.title.name).toBe("Unknown title");
  });

  it("assembles the path from plan titles", () => {
    expect(day.path).toEqual(["Past Lives", "Severance"]);
  });

  it("returns empty structures for a night off", () => {
    const off = toBoardRows(g, "2026-07-21");
    expect(off.rows).toEqual([]);
    expect(off.path).toEqual([]);
  });
});

function gi(over: Partial<GuideItem>): GuideItem {
  return {
    id: 1, date: "2026-07-20", start_min: 1140, end_min: 1200, title_id: 1,
    season: 1, episode: 1, provider_id: 8, is_plan: true, pinned: false,
    edited: false, watched: false, ...over,
  };
}

describe("snap15", () => {
  it("rounds to the nearest 15", () => {
    expect(snap15(1140)).toBe(1140);
    expect(snap15(1147)).toBe(1140);
    expect(snap15(1148)).toBe(1155);
    expect(snap15(1132)).toBe(1125);
  });
});

describe("layoutDayColumns", () => {
  it("gives every non-overlapping item colCount 1", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 1080, end_min: 1140 }),
      gi({ id: 2, start_min: 1140, end_min: 1200 }), // touches edge — not overlap
    ]);
    expect(out.every((o) => o.colCount === 1 && o.colIndex === 0)).toBe(true);
  });

  it("splits two overlapping items into two columns", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 1080, end_min: 1200 }),
      gi({ id: 2, start_min: 1140, end_min: 1260 }),
    ]);
    expect(out.map((o) => o.colCount)).toEqual([2, 2]);
    expect(out.map((o) => o.colIndex).sort()).toEqual([0, 1]);
  });

  it("packs a third non-overlapping-with-first item back into column 0", () => {
    // A[0-120] overlaps B[60-180]; C[120-180] starts when A ends -> reuses A's column
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 120 }),
      gi({ id: 2, start_min: 60, end_min: 180 }),
      gi({ id: 3, start_min: 120, end_min: 180 }),
    ]);
    const byId = new Map(out.map((o) => [o.item.id, o]));
    expect(byId.get(1)!.colCount).toBe(2);
    expect(byId.get(3)!.colIndex).toBe(0); // reused column A vacated
  });

  it("keeps a separate non-overlapping cluster at colCount 1", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 60 }),
      gi({ id: 2, start_min: 30, end_min: 90 }), // cluster A (2 cols)
      gi({ id: 3, start_min: 600, end_min: 660 }), // cluster B alone
    ]);
    expect(out.find((o) => o.item.id === 3)!.colCount).toBe(1);
  });

  it("gives three mutually-overlapping items colCount 3", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 90 }),
      gi({ id: 2, start_min: 30, end_min: 120 }),
      gi({ id: 3, start_min: 60, end_min: 150 }),
    ]);
    expect(out.map((o) => o.colCount)).toEqual([3, 3, 3]);
    expect(out.map((o) => o.colIndex).sort()).toEqual([0, 1, 2]);
  });

  it("keeps a non-transitive chain (A-B, B-C, not A-C) as one cluster at peak concurrency 2", () => {
    // A[0-60] overlaps B[45-105]; B overlaps C[90-150]; A and C do not overlap.
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 60 }),
      gi({ id: 2, start_min: 45, end_min: 105 }),
      gi({ id: 3, start_min: 90, end_min: 150 }),
    ]);
    expect(out.every((o) => o.colCount === 2)).toBe(true); // peak concurrency 2, not 3
    expect(out.find((o) => o.item.id === 3)!.colIndex).toBe(0); // reuses A's vacated column
  });

  it("splits a chain of touching-edge items into isolated single columns", () => {
    const out = layoutDayColumns([
      gi({ id: 1, start_min: 0, end_min: 60 }),
      gi({ id: 2, start_min: 60, end_min: 120 }),
      gi({ id: 3, start_min: 120, end_min: 180 }),
    ]);
    expect(out.every((o) => o.colCount === 1 && o.colIndex === 0)).toBe(true);
  });
});

describe("toTimeGrid", () => {
  const g: GuideResponse = {
    id: 1, start_date: "2026-07-20", end_date: "2026-07-21", seed: 0,
    items: [
      gi({ id: 1, date: "2026-07-20", start_min: 1110, end_min: 1200 }), // 18:30-20:00
      gi({ id: 2, date: "2026-07-21", start_min: 1230, end_min: 1350 }), // 20:30-22:30
      gi({ id: 3, date: "2026-07-20", start_min: 0, end_min: 60, is_plan: false }), // alternate: ignored
    ],
    titles: { "1": { name: "A", kind: "series", tmdb_id: 1, poster_path: "" } },
    providers: { "8": { id: 8, name: "Netflix", logo_path: "" } },
  } as unknown as GuideResponse;

  it("computes the window from plan items only, floored/ceiled to the hour", () => {
    const grid = toTimeGrid(g, "2026-07-20");
    expect(grid.windowStart).toBe(1080); // floor(1110/60)*60 = 18:00
    expect(grid.windowEnd).toBe(1380); // ceil(1350/60)*60 = 23:00
    expect(grid.windowHours).toBe(5); // (1380 - 1080) / 60
  });

  it("sets topFactor/spanFactor relative to windowStart", () => {
    const grid = toTimeGrid(g, "2026-07-20");
    const item1 = grid.days[0].items[0];
    expect(item1.topFactor).toBeCloseTo((1110 - 1080) / 60);
    expect(item1.spanFactor).toBeCloseTo((1200 - 1110) / 60);
  });

  it("flags isToday and isPast per day", () => {
    const grid = toTimeGrid(g, "2026-07-21");
    expect(grid.days[0].isPast).toBe(true); // 07-20 < today 07-21
    expect(grid.days[1].isToday).toBe(true);
  });

  it("returns a zeroed window when there are no plan items", () => {
    const empty = { ...g, items: [] } as GuideResponse;
    const grid = toTimeGrid(empty, "2026-07-20");
    expect(grid.windowHours).toBe(0);
    expect(grid.days.every((d) => d.items.length === 0)).toBe(true);
  });
});
