import { describe, expect, it } from "vitest";

import type { GuideResponse } from "@/lib/types";

import { moveItemInGuide } from "./useGuideItemDrag";

const g: GuideResponse = {
  id: 1,
  start_date: "2026-07-20",
  end_date: "2026-07-21",
  seed: 42,
  items: [
    { id: 11, date: "2026-07-20", start_min: 1200, end_min: 1320, title_id: 1, season: 0, episode: 0, provider_id: 8, is_plan: true, pinned: false, edited: false, watched: false },
    { id: 12, date: "2026-07-20", start_min: 1290, end_min: 1350, title_id: 2, season: 2, episode: 5, provider_id: 9, is_plan: true, pinned: true, edited: false, watched: true },
  ],
  titles: {},
  providers: {},
};

describe("moveItemInGuide", () => {
  it("updates the moved item's date/start_min and recomputes end_min from its own duration", () => {
    const out = moveItemInGuide(g, 11, "2026-07-21", 600);
    const moved = out.items.find((it) => it.id === 11)!;
    expect(moved.date).toBe("2026-07-21");
    expect(moved.start_min).toBe(600);
    expect(moved.end_min).toBe(720); // duration was 120 (1320-1200)
  });

  it("leaves every other item untouched", () => {
    const out = moveItemInGuide(g, 11, "2026-07-21", 600);
    const untouched = out.items.find((it) => it.id === 12)!;
    expect(untouched).toEqual(g.items[1]);
  });

  it("leaves the source guide object untouched (no mutation)", () => {
    const before = JSON.parse(JSON.stringify(g));
    moveItemInGuide(g, 11, "2026-07-21", 600);
    expect(g).toEqual(before);
  });

  it("is a no-op copy when the itemId doesn't match any item", () => {
    const out = moveItemInGuide(g, 999, "2026-07-21", 600);
    expect(out.items).toEqual(g.items);
  });
});
