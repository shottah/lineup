# Web Search + Title Pages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `/search` (300ms-debounced TMDB search grid) and `/title/[kind]/[tmdbId]` (detail page with providers row and shelf actions), per issue #16 and the approved spec `docs/superpowers/specs/2026-07-15-web-search-title-design.md`.

**Architecture:** TanStack Query client components behind thin async server wrappers (the #15 pattern). Pages compose the existing `AuthGate`/`Nav`; mutations PATCH the entry endpoint and invalidate `["title", …]` + `["shelf"]` keys. Plain `<img>` for TMDB posters; homegrown toast folded into `Providers.tsx`.

**Tech Stack:** Next 16.2.10 (App Router; `params` is a **Promise** — see `web/AGENTS.md` + `node_modules/next/dist/docs/`), React 19, @tanstack/react-query 5, Tailwind v4, TypeScript. pnpm (NOT npm/bun).

## Global Constraints

- Branch `feat/16-web-search-title`. All work under `web/`. No new dependencies — `package.json` must not change.
- Verification per task: `cd web && pnpm lint && pnpm build` — both must pass with zero errors; treat new warnings as failures.
- API JSON is snake_case; the types in Task 1 mirror it verbatim — never rename fields client-side.
- Entry PATCH uses the INTERNAL `title.id` from the payload, never the TMDB id.
- Query keys: title page `["title", kind, tmdbId]` (strings from route params); shelf prefix `["shelf"]`; search `["search", q]`.
- 409 toast copy EXACTLY: `Rotation is full (8); finish something first.`
- Plain `<img>` is deliberate (spec decision): each file using it starts with `/* eslint-disable @next/next/no-img-element */` plus the rationale comment shown in the task code.
- Existing components (`AuthGate`, `Nav`, `Providers`) keep their exported names and behavior; `Providers.tsx` gains the toast but its existing auth/query code is byte-preserved except where the task shows an edit.

---

### Task 1: Foundations — types, poster URLs, toast, nav link

**Files:**
- Modify: `web/src/lib/types.ts` (append)
- Create: `web/src/lib/tmdb.ts`
- Modify: `web/src/components/Providers.tsx`
- Modify: `web/src/components/Nav.tsx`

**Interfaces:**
- Consumes: existing `Providers.tsx` auth context, `Nav.tsx` markup.
- Produces (Tasks 2–3 rely on): all types below; `posterUrl(path: string, size: "w92" | "w342"): string | null`; `useToast(): { show(message: string): void }` exported from `@/components/Providers`.

- [ ] **Step 1: Append the API mirror types**

Append to `web/src/lib/types.ts`:

```ts

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
```

- [ ] **Step 2: Create the poster URL helper**

Create `web/src/lib/tmdb.ts`:

```ts
// TMDB image URL assembly. poster_path/logo_path come from the API
// verbatim (e.g. "/abc.jpg", or "" when TMDB has none).

const IMAGE_BASE = "https://image.tmdb.org/t/p";

export type PosterSize = "w92" | "w342";

export function posterUrl(path: string, size: PosterSize): string | null {
  if (!path) {
    return null;
  }
  return `${IMAGE_BASE}/${size}${path}`;
}
```

- [ ] **Step 3: Fold the toast into Providers.tsx**

Replace the entire contents of `web/src/components/Providers.tsx` with:

```tsx
"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { onAuthStateChanged, type User as FirebaseUser } from "firebase/auth";

import { auth } from "@/lib/firebase";

type AuthState = { user: FirebaseUser | null; loading: boolean };

const AuthContext = createContext<AuthState>({ user: null, loading: true });

export function useAuth(): AuthState {
  return useContext(AuthContext);
}

// --- Toast (#16): one visible message at a time, last wins, 4s
// auto-dismiss. Kept dependency-free: a single consumer today (the
// rotation_full 409) doesn't justify a package.

type ToastState = { show: (message: string) => void };

const ToastContext = createContext<ToastState>({ show: () => {} });

export function useToast(): ToastState {
  return useContext(ToastContext);
}

const TOAST_MS = 4000;

function ToastProvider({ children }: { children: React.ReactNode }) {
  const [message, setMessage] = useState<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const show = useCallback((next: string) => {
    setMessage(next);
    if (timer.current) {
      clearTimeout(timer.current);
    }
    timer.current = setTimeout(() => setMessage(null), TOAST_MS);
  }, []);

  useEffect(
    () => () => {
      if (timer.current) {
        clearTimeout(timer.current);
      }
    },
    [],
  );

  return (
    <ToastContext.Provider value={{ show }}>
      {children}
      {message && (
        <div
          role="status"
          className="fixed bottom-6 left-1/2 z-50 -translate-x-1/2 rounded-lg bg-zinc-950 px-4 py-2 text-sm text-zinc-50 shadow-lg dark:bg-zinc-50 dark:text-zinc-950"
        >
          {message}
        </div>
      )}
    </ToastContext.Provider>
  );
}

export function Providers({ children }: { children: React.ReactNode }) {
  // One QueryClient per component tree (not module scope): avoids sharing
  // cache state across SSR requests.
  const [queryClient] = useState(() => new QueryClient());
  const [authState, setAuthState] = useState<AuthState>({ user: null, loading: true });

  useEffect(
    () => onAuthStateChanged(auth, (user) => setAuthState({ user, loading: false })),
    [],
  );

  return (
    <QueryClientProvider client={queryClient}>
      <AuthContext.Provider value={authState}>
        <ToastProvider>{children}</ToastProvider>
      </AuthContext.Provider>
    </QueryClientProvider>
  );
}
```

- [ ] **Step 4: Add the Search link to Nav**

In `web/src/components/Nav.tsx`, replace:

```tsx
      <Link href="/guide" className="font-semibold text-zinc-950 dark:text-zinc-50">
        Lineup
      </Link>
```

with:

```tsx
      <div className="flex items-center gap-4">
        <Link href="/guide" className="font-semibold text-zinc-950 dark:text-zinc-50">
          Lineup
        </Link>
        <Link
          href="/search"
          className="text-sm text-zinc-500 hover:text-zinc-950 dark:hover:text-zinc-50"
        >
          Search
        </Link>
      </div>
```

- [ ] **Step 5: Verify**

Run: `cd web && pnpm lint && pnpm build`
Expected: lint clean; build succeeds (routes unchanged: `/`, `/guide`, `/login`, `/_not-found`).

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/types.ts web/src/lib/tmdb.ts web/src/components/Providers.tsx web/src/components/Nav.tsx
git commit -m "feat(web): api mirror types, poster urls, toast, search nav link"
```

---

### Task 2: Search page

**Files:**
- Create: `web/src/components/TitleCard.tsx`
- Create: `web/src/app/search/SearchBody.tsx`
- Create: `web/src/app/search/page.tsx`

**Interfaces:**
- Consumes: `SearchResult`/`SearchResponse` types, `posterUrl` (Task 1), `api<T>` from `@/lib/api`, `AuthGate`/`Nav`.
- Produces: `TitleCard({ result: SearchResult })` (reused by #17's shelves later); route `/search`.

- [ ] **Step 1: Create TitleCard**

Create `web/src/components/TitleCard.tsx`:

```tsx
/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB w342 posters are pre-sized for the grid; next/image would add
   remotePatterns config and an optimization hop for CDN-optimized files
   (spec 2026-07-15-web-search-title-design.md). */
import Link from "next/link";

import { posterUrl } from "@/lib/tmdb";
import type { SearchResult } from "@/lib/types";

// Poster card linking to the title page.
export function TitleCard({ result }: { result: SearchResult }) {
  const poster = posterUrl(result.poster_path, "w342");
  return (
    <Link
      href={`/title/${result.kind}/${result.tmdb_id}`}
      className="group block overflow-hidden rounded-xl border border-zinc-200 dark:border-zinc-800"
    >
      {poster ? (
        <img
          src={poster}
          alt={result.name}
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
          {result.name}
        </p>
        <p className="mt-0.5 text-xs text-zinc-500">
          {result.year || "—"} · {result.kind}
        </p>
      </div>
    </Link>
  );
}
```

- [ ] **Step 2: Create the search body**

Create `web/src/app/search/SearchBody.tsx`:

```tsx
"use client";

import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { TitleCard } from "@/components/TitleCard";
import { api } from "@/lib/api";
import type { SearchResponse } from "@/lib/types";

const DEBOUNCE_MS = 300;

export function SearchBody() {
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");

  // Debounce: q trails input by 300ms, so the query (keyed on q) only
  // fires when typing pauses.
  useEffect(() => {
    const t = setTimeout(() => setQ(input.trim()), DEBOUNCE_MS);
    return () => clearTimeout(t);
  }, [input]);

  const { data, error, isPending } = useQuery({
    queryKey: ["search", q],
    queryFn: () => api<SearchResponse>(`/v1/search?q=${encodeURIComponent(q)}`),
    enabled: q !== "",
  });

  return (
    <main className="mx-auto max-w-5xl p-6">
      <input
        autoFocus
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="Search movies and series…"
        className="w-full rounded-lg border border-zinc-300 bg-transparent px-4 py-2 text-zinc-950 placeholder:text-zinc-400 focus:outline-none focus:ring-2 focus:ring-zinc-400 dark:border-zinc-700 dark:text-zinc-50"
      />
      {q === "" ? (
        <p className="mt-8 text-sm text-zinc-500">Search for something to watch.</p>
      ) : isPending ? (
        <p className="mt-8 text-sm text-zinc-500">Searching…</p>
      ) : error ? (
        <p className="mt-8 text-sm text-red-600">Search is unavailable right now.</p>
      ) : !data || data.results.length === 0 ? (
        <p className="mt-8 text-sm text-zinc-500">Nothing found for “{q}”.</p>
      ) : (
        <div className="mt-6 grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5">
          {data.results.map((r) => (
            <TitleCard key={`${r.kind}-${r.tmdb_id}`} result={r} />
          ))}
        </div>
      )}
    </main>
  );
}
```

- [ ] **Step 3: Create the page wrapper**

Create `web/src/app/search/page.tsx`:

```tsx
import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { SearchBody } from "./SearchBody";

export default function SearchPage() {
  return (
    <AuthGate>
      <Nav />
      <SearchBody />
    </AuthGate>
  );
}
```

- [ ] **Step 4: Verify**

Run: `cd web && pnpm lint && pnpm build`
Expected: lint clean (the img rule is file-disabled with rationale); build lists the new `/search` route.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/TitleCard.tsx web/src/app/search/
git commit -m "feat(web): search page with debounced title grid"
```

---

### Task 3: Title page with entry actions

**Files:**
- Create: `web/src/components/StarRating.tsx`
- Create: `web/src/components/EntryActions.tsx`
- Create: `web/src/app/title/[kind]/[tmdbId]/TitleBody.tsx`
- Create: `web/src/app/title/[kind]/[tmdbId]/page.tsx`

**Interfaces:**
- Consumes: Task 1's types + `posterUrl` + `useToast`; `api`/`ApiError` from `@/lib/api`; `AuthGate`/`Nav`.
- Produces: route `/title/[kind]/[tmdbId]`; `EntryActions({ title, entry, kind, tmdbId })`; `StarRating({ value, onRate, disabled? })` (reused by #17).

- [ ] **Step 1: Create StarRating**

Create `web/src/components/StarRating.tsx`:

```tsx
"use client";

// Five stars with half-step hit zones (0.5–5.0): each star overlays an
// amber fill (0/50/100% width) on a gray base and carries two invisible
// half-width buttons. Clicking the current value clears the rating
// (null). Purely presentational; the parent owns persistence.
export function StarRating({
  value,
  onRate,
  disabled = false,
}: {
  value: number | null;
  onRate: (v: number | null) => void;
  disabled?: boolean;
}) {
  const current = value ?? 0;
  return (
    <div className="flex items-center gap-0.5">
      {[1, 2, 3, 4, 5].map((star) => (
        <span key={star} className="relative inline-block h-6 w-6 select-none">
          <span
            aria-hidden
            className="absolute inset-0 text-center text-xl leading-6 text-zinc-300 dark:text-zinc-700"
          >
            ★
          </span>
          <span
            aria-hidden
            className="absolute inset-0 overflow-hidden text-center text-xl leading-6 text-amber-500"
            style={{ width: current >= star ? "100%" : current >= star - 0.5 ? "50%" : "0%" }}
          >
            ★
          </span>
          <button
            type="button"
            disabled={disabled}
            aria-label={`Rate ${star - 0.5} stars`}
            onClick={() => onRate(current === star - 0.5 ? null : star - 0.5)}
            className="absolute inset-y-0 left-0 w-1/2 disabled:cursor-not-allowed"
          />
          <button
            type="button"
            disabled={disabled}
            aria-label={`Rate ${star} stars`}
            onClick={() => onRate(current === star ? null : star)}
            className="absolute inset-y-0 right-0 w-1/2 disabled:cursor-not-allowed"
          />
        </span>
      ))}
      {value != null && <span className="ml-2 text-xs text-zinc-500">{value.toFixed(1)}</span>}
    </div>
  );
}
```

- [ ] **Step 2: Create EntryActions**

Create `web/src/components/EntryActions.tsx`:

```tsx
"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { useToast } from "@/components/Providers";
import { StarRating } from "@/components/StarRating";
import { api, ApiError } from "@/lib/api";
import type { Entry, EntryStatus, Title } from "@/lib/types";

type EntryPatch = {
  status?: EntryStatus;
  rating?: number | null;
  favorite?: boolean;
};

const STATUSES: { value: Exclude<EntryStatus, "none">; label: string }[] = [
  { value: "watchlist", label: "Watchlist" },
  { value: "rotation", label: "Rotation" },
  { value: "watched", label: "Watched" },
];

// Shelf actions for one title. A null entry means "no relationship yet":
// status none, unrated, not favorite. Status buttons are a radio with
// toggle-off (clicking the active one sets none). PATCHes the INTERNAL
// title id — never the TMDB id.
export function EntryActions({
  title,
  entry,
  kind,
  tmdbId,
}: {
  title: Title;
  entry: Entry | null;
  kind: string;
  tmdbId: string;
}) {
  const queryClient = useQueryClient();
  const { show } = useToast();

  const status: EntryStatus = entry?.status ?? "none";
  const rating = entry?.rating ?? null;
  const favorite = entry?.favorite ?? false;

  const mutation = useMutation({
    mutationFn: (patch: EntryPatch) =>
      api<Entry>(`/v1/titles/${title.id}/entry`, {
        method: "PATCH",
        body: JSON.stringify(patch),
      }),
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409 && err.code === "rotation_full") {
        show("Rotation is full (8); finish something first.");
      } else {
        show("Couldn't save — try again.");
      }
    },
    onSettled: () => {
      // Refetch whether it worked or not: on error the server state is
      // unknown, and the title payload is the source of truth.
      queryClient.invalidateQueries({ queryKey: ["title", kind, tmdbId] });
      queryClient.invalidateQueries({ queryKey: ["shelf"] });
    },
  });

  const busy = mutation.isPending;

  return (
    <div className="mt-6 flex flex-col gap-4">
      <div className="flex items-center gap-2">
        {STATUSES.map((s) => {
          const active = status === s.value;
          return (
            <button
              key={s.value}
              type="button"
              disabled={busy}
              onClick={() => mutation.mutate({ status: active ? "none" : s.value })}
              className={`rounded-lg border px-3 py-1.5 text-sm disabled:opacity-50 ${
                active
                  ? "border-zinc-950 bg-zinc-950 text-zinc-50 dark:border-zinc-50 dark:bg-zinc-50 dark:text-zinc-950"
                  : "border-zinc-300 text-zinc-950 dark:border-zinc-700 dark:text-zinc-50"
              }`}
            >
              {s.label}
            </button>
          );
        })}
        <button
          type="button"
          disabled={busy}
          aria-label={favorite ? "Remove from favorites" : "Add to favorites"}
          onClick={() => mutation.mutate({ favorite: !favorite })}
          className={`ml-2 text-xl disabled:opacity-50 ${
            favorite ? "text-red-500" : "text-zinc-300 dark:text-zinc-700"
          }`}
        >
          ♥
        </button>
      </div>
      <StarRating value={rating} onRate={(v) => mutation.mutate({ rating: v })} disabled={busy} />
    </div>
  );
}
```

- [ ] **Step 3: Create the title body**

Create `web/src/app/title/[kind]/[tmdbId]/TitleBody.tsx`:

```tsx
/* eslint-disable @next/next/no-img-element -- plain <img> is deliberate:
   TMDB files are pre-sized; next/image would add remotePatterns config
   and an optimization hop (spec 2026-07-15-web-search-title-design.md). */
"use client";

import { useQuery } from "@tanstack/react-query";

import { EntryActions } from "@/components/EntryActions";
import { api, ApiError } from "@/lib/api";
import { posterUrl } from "@/lib/tmdb";
import type { TitleFull } from "@/lib/types";

export function TitleBody({ kind, tmdbId }: { kind: string; tmdbId: string }) {
  const { data, error, isPending } = useQuery({
    queryKey: ["title", kind, tmdbId],
    queryFn: () => api<TitleFull>(`/v1/titles/${kind}/${tmdbId}`),
    // 404 is a definitive answer; retrying it just delays the empty state.
    retry: (failureCount, err) =>
      !(err instanceof ApiError && err.status === 404) && failureCount < 2,
  });

  if (isPending) {
    return <p className="p-8 text-sm text-zinc-500">Loading…</p>;
  }
  if (error || !data) {
    const notFound = error instanceof ApiError && error.status === 404;
    return (
      <p className="p-8 text-sm text-zinc-500">
        {notFound ? "Title not found." : "Can't reach the catalog right now — try again shortly."}
      </p>
    );
  }

  const { title, seasons, providers, entry } = data;
  const poster = posterUrl(title.poster_path, "w342");

  return (
    <main className="mx-auto flex max-w-4xl flex-col gap-8 p-6 sm:flex-row">
      {poster ? (
        <img src={poster} alt={title.name} className="h-fit w-56 shrink-0 rounded-xl" />
      ) : (
        <div className="flex aspect-[2/3] w-56 shrink-0 items-center justify-center rounded-xl bg-zinc-100 text-xs text-zinc-400 dark:bg-zinc-900">
          no poster
        </div>
      )}
      <div className="min-w-0">
        <h1 className="text-2xl font-semibold text-zinc-950 dark:text-zinc-50">{title.name}</h1>
        <p className="mt-1 text-sm text-zinc-500">
          {title.kind === "movie"
            ? `Movie · ${title.runtime_minutes} min`
            : `Series · ${seasons.length} season${seasons.length === 1 ? "" : "s"}${
                title.airing ? " · airing" : ""
              }`}
        </p>
        {title.overview && (
          <p className="mt-4 text-sm leading-6 text-zinc-700 dark:text-zinc-300">{title.overview}</p>
        )}

        <div className="mt-6">
          <h2 className="text-sm font-medium text-zinc-950 dark:text-zinc-50">Where to watch</h2>
          {providers.length === 0 ? (
            <p className="mt-2 text-sm text-zinc-500">Not streaming in your region.</p>
          ) : (
            <div className="mt-2 flex flex-wrap items-center gap-3">
              {providers.map((p) => {
                const logo = posterUrl(p.logo_path, "w92");
                return (
                  <span
                    key={p.id}
                    className="flex items-center gap-2 rounded-lg border border-zinc-200 px-2 py-1 text-sm text-zinc-950 dark:border-zinc-800 dark:text-zinc-50"
                  >
                    {logo && <img src={logo} alt="" className="h-5 w-5 rounded" />}
                    {p.name}
                  </span>
                );
              })}
            </div>
          )}
        </div>

        <EntryActions title={title} entry={entry} kind={kind} tmdbId={tmdbId} />
      </div>
    </main>
  );
}
```

- [ ] **Step 4: Create the page wrapper (Next 16 Promise params)**

Create `web/src/app/title/[kind]/[tmdbId]/page.tsx`:

```tsx
import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { TitleBody } from "./TitleBody";

// Next 16: params is a Promise in server components.
export default async function TitlePage({
  params,
}: {
  params: Promise<{ kind: string; tmdbId: string }>;
}) {
  const { kind, tmdbId } = await params;
  return (
    <AuthGate>
      <Nav />
      <TitleBody kind={kind} tmdbId={tmdbId} />
    </AuthGate>
  );
}
```

- [ ] **Step 5: Verify**

Run: `cd web && pnpm lint && pnpm build`
Expected: lint clean; build lists `/title/[kind]/[tmdbId]` as a dynamic (ƒ) route and `/search` as static.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/StarRating.tsx web/src/components/EntryActions.tsx web/src/app/title/
git commit -m "feat(web): title page with providers row and entry actions"
```

---

### Task 4: Sweep + PR

**Files:** none new.

- [ ] **Step 1: Full verification**

Run: `cd web && pnpm lint && pnpm build && cd ../api && go test ./... && cd ..`
Expected: everything green (api untouched — this proves it).

- [ ] **Step 2: Smoke the running dev server** (the local stack from #15 testing is up; the dev server hot-reloads this branch's files)

Run: `curl -s -o /dev/null -w '%{http_code}\n' http://localhost:3001/search && curl -s -o /dev/null -w '%{http_code}\n' 'http://localhost:3001/title/movie/603'`
Expected: `200` twice (the SSR shell; interactive behavior is the user's manual loop).

- [ ] **Step 3: Push and open the PR** (writing-github-content style)

```bash
git push -u origin feat/16-web-search-title
gh pr create --title "feat(web): search and title pages" --body "..."  # body: closes #16, summary, verification notes, session link
```

The user's manual acceptance loop (search → watchlist → rotate → rate 4.5 → favorite → 409 toast) happens on the running stack before they squash-merge.

---

## Execution notes

- Task order strict: 1 → 2 → 3 → 4.
- No JS test infra exists in `web/` and none is added (spec decision); the per-task gate is `pnpm lint && pnpm build`.
- The final whole-branch review happens before Task 4 Step 3 (controller dispatches it per subagent-driven-development).
