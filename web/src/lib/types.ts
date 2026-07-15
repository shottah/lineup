// Mirrors the API's /v1/me JSON exactly (see api/internal/store/users.go
// JSON tags and api/internal/prefs — do not rename fields client-side).

export type DayWindow = {
  enabled: boolean;
  start: string; // "HH:MM"
  end: string; // "HH:MM"
};

export type SchedulePrefs = {
  windows: Record<string, DayWindow>;
};

export type User = {
  id: number;
  email: string;
  display_name: string;
  region: string;
  schedule_prefs: SchedulePrefs;
};
