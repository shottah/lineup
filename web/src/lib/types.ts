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

// --- Search + title pages (#16). Mirrors /v1/search and /v1/titles JSON
// (see api/internal/httpserver/titles.go and api/internal/store/titles.go
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
  id: number;
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
  title_id: number;
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

export type TitleFull = {
  title: Title;
  seasons: SeasonRow[];
  providers: ProviderRow[];
  entry: Entry | null;
};

// --- Profile shelves (#17). Mirrors /v1/me/shelves/{shelf}.

export type ShelfName = "watchlist" | "rotation" | "watched" | "favorites" | "ratings";

export type ShelfResponse = {
  entries: Entry[];
};

// --- Guide (#18). Mirrors /v1/guides JSON incl. the sidecar maps.

export type GuideItem = {
  id: number;
  date: string; // "YYYY-MM-DD"
  start_min: number; // minutes from midnight
  end_min: number;
  title_id: number;
  season: number;
  episode: number;
  provider_id: number;
  is_plan: boolean;
  pinned: boolean;
  edited: boolean;
  watched: boolean;
};

export type GuideTitleLookup = {
  name: string;
  kind: "movie" | "series";
  tmdb_id: number;
};

export type GuideResponse = {
  id: number;
  start_date: string;
  end_date: string;
  seed: number;
  items: GuideItem[];
  titles: Record<string, GuideTitleLookup>;
  providers: Record<string, ProviderRow>;
};
