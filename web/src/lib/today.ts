// "Today" on the web is the browser's local calendar date. The API computes
// its own "today" in the user's stored timezone (users.timezone), which
// TimezoneSync keeps equal to the browser's zone, so both sides agree on
// which evening is tonight.

// Local (not UTC) calendar date, YYYY-MM-DD.
export function localDate(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

export function todayLocal(): string {
  return localDate(new Date());
}

// The browser's IANA zone, or undefined when the runtime can't say.
export function browserTimezone(): string | undefined {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || undefined;
  } catch {
    return undefined;
  }
}

// The zone to PATCH onto /v1/me, or null when nothing needs to change.
export function timezoneToSync(stored: string | undefined, browser: string | undefined): string | null {
  if (!browser || browser === stored) return null;
  return browser;
}
