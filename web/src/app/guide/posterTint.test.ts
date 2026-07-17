import { describe, expect, it, vi } from "vitest";

import {
  circularMeanHue,
  classifyPlate,
  dominantHue,
  hashHue,
  logoPlate,
  meanLuminance,
  peekLogoPlate,
  peekPosterHue,
  posterHue,
  rgbToHsl,
} from "./posterTint";

describe("hashHue", () => {
  it("is deterministic for a given title_id", () => {
    expect(hashHue(12345)).toBe(hashHue(12345));
    expect(hashHue(95396)).toBe(hashHue(95396));
  });

  it("matches the spec's Knuth multiplicative hash (title_id * 2654435761 % 360)", () => {
    // Hand-verified via BigInt arithmetic (avoids the Number precision
    // loss that plain `*` hits once title_id * 2654435761 exceeds 2^53).
    expect(hashHue(0)).toBe(0);
    expect(hashHue(1)).toBe(241);
    expect(hashHue(2)).toBe(122);
    expect(hashHue(12345)).toBe(105);
  });

  it("always returns an integer in [0, 360)", () => {
    for (const id of [0, 1, 2, 3, 4, 5, 1000, 95396, 666277, 999999]) {
      const hue = hashHue(id);
      expect(Number.isInteger(hue)).toBe(true);
      expect(hue).toBeGreaterThanOrEqual(0);
      expect(hue).toBeLessThan(360);
    }
  });

  it("gives different title_ids different hues (well distributed, not proof of no collisions)", () => {
    const hues = new Set([1, 2, 3, 4, 5].map(hashHue));
    expect(hues.size).toBeGreaterThan(1);
  });
});


describe("rgbToHsl", () => {
  it("converts primary colors", () => {
    expect(rgbToHsl(255, 0, 0)).toMatchObject({ h: 0, s: 1, l: 0.5 });
    expect(rgbToHsl(0, 255, 0)).toMatchObject({ h: 120, s: 1, l: 0.5 });
    expect(rgbToHsl(0, 0, 255)).toMatchObject({ h: 240, s: 1, l: 0.5 });
  });

  it("gives gray zero saturation regardless of hue", () => {
    expect(rgbToHsl(128, 128, 128).s).toBe(0);
    expect(rgbToHsl(0, 0, 0).l).toBe(0);
    expect(rgbToHsl(255, 255, 255).l).toBe(1);
  });
});

describe("circularMeanHue", () => {
  it("wraps correctly across the 359°→0° boundary (plain mean would be wrong)", () => {
    // Naive arithmetic mean of [350, 10] is 180 — exactly wrong per spec §2.3
    // step 6. The circular mean must land near 0/360 instead.
    const mean = circularMeanHue([350, 10]);
    expect(mean < 20 || mean > 340).toBe(true);
  });

  it("returns the shared hue when all inputs agree", () => {
    expect(circularMeanHue([120, 120, 120])).toBeCloseTo(120, 5);
  });

  it("averages two hues on the same side of the wraparound normally", () => {
    expect(circularMeanHue([100, 140])).toBeCloseTo(120, 5);
  });
});

describe("dominantHue", () => {
  it("abandons extraction (returns null) when fewer than 10% of pixels survive the filter", () => {
    // All gray (saturation 0) — filtered out entirely.
    const pixels = Array.from({ length: 216 }, () => ({ r: 128, g: 128, b: 128 }));
    expect(dominantHue(pixels)).toBeNull();
  });

  it("abandons extraction when survivors sit just under the 10% floor", () => {
    const grayPixels = Array.from({ length: 195 }, () => ({ r: 128, g: 128, b: 128 }));
    // 21/216 ≈ 9.7% — under the 10% floor.
    const redPixels = Array.from({ length: 21 }, () => ({ r: 200, g: 20, b: 20 }));
    expect(dominantHue([...grayPixels, ...redPixels])).toBeNull();
  });

  it("computes the circular mean hue of surviving pixels once the 10% floor is cleared", () => {
    const grayPixels = Array.from({ length: 190 }, () => ({ r: 128, g: 128, b: 128 }));
    // 26/216 ≈ 12% — clears the 10% floor. All pure red (hue 0).
    const redPixels = Array.from({ length: 26 }, () => ({ r: 200, g: 20, b: 20 }));
    const hue = dominantHue([...grayPixels, ...redPixels]);
    expect(hue).not.toBeNull();
    expect(hue).toBeCloseTo(0, 0);
  });

  it("filters out near-black and near-white pixels as letterboxing/blown highlights", () => {
    const black = Array.from({ length: 190 }, () => ({ r: 1, g: 1, b: 1 })); // l < 0.08
    const red = Array.from({ length: 26 }, () => ({ r: 200, g: 20, b: 20 }));
    expect(dominantHue([...black, ...red])).toBeCloseTo(0, 0);
  });
});

