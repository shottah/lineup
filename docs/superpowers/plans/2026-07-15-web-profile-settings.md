# Web Profile Shelves + Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/profile` (five shelf tabs with rotation capacity meter and next-episode badges) and `/settings` (region + per-day viewing windows), per issue #17 and the approved spec `docs/superpowers/specs/2026-07-15-web-profile-settings-design.md`.

**Architecture:** One small API prep commit (add `tmdb_id` to the entry JSON so shelf cards can link to title pages), then client pages on the #16 patterns: lazy per-shelf `useQuery` keyed `["shelf", name]` (already invalidated by EntryActions), a controlled settings form PATCHing the full prefs document. Three ledgered #16 polish items ride along.

**Tech Stack:** Go (api), Next 16.2.10 App Router / React 19 / TanStack Query 5 / Tailwind v4 (web), pnpm.

## Global Constraints

- Branch `feat/17-web-profile-settings`. No new dependencies (Go or JS).
- Web gate per task: `cd web && pnpm lint && pnpm build` — clean, zero warnings.
- Go gate (Task 1): `gofmt -l` silent, `go vet` clean, full `go test ./...` green — store tests need the running `lineup-pg` container via `TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable'` (start with `docker start lineup-pg`), and must also pass (skip) WITHOUT the env var.
- API JSON snake_case mirrored verbatim in TS; `PATCH /v1/me` takes the FULL `schedule_prefs` document (seven lowercase day keys mon…sun, zero-padded `HH:MM`, start<end on EVERY row — enabled or not).
- Query keys: shelves `["shelf", name]`; me `["me"]`.
- Toast copy exactly: `Settings saved` and `Couldn't save — try again.`
- Rotation cap constant 8; meter copy `n of 8 rotation slots used`.
- Plain `<img>` stays deliberate (existing per-file eslint-disable in TitleCard).

---

### Task 1: API — `tmdb_id` in the entry payload

**Files:**
- Modify: `api/internal/store/entries.go`
- Modify: `api/internal/store/entries_test.go`
- Modify: `api/internal/httpserver/entries_test.go`
- Modify: `web/src/lib/types.ts`

**Interfaces:**
- Consumes: `entryColumns`/`scanEntry` (entries.go:50-61), `Entry` struct (entries.go:25-38).
- Produces: `Entry.TMDBID int64` (`json:"tmdb_id"`) — every entry/shelf payload carries the TMDB id; TS `Entry` gains `tmdb_id: number`. Tasks 3+ rely on `entry.tmdb_id` for title-page links.

- [ ] **Step 1: Write the failing test**

In `api/internal/store/entries_test.go`, in `TestUpsertEntryLifecycle`, replace:

```go
	if e.TitleID != tid || e.Status != "none" || e.Rating == nil || *e.Rating != 3.5 ||
		e.Pointer.Season != 1 || e.Pointer.Episode != 1 || e.WatchedAt != nil || e.Name != "Entry Lifecycle Show" {
		t.Fatalf("insert entry = %+v", e)
	}
```

with:

