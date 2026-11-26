// Mirrors the API's /v1/me JSON exactly (see api/internal/docstore/users.go
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
  id: string;
  email: string;
  display_name: string;
  region: string;
  schedule_prefs: SchedulePrefs;
  // IANA zone ("America/Chicago") the API uses for "today"; "" means UTC.
  // Kept in sync with the browser's zone by TimezoneSync.
  timezone: string;
};

// --- Search + title pages (#16). Mirrors /v1/search and /v1/titles JSON
// (see api/internal/httpserver/titles.go and api/internal/docstore/titles.go
// JSON tags — do not rename fields client-side).

export type SearchResult = {
  tmdb_id: number;
  kind: "movie" | "series";
  name: string;
  overview: string;
  poster_path: string;
  year: string; // "1999" or ""
};

export type SearchResponse = {
  results: SearchResult[];
};

export type Title = {
  id: string;
  tmdb_id: number;
  kind: "movie" | "series";
  name: string;
  overview: string;
  poster_path: string;
  runtime_minutes: number;
  airing: boolean;
};

export type SeasonRow = {
  number: number;
  episode_count: number;
};

export type ProviderRow = {
  id: number;
  name: string;
  logo_path: string;
};

export type Pointer = {
  season: number;
  episode: number;
};

export type EntryStatus = "none" | "watchlist" | "rotation" | "watched";

export type Entry = {
  title_id: string;
  tmdb_id: number;
  kind: "movie" | "series";
  name: string;
  poster_path: string;
  runtime_minutes: number;
  airing: boolean;
  status: EntryStatus;
  rating: number | null;
  favorite: boolean;
  pointer: Pointer;
  added_at: string;
  watched_at: string | null;
};

// How a title is available on its providers in the user's region: with a
// subscription, or as a paid rental/purchase (API falls back stream > rent
// > buy and never mixes them).
export type Offer = "stream" | "rent" | "buy";

export type TitleFull = {
  title: Title;
  seasons: SeasonRow[];
  providers: ProviderRow[];
  // "" when there are no providers. Optional for pre-offer API payloads.
  offer?: Offer | "";
  entry: Entry | null;
};

// --- Profile shelves (#17). Mirrors /v1/me/shelves/{shelf}.

export type ShelfName = "watchlist" | "rotation" | "watched" | "favorites" | "ratings";

export type ShelfResponse = {
  entries: Entry[];
};

// --- Guide (#18). Mirrors /v1/guides JSON incl. the sidecar maps.

export type GuideItem = {
  id: string;
  date: string; // "YYYY-MM-DD"
  start_min: number; // minutes from midnight
  end_min: number;
  title_id: string;
  season: number;
  episode: number;
  provider_id: number;
  // Optional so payloads from an API deploy that predates offers still
  // type-check; absent means "stream".
  offer?: Offer;
  is_plan: boolean;
  pinned: boolean;
  edited: boolean;
  watched: boolean;
};

export type GuideTitleLookup = {
  name: string;
  kind: "movie" | "series";
  tmdb_id: number;
  poster_path: string;
};

export type GuideResponse = {
  id: string;
  start_date: string;
  end_date: string;
  seed: number; // UnixNano exceeds 2^53; JSON parsing truncates it. Display/echo only — never round-trip.
  items: GuideItem[];
  titles: Record<string, GuideTitleLookup>;
  providers: Record<string, ProviderRow>;
  // Enabled viewing window per guide day (disabled days absent), from the
  // user's current prefs. Sizes the calendar axis. Optional for older API
  // payloads.
  windows?: GuideDayWindow[];
};

export type GuideDayWindow = {
  date: string; // "YYYY-MM-DD"
  start_min: number;
  end_min: number;
};