describe("meanLuminance", () => {
  it("gives white pixels luminance near 1", () => {
    const pixels = [{ r: 255, g: 255, b: 255, a: 255 }];
    expect(meanLuminance(pixels)).toBeCloseTo(1, 5);
  });

  it("gives black pixels luminance 0", () => {
    const pixels = [{ r: 0, g: 0, b: 0, a: 255 }];
    expect(meanLuminance(pixels)).toBe(0);
  });

  it("skips near-fully-transparent padding (alpha <= 10)", () => {
    const pixels = [
      { r: 255, g: 255, b: 255, a: 5 }, // ignored
      { r: 0, g: 0, b: 0, a: 255 },
    ];
    expect(meanLuminance(pixels)).toBe(0);
  });

  it("weights by alpha", () => {
    const pixels = [
      { r: 255, g: 255, b: 255, a: 255 },
      { r: 0, g: 0, b: 0, a: 25 },
    ];
    // Heavily weighted toward the opaque white pixel.
    expect(meanLuminance(pixels)).toBeGreaterThan(0.85);
  });
});

describe("classifyPlate", () => {
  it("classifies bright marks (mean luminance > 0.75) as needing a dark plate", () => {
    expect(classifyPlate(0.9)).toBe("plate-dark");
    expect(classifyPlate(0.76)).toBe("plate-dark");
  });

  it("classifies dark/colored marks as needing a light plate", () => {
    expect(classifyPlate(0.75)).toBe("plate-light");
    expect(classifyPlate(0.1)).toBe("plate-light");
    expect(classifyPlate(0)).toBe("plate-light");
  });
});

// posterHue/logoPlate orchestration: cache + memoization + fallback
// behavior, exercised with an injected fake extractor so the real
// canvas/Image sampling (browser-only, not available under vitest's
// node environment — see the module for why) never runs in tests.

describe("posterHue", () => {
  it("resolves to the extractor's hue on success", async () => {
    const extract = vi.fn().mockResolvedValue(200);
    const hue = await posterHue(101, "/poster.jpg", extract);
    expect(hue).toBe(200);
    expect(extract).toHaveBeenCalledTimes(1);
  });

  it("memoizes: a second call for the same title_id does not re-invoke the extractor", async () => {
    const extract = vi.fn().mockResolvedValue(55);
    await posterHue(102, "/poster.jpg", extract);
    const second = await posterHue(102, "/poster.jpg", extract);
    expect(second).toBe(55);
    expect(extract).toHaveBeenCalledTimes(1);
  });

  it("dedupes concurrent in-flight calls for the same title_id", async () => {
    let release!: (hue: number) => void;
    const pending = new Promise<number>((resolve) => {
      release = resolve;
    });
    const extract = vi.fn().mockReturnValue(pending);

    const first = posterHue(103, "/poster.jpg", extract);
    const second = posterHue(103, "/poster.jpg", extract);
    release(77);

    expect(await first).toBe(77);
    expect(await second).toBe(77);
    expect(extract).toHaveBeenCalledTimes(1);
  });

  it("falls back to hashHue(title_id) when extraction fails, and caches the fallback", async () => {
    const extract = vi.fn().mockRejectedValue(new Error("decode error"));
    const hue = await posterHue(104, "/poster.jpg", extract);
    expect(hue).toBe(hashHue(104));

    const second = await posterHue(104, "/poster.jpg", extract);
    expect(second).toBe(hashHue(104));
    expect(extract).toHaveBeenCalledTimes(1); // not retried
  });

  it("falls back to hashHue(title_id) without calling the extractor when poster_path is empty", async () => {
    const extract = vi.fn();
    const hue = await posterHue(105, "", extract);
    expect(hue).toBe(hashHue(105));
    expect(extract).not.toHaveBeenCalled();
  });
});