```go
	if e.TitleID != tid || e.Status != "none" || e.Rating == nil || *e.Rating != 3.5 ||
		e.Pointer.Season != 1 || e.Pointer.Episode != 1 || e.WatchedAt != nil || e.Name != "Entry Lifecycle Show" {
		t.Fatalf("insert entry = %+v", e)
	}
	// Shelf cards link to /title/{kind}/{tmdbId}; the payload must carry
	// the TMDB id (#17).
	if e.TMDBID == 0 {
		t.Fatalf("entry TMDBID = 0, want the seeded title's tmdb_id")
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `docker start lineup-pg 2>/dev/null; cd api && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./internal/store/ -run TestUpsertEntryLifecycle`
Expected: FAIL — compile error `e.TMDBID undefined`.

- [ ] **Step 3: Implement**

In `api/internal/store/entries.go`, three replacements.

Replace (in the `Entry` struct):

```go
type Entry struct {
	TitleID        int64      `json:"title_id"`
	Kind           string     `json:"kind"`
```

with:

```go
type Entry struct {
	TitleID        int64      `json:"title_id"`
	TMDBID         int64      `json:"tmdb_id"`
	Kind           string     `json:"kind"`
```

Replace:

```go
const entryColumns = `t.id, t.kind, t.name, t.poster_path, t.runtime_minutes, t.airing,
       %[1]s.status, %[1]s.rating, %[1]s.favorite, %[1]s.pointer_season, %[1]s.pointer_episode, %[1]s.added_at, %[1]s.watched_at`
```

with:

```go
const entryColumns = `t.id, t.tmdb_id, t.kind, t.name, t.poster_path, t.runtime_minutes, t.airing,
       %[1]s.status, %[1]s.rating, %[1]s.favorite, %[1]s.pointer_season, %[1]s.pointer_episode, %[1]s.added_at, %[1]s.watched_at`
```

Replace (in `scanEntry`):

```go
	err := row.Scan(&e.TitleID, &e.Kind, &e.Name, &e.PosterPath, &e.RuntimeMinutes, &e.Airing,
		&e.Status, &e.Rating, &e.Favorite, &e.Pointer.Season, &e.Pointer.Episode, &e.AddedAt, &e.WatchedAt)
```

with:

```go
	err := row.Scan(&e.TitleID, &e.TMDBID, &e.Kind, &e.Name, &e.PosterPath, &e.RuntimeMinutes, &e.Airing,
		&e.Status, &e.Rating, &e.Favorite, &e.Pointer.Season, &e.Pointer.Episode, &e.AddedAt, &e.WatchedAt)
```

In `api/internal/httpserver/entries_test.go`, mirror the field in the fake. Replace (in `fakeEntries.UpsertEntry`):

```go
		e = &store.Entry{TitleID: titleID, Kind: "series", Name: f.titles[titleID], Status: "none",
			Pointer: store.Pointer{Season: 1, Episode: 1}, AddedAt: time.Now()}
```

with:

```go
		// Fake tmdb ids are derived deterministically so handler tests can
		// assert the field flows through (real ids come from the join).
		e = &store.Entry{TitleID: titleID, TMDBID: titleID + 100000, Kind: "series", Name: f.titles[titleID], Status: "none",
			Pointer: store.Pointer{Season: 1, Episode: 1}, AddedAt: time.Now()}
```

In `web/src/lib/types.ts`, replace:

```ts
export type Entry = {
  title_id: number;
  kind: "movie" | "series";
```

with:

```ts
export type Entry = {
  title_id: number;
  tmdb_id: number;
  kind: "movie" | "series";
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd api && gofmt -l . && go vet ./... && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./... && go test ./... && cd ../web && pnpm build`
Expected: gofmt silent; all Go packages PASS in both runs (skips without the env var); web build green (additive TS field).

- [ ] **Step 5: Commit**

```bash
git add api/internal/store/entries.go api/internal/store/entries_test.go api/internal/httpserver/entries_test.go web/src/lib/types.ts
git commit -m "feat(api): expose tmdb_id in entry payloads for shelf links"
```

---

### Task 2: TitleCard narrowing + badge + #16 polish ride-alongs

**Files:**
- Modify: `web/src/components/TitleCard.tsx` (full replacement)
- Modify: `web/src/app/search/SearchBody.tsx`
- Modify: `web/src/app/title/[kind]/[tmdbId]/TitleBody.tsx`

**Interfaces:**
- Consumes: existing `TitleCard`/`SearchBody`/`TitleBody` from #16.
- Produces: `TitleCard({ title: TitleCardData, badge?: string })` with exported `TitleCardData{tmdb_id: number; kind: "movie" | "series"; name: string; poster_path: string; year: string}` — Task 3 builds these from entries.

- [ ] **Step 1: Replace TitleCard**

Replace the entire contents of `web/src/components/TitleCard.tsx` with:

```tsx
/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB w342 posters are pre-sized for the grid; next/image would add
   remotePatterns config and an optimization hop for CDN-optimized files
   (spec 2026-07-15-web-search-title-design.md). */
import Link from "next/link";

import { posterUrl } from "@/lib/tmdb";

// The subset of a title this card renders. SearchResult satisfies it
// structurally; shelf entries map into it with year "" (which hides the
// year segment).
export type TitleCardData = {
  tmdb_id: number;
  kind: "movie" | "series";
  name: string;
  poster_path: string;
  year: string;
};

// Poster card linking to the title page. badge renders as a small pill
// over the poster (rotation's next-episode, ratings' value).
export function TitleCard({ title, badge }: { title: TitleCardData; badge?: string }) {
  const poster = posterUrl(title.poster_path, "w342");
  return (
    <Link
      href={`/title/${title.kind}/${title.tmdb_id}`}
      className="group relative block overflow-hidden rounded-xl border border-zinc-200 dark:border-zinc-800"
    >
      {badge && (
        <span className="absolute left-2 top-2 z-10 rounded-md bg-zinc-950/80 px-1.5 py-0.5 text-xs text-zinc-50">
          {badge}
        </span>
      )}
      {poster ? (
        <img
          src={poster}
          alt={title.name}
          loading="lazy"
          className="aspect-[2/3] w-full object-cover"
        />
      ) : (
        <div className="flex aspect-[2/3] w-full items-center justify-center bg-zinc-100 text-xs text-zinc-400 dark:bg-zinc-900">
          no poster
        </div>
      )}
      <div className="p-3">
        <p className="truncate text-sm font-medium text-zinc-950 group-hover:underline dark:text-zinc-50">
          {title.name}
        </p>
        <p className="mt-0.5 text-xs text-zinc-500">
          {title.year ? `${title.year} · ${title.kind}` : title.kind}
        </p>
      </div>
    </Link>
  );
}
```

- [ ] **Step 2: Update the search caller and query options**

In `web/src/app/search/SearchBody.tsx`, replace:

```tsx
import { useQuery } from "@tanstack/react-query";
```

with:

```tsx
import { keepPreviousData, useQuery } from "@tanstack/react-query";
```

Replace:

```tsx
  const { data, error, isPending } = useQuery({
    queryKey: ["search", q],
    queryFn: () => api<SearchResponse>(`/v1/search?q=${encodeURIComponent(q)}`),
    enabled: q !== "",
  });
```

with:

```tsx
  const { data, error, isPending } = useQuery({
    queryKey: ["search", q],
    queryFn: () => api<SearchResponse>(`/v1/search?q=${encodeURIComponent(q)}`),
    enabled: q !== "",
    // Keep the previous grid rendered while the next debounced query
    // loads, and cap retries like the title query (default is 3 with
    // exponential backoff — ~15s of "Searching…" when the API is down).
    placeholderData: keepPreviousData,
    retry: 2,
  });
```

Replace:

```tsx
          {data.results.map((r) => (
            <TitleCard key={`${r.kind}-${r.tmdb_id}`} result={r} />
          ))}
```

with:

```tsx
          {data.results.map((r) => (
            <TitleCard key={`${r.kind}-${r.tmdb_id}`} title={r} />
          ))}
```

- [ ] **Step 3: Hide degenerate metadata on the title page**

In `web/src/app/title/[kind]/[tmdbId]/TitleBody.tsx`, replace:

```tsx
        <p className="mt-1 text-sm text-zinc-500">
          {title.kind === "movie"
            ? `Movie · ${title.runtime_minutes} min`
            : `Series · ${seasons.length} season${seasons.length === 1 ? "" : "s"}${
                title.airing ? " · airing" : ""
              }`}
        </p>
```

with:

```tsx
        <p className="mt-1 text-sm text-zinc-500">
          {title.kind === "movie"
            ? title.runtime_minutes > 0
              ? `Movie · ${title.runtime_minutes} min`
              : "Movie"
            : `Series${
                seasons.length > 0
                  ? ` · ${seasons.length} season${seasons.length === 1 ? "" : "s"}`
                  : ""
              }${title.airing ? " · airing" : ""}`}
        </p>
```

- [ ] **Step 4: Verify**

Run: `cd web && pnpm lint && pnpm build`
Expected: clean; same route table as before.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/TitleCard.tsx web/src/app/search/SearchBody.tsx 'web/src/app/title/[kind]/[tmdbId]/TitleBody.tsx'
git commit -m "feat(web): TitleCard data subset + badge; search keepPreviousData; tidy title meta"
```

---

### Task 3: `/profile` shelves

**Files:**
- Modify: `web/src/lib/types.ts` (append)
- Create: `web/src/app/profile/ProfileBody.tsx`
- Create: `web/src/app/profile/page.tsx`
- Modify: `web/src/components/Nav.tsx`

**Interfaces:**
- Consumes: `Entry` (with `tmdb_id`, Task 1), `TitleCard`/`TitleCardData` (Task 2), `api<T>`.
- Produces: route `/profile`; types `ShelfName`, `ShelfResponse`.

- [ ] **Step 1: Append shelf types**

Append to `web/src/lib/types.ts`:

```ts

// --- Profile shelves (#17). Mirrors /v1/me/shelves/{shelf}.

export type ShelfName = "watchlist" | "rotation" | "watched" | "favorites" | "ratings";

export type ShelfResponse = {
  entries: Entry[];
};
```

- [ ] **Step 2: Create ProfileBody**

Create `web/src/app/profile/ProfileBody.tsx`:

```tsx
"use client";

import { useState } from "react";
import Link from "next/link";
import { useQuery } from "@tanstack/react-query";

import { TitleCard, type TitleCardData } from "@/components/TitleCard";
import { api } from "@/lib/api";
import type { Entry, ShelfName, ShelfResponse } from "@/lib/types";

const TABS: { shelf: ShelfName; label: string }[] = [
  { shelf: "watchlist", label: "Watchlist" },
  { shelf: "rotation", label: "In Rotation" },
  { shelf: "watched", label: "Watched" },
  { shelf: "favorites", label: "Favorites" },
  { shelf: "ratings", label: "Ratings" },
];

const ROTATION_CAP = 8;

const EMPTY_COPY: Record<Exclude<ShelfName, "watchlist">, string> = {
  rotation: "Promote watchlist titles to build your weekly lineup.",
  watched: "Nothing marked watched yet.",
  favorites: "No favorites yet — tap the heart on a title.",
  ratings: "No ratings yet.",
};

function cardData(e: Entry): TitleCardData {
  return {
    tmdb_id: e.tmdb_id,
    kind: e.kind,
    name: e.name,
    poster_path: e.poster_path,
    year: "",
  };
}

// Tab-specific poster badges: rotation shows the next episode for series
// (the pointer the guide advances), ratings shows the value.
function badgeFor(shelf: ShelfName, e: Entry): string | undefined {
  if (shelf === "rotation" && e.kind === "series") {
    return `Next: S${e.pointer.season}E${e.pointer.episode}`;
  }
  if (shelf === "ratings" && e.rating != null) {
    return `${e.rating.toFixed(1)}★`;
  }
  return undefined;
}

function ShelfGrid({ shelf }: { shelf: ShelfName }) {
  const { data, error, isPending } = useQuery({
    queryKey: ["shelf", shelf],
    queryFn: () => api<ShelfResponse>(`/v1/me/shelves/${shelf}`),
  });

  if (isPending) {
    return <p className="mt-8 text-sm text-zinc-500">Loading…</p>;
  }
  if (error || !data) {
    return <p className="mt-8 text-sm text-red-600">Couldn't load this shelf.</p>;
  }

  return (
    <>
      {shelf === "rotation" && (
        <p className="mt-4 text-sm text-zinc-500">
          {data.entries.length} of {ROTATION_CAP} rotation slots used
        </p>
      )}
      {data.entries.length === 0 ? (
        <p className="mt-8 text-sm text-zinc-500">
          {shelf === "watchlist" ? (
            <>
              Nothing on your watchlist yet —{" "}
              <Link href="/search" className="underline underline-offset-4">
                find something in Search
              </Link>
              .
            </>
          ) : (
            EMPTY_COPY[shelf]
          )}
        </p>
      ) : (
        <div className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {data.entries.map((e) => (
            <TitleCard key={e.title_id} title={cardData(e)} badge={badgeFor(shelf, e)} />
          ))}
        </div>
      )}
    </>
  );
}

export function ProfileBody() {
  const [active, setActive] = useState<ShelfName>("watchlist");
  return (
    <main className="mx-auto max-w-5xl p-6">
      <div className="flex flex-wrap items-center gap-2">
        {TABS.map((t) => (
          <button
            key={t.shelf}
            type="button"
            aria-pressed={active === t.shelf}
            onClick={() => setActive(t.shelf)}
            className={`rounded-lg border px-3 py-1.5 text-sm ${
              active === t.shelf
                ? "border-zinc-950 bg-zinc-950 text-zinc-50 dark:border-zinc-50 dark:bg-zinc-50 dark:text-zinc-950"
                : "border-zinc-300 text-zinc-950 dark:border-zinc-700 dark:text-zinc-50"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>
      <ShelfGrid shelf={active} />
    </main>
  );
}
```

- [ ] **Step 3: Create the page wrapper**

Create `web/src/app/profile/page.tsx`:

```tsx
import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { ProfileBody } from "./ProfileBody";

export default function ProfilePage() {
  return (
    <AuthGate>
      <Nav />
      <ProfileBody />
    </AuthGate>
  );
}
```

- [ ] **Step 4: Add the Profile nav link**

In `web/src/components/Nav.tsx`, replace:

```tsx
        <Link
          href="/search"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Search
        </Link>
```

with:

```tsx
        <Link
          href="/search"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Search
        </Link>
        <Link
          href="/profile"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Profile
        </Link>
```

- [ ] **Step 5: Verify**

Run: `cd web && pnpm lint && pnpm build`
Expected: clean; `/profile` appears as a static route.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/types.ts web/src/app/profile/ web/src/components/Nav.tsx
git commit -m "feat(web): profile shelves with rotation meter and badges"
```

---

### Task 4: `/settings`

**Files:**
- Create: `web/src/app/settings/SettingsBody.tsx`
- Create: `web/src/app/settings/page.tsx`
- Modify: `web/src/components/Nav.tsx`

**Interfaces:**
- Consumes: `User`/`SchedulePrefs`/`DayWindow` types, `useToast`, `api<T>`, `["me"]` key.
- Produces: route `/settings`.

- [ ] **Step 1: Create SettingsBody**

Create `web/src/app/settings/SettingsBody.tsx`:

```tsx
"use client";

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { api } from "@/lib/api";
import type { DayWindow, SchedulePrefs, User } from "@/lib/types";

const REGIONS = [
  "US", "GB", "CA", "AU", "DE", "FR", "ES", "IT", "NL", "SE", "BR", "MX", "JP", "KR", "IN",
];

const DAYS: { key: string; label: string }[] = [
  { key: "mon", label: "Mon" },
  { key: "tue", label: "Tue" },
  { key: "wed", label: "Wed" },
  { key: "thu", label: "Thu" },
  { key: "fri", label: "Fri" },
  { key: "sat", label: "Sat" },
  { key: "sun", label: "Sun" },
];

const DEFAULT_WINDOW: DayWindow = { enabled: true, start: "19:00", end: "23:00" };

type FormState = { region: string; prefs: SchedulePrefs };

function SettingsForm({ user }: { user: User }) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  // Seed every day defensively: rows the stored document lacks (legacy
  // pre-default shapes) get the canonical default window.
  const [form, setForm] = useState<FormState>(() => ({
    region: user.region,
    prefs: {
      windows: Object.fromEntries(
        DAYS.map((d) => [d.key, user.schedule_prefs?.windows?.[d.key] ?? { ...DEFAULT_WINDOW }]),
      ),
    },
  }));

  const regions = REGIONS.includes(form.region) ? REGIONS : [form.region, ...REGIONS];

  const setWindow = (day: string, patch: Partial<DayWindow>) =>
    setForm((f) => ({
      ...f,
      prefs: {
        windows: { ...f.prefs.windows, [day]: { ...f.prefs.windows[day], ...patch } },
      },
    }));

  // Server rule (prefs.Validate): start < end on EVERY row, enabled or
  // not. Zero-padded HH:MM compares correctly as strings.
  const invalidDays = DAYS.filter((d) => {
    const w = form.prefs.windows[d.key];
    return w.start >= w.end;
  }).map((d) => d.key);

  const mutation = useMutation({
    mutationFn: () =>
      api<User>("/v1/me", {
        method: "PATCH",
        body: JSON.stringify({ region: form.region, schedule_prefs: form.prefs }),
      }),
    onSuccess: () => {
      show("Settings saved");
      queryClient.invalidateQueries({ queryKey: ["me"] });
    },
    onError: () => show("Couldn't save — try again."),
  });

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        mutation.mutate();
      }}
      className="mt-6 flex max-w-xl flex-col gap-6"
    >
      <label className="flex items-center gap-3 text-sm text-zinc-950 dark:text-zinc-50">
        Region
        <select
          value={form.region}
          onChange={(e) => setForm((f) => ({ ...f, region: e.target.value }))}
          className="rounded-lg border border-zinc-300 bg-transparent px-2 py-1 dark:border-zinc-700"
        >
          {regions.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </label>

      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium text-zinc-950 dark:text-zinc-50">
          Viewing windows
        </legend>
        {DAYS.map((d) => {
          const w = form.prefs.windows[d.key];
          const invalid = invalidDays.includes(d.key);
          return (
            <div key={d.key} className="flex items-center gap-3 text-sm">
              <label className="flex w-24 items-center gap-2 text-zinc-950 dark:text-zinc-50">
                <input
                  type="checkbox"
                  checked={w.enabled}
                  onChange={(e) => setWindow(d.key, { enabled: e.target.checked })}
                />
                {d.label}
              </label>
              <input
                type="time"
                value={w.start}
                disabled={!w.enabled}
                aria-label={`${d.label} start`}
                onChange={(e) => setWindow(d.key, { start: e.target.value })}
                className="rounded-lg border border-zinc-300 bg-transparent px-2 py-1 disabled:opacity-50 dark:border-zinc-700"
              />
              <span className="text-zinc-500">to</span>
              <input
                type="time"
                value={w.end}
                disabled={!w.enabled}
                aria-label={`${d.label} end`}
                onChange={(e) => setWindow(d.key, { end: e.target.value })}
                className="rounded-lg border border-zinc-300 bg-transparent px-2 py-1 disabled:opacity-50 dark:border-zinc-700"
              />
              {invalid && <span className="text-xs text-red-600">start must be before end</span>}
            </div>
          );
        })}
      </fieldset>

      <button
        type="submit"
        disabled={mutation.isPending || invalidDays.length > 0}
        className="w-fit rounded-lg bg-zinc-950 px-4 py-2 text-sm text-zinc-50 disabled:opacity-50 dark:bg-zinc-50 dark:text-zinc-950"
      >
        Save settings
      </button>
    </form>
  );
}

export function SettingsBody() {
  const { data, error, isPending } = useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/v1/me"),
  });

  if (isPending) {
    return <p className="p-8 text-sm text-zinc-500">Loading…</p>;
  }
  if (error || !data) {
    return <p className="p-8 text-sm text-red-600">Could not load your profile.</p>;
  }

  return (
    <main className="mx-auto max-w-5xl p-6">
      <h1 className="text-xl font-semibold text-zinc-950 dark:text-zinc-50">Settings</h1>
      <SettingsForm user={data} />
    </main>
  );
}
```

- [ ] **Step 2: Create the page wrapper**

Create `web/src/app/settings/page.tsx`:

```tsx
import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { SettingsBody } from "./SettingsBody";

export default function SettingsPage() {
  return (
    <AuthGate>
      <Nav />
      <SettingsBody />
    </AuthGate>
  );
}
```

- [ ] **Step 3: Add the Settings nav link**

In `web/src/components/Nav.tsx`, replace:

```tsx
        <Link
          href="/profile"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Profile
        </Link>
```

with:

```tsx
        <Link
          href="/profile"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Profile
        </Link>
        <Link
          href="/settings"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Settings
        </Link>
```

- [ ] **Step 4: Verify**

Run: `cd web && pnpm lint && pnpm build`
Expected: clean; `/settings` appears as a static route.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/settings/ web/src/components/Nav.tsx
git commit -m "feat(web): settings page with region and viewing windows"
```

---

### Task 5: Sweep + PR

**Files:** none new.

- [ ] **Step 1: Full verification**

Run: `cd web && pnpm lint && pnpm build && cd ../api && gofmt -l . && go vet ./... && TEST_DATABASE_URL='postgres://lineup:lineup@localhost:5433/lineup?sslmode=disable' go test ./... && go test ./... && cd ..`
Expected: everything green in both Go runs.

- [ ] **Step 2: Smoke the running dev server**

Run: `curl -s -o /dev/null -w 'profile: %{http_code}\n' http://localhost:3001/profile && curl -s -o /dev/null -w 'settings: %{http_code}\n' http://localhost:3001/settings`
Expected: `200` twice.

- [ ] **Step 3: Push and open the PR** (writing-github-content style)

```bash
git push -u origin feat/17-web-profile-settings
gh pr create --title "feat(web): profile shelves and settings" --body "..."  # body: closes #17, summary, verification, session link
```

The user's manual acceptance (shelves reflect entry actions; prefs persist across reload; invalid start/end blocks Save) happens on the running stack before squash-merge.

---

## Execution notes

- Task order strict: 1 → 2 → 3 → 4 → 5 (each consumes the previous task's exports).
- Task 1 needs the `lineup-pg` container; if docker is unavailable, STOP and report BLOCKED.
- The final whole-branch review happens before Task 5 Step 3 (controller dispatches it per subagent-driven-development).
