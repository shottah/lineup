import { describe, expect, it } from "vitest";

import type { GuideResponse } from "./types";
import { fmtTime, toBoardRows, toCalendarColumns } from "./guide";

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
    "1": { name: "Past Lives", kind: "movie", tmdb_id: 666277 },
    "2": { name: "Severance", kind: "series", tmdb_id: 95396 },
    "3": { name: "Slow Horses", kind: "series", tmdb_id: 95480 },
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

  it("resolves sidecars and builds subs", () => {
    const [movie, series] = cols[0].slots;
    expect(movie.title.name).toBe("Past Lives");
    expect(movie.timeLabel).toBe("8:00 pm");
    expect(movie.sub).toBe("Movie · Netflix");
    expect(series.sub).toBe("S2E5 · Apple TV+");
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