// peekPosterHue: synchronous read of the same cache posterHue populates —
// zero-flash pattern lets a render read an already-resolved hue without
// awaiting.

describe("peekPosterHue", () => {
  it("returns null for a title_id that has never been requested", () => {
    expect(peekPosterHue(999101)).toBeNull();
  });

  it("returns null while extraction is still in flight (never observes a pending promise)", async () => {
    let release!: (hue: number) => void;
    const pending = new Promise<number>((resolve) => {
      release = resolve;
    });
    const extract = vi.fn().mockReturnValue(pending);

    const inFlight = posterHue(106, "/poster.jpg", extract);
    expect(peekPosterHue(106)).toBeNull();

    release(88);
    await inFlight;
  });

  it("returns the resolved hue once the corresponding posterHue call has completed", async () => {
    const extract = vi.fn().mockResolvedValue(150);
    await posterHue(107, "/poster.jpg", extract);
    expect(peekPosterHue(107)).toBe(150);
  });

  it("returns the resolved fallback hue once a failed extraction has completed", async () => {
    const extract = vi.fn().mockRejectedValue(new Error("decode error"));
    await posterHue(108, "/poster.jpg", extract);
    expect(peekPosterHue(108)).toBe(hashHue(108));
  });
});

describe("logoPlate", () => {
  it("resolves to the sampled plate on success", async () => {
    const sample = vi.fn().mockResolvedValue("plate-dark");
    const plate = await logoPlate(201, "/netflix.png", sample);
    expect(plate).toBe("plate-dark");
    expect(sample).toHaveBeenCalledTimes(1);
  });

  it("memoizes per provider_id: a second call does not re-sample", async () => {
    const sample = vi.fn().mockResolvedValue("plate-light");
    await logoPlate(202, "/apple.png", sample);
    const second = await logoPlate(202, "/apple.png", sample);
    expect(second).toBe("plate-light");
    expect(sample).toHaveBeenCalledTimes(1);
  });

  it("falls back to text-fallback when sampling fails, and caches it (no retry)", async () => {
    const sample = vi.fn().mockRejectedValue(new Error("tainted canvas"));
    const plate = await logoPlate(203, "/broken.png", sample);
    expect(plate).toBe("text-fallback");

    const second = await logoPlate(203, "/broken.png", sample);
    expect(second).toBe("text-fallback");
    expect(sample).toHaveBeenCalledTimes(1);
  });

  it("goes straight to text-fallback without sampling when logo_path is empty", async () => {
    const sample = vi.fn();
    const plate = await logoPlate(204, "", sample);
    expect(plate).toBe("text-fallback");
    expect(sample).not.toHaveBeenCalled();
  });
});

// peekLogoPlate: synchronous read of the same cache logoPlate populates —
// mirrors peekPosterHue's semantics above.

describe("peekLogoPlate", () => {
  it("returns null for a provider_id that has never been requested", () => {
    expect(peekLogoPlate(999201)).toBeNull();
  });

  it("returns null while sampling is still in flight (never observes a pending promise)", async () => {
    let release!: (plate: "plate-light" | "plate-dark") => void;
    const pending = new Promise<"plate-light" | "plate-dark">((resolve) => {
      release = resolve;
    });
    const sample = vi.fn().mockReturnValue(pending);

    const inFlight = logoPlate(205, "/netflix.png", sample);
    expect(peekLogoPlate(205)).toBeNull();

    release("plate-dark");
    await inFlight;
  });

  it("returns the resolved plate once the corresponding logoPlate call has completed", async () => {
    const sample = vi.fn().mockResolvedValue("plate-light");
    await logoPlate(206, "/apple.png", sample);
    expect(peekLogoPlate(206)).toBe("plate-light");
  });

  it("returns the resolved text-fallback once a failed sample has completed", async () => {
    const sample = vi.fn().mockRejectedValue(new Error("tainted canvas"));
    await logoPlate(207, "/broken.png", sample);
    expect(peekLogoPlate(207)).toBe("text-fallback");
  });
});
