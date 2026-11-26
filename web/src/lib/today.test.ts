import { describe, expect, it } from "vitest";

import { localDate, timezoneToSync } from "./today";

describe("localDate", () => {
  it("formats the local calendar date, not the UTC one", () => {
    // Constructed from local parts, so this holds in any test timezone.
    expect(localDate(new Date(2026, 10, 25, 20, 18))).toBe("2026-11-25");
    expect(localDate(new Date(2026, 0, 5, 0, 0))).toBe("2026-01-05");
  });
});

describe("timezoneToSync", () => {
  it("returns the browser zone when the stored one differs", () => {
    expect(timezoneToSync("", "America/Chicago")).toBe("America/Chicago");
    expect(timezoneToSync("UTC", "America/Chicago")).toBe("America/Chicago");
    expect(timezoneToSync(undefined, "Europe/London")).toBe("Europe/London");
  });

  it("returns null when already in sync or the browser reports nothing", () => {
    expect(timezoneToSync("America/Chicago", "America/Chicago")).toBeNull();
    expect(timezoneToSync("America/Chicago", "")).toBeNull();
    expect(timezoneToSync("America/Chicago", undefined)).toBeNull();
  });
});
