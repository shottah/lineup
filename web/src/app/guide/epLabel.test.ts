import { describe, expect, it } from "vitest";

import type { GuideItem, GuideTitleLookup } from "@/lib/types";

import { epLabel } from "./epLabel";

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

describe("epLabel", () => {
  it("formats series as SxEy", () => {
    const title: GuideTitleLookup = { name: "Severance", kind: "series", tmdb_id: 1, poster_path: "" };
    expect(epLabel(title, baseItem)).toBe("S2E5");
  });

  it("labels movies as Movie regardless of season/episode", () => {
    const title: GuideTitleLookup = { name: "Past Lives", kind: "movie", tmdb_id: 2, poster_path: "" };
    expect(epLabel(title, { ...baseItem, season: 0, episode: 0 })).toBe("Movie");
  });
});
