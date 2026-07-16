import { describe, expect, it } from "vitest";

import { parseHHMM, toHHMM } from "./WindowSlider";

describe("parseHHMM", () => {
  it("parses ordinary HH:MM into minutes", () => {
    expect(parseHHMM("16:00")).toBe(960);
    expect(parseHHMM("19:00")).toBe(1140);
    expect(parseHHMM("23:00")).toBe(1380);
    expect(parseHHMM("00:00")).toBe(0);
  });

  it("maps the 23:59 sentinel to 1440, never 1439", () => {
    expect(parseHHMM("23:59")).toBe(1440);
    expect(parseHHMM("23:59")).not.toBe(1439);
  });
});

describe("toHHMM", () => {
  it("formats minutes as zero-padded HH:MM", () => {
    expect(toHHMM(960)).toBe("16:00");
    expect(toHHMM(1140)).toBe("19:00");
    expect(toHHMM(1380)).toBe("23:00");
    expect(toHHMM(0)).toBe("00:00");
  });

  it("maps 1440 to the 23:59 sentinel", () => {
    expect(toHHMM(1440)).toBe("23:59");
  });
});

describe("sentinel round-trip", () => {
  it("23:59 -> 1440 -> 23:59", () => {
    expect(toHHMM(parseHHMM("23:59"))).toBe("23:59");
  });

  it("1440 -> 23:59 -> 1440", () => {
    expect(parseHHMM(toHHMM(1440))).toBe(1440);
  });

  it("round-trips every other track stop exactly", () => {
    for (let m = 960; m < 1440; m += 30) {
      expect(parseHHMM(toHHMM(m))).toBe(m);
    }
  });
});
