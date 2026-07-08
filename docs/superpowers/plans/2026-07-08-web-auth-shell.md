# Web Auth Flow, API Client, App Shell (issue #15) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Next.js version warning:** `web/` runs Next.js 16.2.10 + React 19 —
> newer than model training data. Before writing or modifying any page,
> layout, or routing code, read the relevant guide under
> `web/node_modules/next/dist/docs/01-app/` (getting-started covers
> layouts/pages/linking; api-reference covers `next/navigation`). Do not
> assume Next 14/15 conventions hold.

**Goal:** Web sign-in via Firebase Auth (emulator-backed in dev), a typed API client attaching ID tokens, and the app shell (Providers/AuthGate/Nav, /login, / routing, protected /guide placeholder), plus API CORS.

**Architecture:** Client-side auth state via `onAuthStateChanged` in a Providers context; `AuthGate` guards protected pages; `api<T>()` attaches a fresh ID token per call and normalizes errors to `ApiError`. The emulator connection is driven purely by `config.authEmulatorHost` presence (dev-only field).

**Tech Stack:** Next.js 16.2.10 (App Router, TS), React 19, Tailwind 4, pnpm, firebase JS SDK (modular), @tanstack/react-query v5, go-chi/cors (API side).

## Global Constraints

- Stacked branch `feat/15-web-auth-shell` on `feat/6-firebase-apphosting` (gh-stack managed). Commit per task with `git add`/`git commit` as usual; do NOT run `gh stack` commands mid-task — the controller handles stack operations.
- `web/` uses pnpm only. New deps: `pnpm add firebase @tanstack/react-query` (Task 2). API dep: `github.com/go-chi/cors` (Task 1).
- No web test framework exists or is added (issue acceptance = build + manual; consistent with the #8 decision). Web verification per task: `cd web && pnpm run build && pnpm run lint`.
- Go changes follow TDD with chi httptest.
- CORS allowed origins (exact list): `http://localhost:3000`, `http://localhost:3001`, `https://lineup-app-ae6b.web.app`, `https://lineup-app-ae6b.firebaseapp.com`. Methods GET/POST/PATCH/OPTIONS; headers Authorization, Content-Type; no credentials.
- `config.ts`: dev block gains `authEmulatorHost: "http://localhost:9099"`; BOTH blocks gain `appleAuth: false`; production must NOT have `authEmulatorHost`. Explicit `AppConfig` type replaces `as const` (union types would make `config.authEmulatorHost` a type error).
- API JSON error contract (existing): `{"error":"<code>"}`; `api<T>()` surfaces it as `ApiError{status, code}`, with `code: "no_session"` + status 401 thrown locally when no user is signed in (no network call).
- `/v1/me` JSON shape (existing, mirror exactly in types.ts): `{"id":number,"email":string,"display_name":string,"region":string,"schedule_prefs":{"windows":{"mon":{"enabled":bool,"start":"HH:MM","end":"HH:MM"},…}}}`.
- Styling: match the existing skeleton's Tailwind idiom (zinc palette, dark: variants); no new design system.

---

### Task 1: API CORS middleware

**Files:**
- Modify: `api/internal/httpserver/server.go` (add cors.Handler to middleware chain)
- Create: `api/internal/httpserver/cors_test.go`
- Modify: `api/go.mod` (+ github.com/go-chi/cors)

**Interfaces:**
- Produces: cross-origin access to all API routes from the four allowed origins. Web dev on :3001 depends on it (Task 6 live check).

- [ ] **Step 1: Write the failing test**

```go
// api/internal/httpserver/cors_test.go
package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCORSPreflight(t *testing.T) {
	srv := New(Deps{})
	cases := []struct {
		name      string
		origin    string
		wantAllow bool
	}{
		{"localhost 3001 allowed", "http://localhost:3001", true},
		{"localhost 3000 allowed", "http://localhost:3000", true},
		{"hosted web.app allowed", "https://lineup-app-ae6b.web.app", true},
		{"unknown origin rejected", "https://evil.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, "/v1/me", nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", "GET")
			req.Header.Set("Access-Control-Request-Headers", "Authorization")
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			got := rec.Header().Get("Access-Control-Allow-Origin")
			if tc.wantAllow && got != tc.origin {
				t.Fatalf("ACAO = %q, want %q", got, tc.origin)
			}
			if !tc.wantAllow && got != "" {
				t.Fatalf("ACAO = %q for disallowed origin, want empty", got)
			}
		})
	}
}

func TestCORSActualRequestHeader(t *testing.T) {
	srv := New(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "http://localhost:3001")
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3001" {
		t.Fatalf("ACAO on actual request = %q, want origin echoed", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz with Origin = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `cd api && go test ./internal/httpserver/ -run TestCORS`
Expected: FAIL (no ACAO headers — middleware absent)

- [ ] **Step 3: Implement**

Run: `cd api && go get github.com/go-chi/cors@latest`

In `server.go`, add the import `"github.com/go-chi/cors"` and insert BEFORE the existing `r.Use(middleware.RequestID, ...)` line:

```go
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{
			"http://localhost:3000",
			"http://localhost:3001",
			"https://lineup-app-ae6b.web.app",
			"https://lineup-app-ae6b.firebaseapp.com",
		},
		AllowedMethods: []string{"GET", "POST", "PATCH", "OPTIONS"},
		AllowedHeaders: []string{"Authorization", "Content-Type"},
		MaxAge:         300,
	}))
```

- [ ] **Step 4: Verify pass + full suite**

Run: `cd api && go test ./internal/httpserver/ && go vet ./... && go test ./...`
Expected: PASS everywhere (store tests skip without TEST_DATABASE_URL)

- [ ] **Step 5: Commit**

```bash
git add api/
git commit -m "feat(api): CORS middleware for web origins"
```

### Task 2: web deps, config extension, layout metadata

**Files:**
- Modify: `web/package.json` + `web/pnpm-lock.yaml` (pnpm add)
- Modify: `web/src/lib/config.ts`
- Modify: `web/src/app/layout.tsx` (metadata only)

**Interfaces:**
- Produces: `config.authEmulatorHost?: string` (dev-only), `config.appleAuth: boolean`; `firebase` + `@tanstack/react-query` installed. Tasks 3–5 consume.

- [ ] **Step 1: Install deps**

Run: `cd web && pnpm add firebase @tanstack/react-query`
Expected: both land in `dependencies`; lockfile updates.

- [ ] **Step 2: Rewrite `web/src/lib/config.ts`**

Replace the whole file with:

```ts
// Client configuration, switched on NODE_ENV at build time.
// Every value here ships to the browser and is public by design; the
// Firebase apiKey is a client identifier (abuse-limited via API key
// restrictions in GCP), NOT a secret. Real secrets live in Secret Manager
// and never in this file.

type AppConfig = {
  firebase: { apiKey: string; authDomain: string; projectId: string };
  apiUrl: string;
  /** Auth emulator origin. Present only in development; its absence keeps
   *  emulator wiring out of production bundles. */
  authEmulatorHost?: string;
  appleAuth: boolean;
};

const configs: Record<"development" | "production", AppConfig> = {
  development: {
    firebase: {
      apiKey: "FILLED_IN_BY_RUNBOOK_STEP_3",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "demo-lineup",
    },
    apiUrl: "http://localhost:8080",
    authEmulatorHost: "http://localhost:9099",
    appleAuth: false,
  },
  production: {
    firebase: {
      apiKey: "FILLED_IN_BY_RUNBOOK_STEP_3",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "lineup-app-ae6b",
    },
    apiUrl: "https://lineup-api-zzwkjc5sdq-uc.a.run.app",
    appleAuth: false,
  },
};

export const config =
  configs[process.env.NODE_ENV === "production" ? "production" : "development"];
```

Note the deliberate dev change: `projectId: "demo-lineup"` — the emulator
only accepts its own project id, and dev always runs against the emulator.
Production keeps the real project id. The dev `apiKey` placeholder is
irrelevant to the emulator (any string works) and dev `authDomain` is
unused under the emulator; both keep the runbook-fill convention anyway.

- [ ] **Step 3: Update `layout.tsx` metadata**

Change the `metadata` export only:

```ts
export const metadata: Metadata = {
  title: "Lineup",
  description: "Your week of TV, planned like a lineup",
};
```

- [ ] **Step 4: Verify**

Run: `cd web && pnpm run build && pnpm run lint`
Expected: green. (`git grep -n FILLED_IN_BY_RUNBOOK_STEP_3 -- ':/web'` still matches — expected until issue #6's cloud steps fill values; the #6 gate owns that.)

- [ ] **Step 5: Commit**

```bash
git add web/package.json web/pnpm-lock.yaml web/src/lib/config.ts web/src/app/layout.tsx
git commit -m "feat(web): firebase + react-query deps, emulator-aware config, Lineup metadata"
```

### Task 3: lib modules — types, firebase, api

**Files:**
- Create: `web/src/lib/types.ts`
- Create: `web/src/lib/firebase.ts`
- Create: `web/src/lib/api.ts`

**Interfaces:**
- Consumes: `config` from Task 2.
- Produces: `User`, `SchedulePrefs`, `DayWindow` types; `auth`, `signInWithGoogle()`, `signOutUser()` from firebase.ts; `api<T>(path, init?)` + `ApiError` from api.ts. Tasks 4–5 consume.

- [ ] **Step 1: Write `web/src/lib/types.ts`**

```ts
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
```

- [ ] **Step 2: Write `web/src/lib/firebase.ts`**

```ts
// Firebase app/auth singletons. When config.authEmulatorHost is set (dev),
// auth talks to the local emulator; in production the field is absent and
// this connects to live Firebase. The guard on auth.emulatorConfig keeps
// hot-module reloads from calling connectAuthEmulator twice.

import { getApps, initializeApp } from "firebase/app";
import {
  connectAuthEmulator,
  getAuth,
  GoogleAuthProvider,
  signInWithPopup,
  signOut,
} from "firebase/auth";

import { config } from "@/lib/config";

const app = getApps()[0] ?? initializeApp(config.firebase);

export const auth = getAuth(app);

if (config.authEmulatorHost && !auth.emulatorConfig) {
  connectAuthEmulator(auth, config.authEmulatorHost, { disableWarnings: true });
}

export async function signInWithGoogle(): Promise<void> {
  await signInWithPopup(auth, new GoogleAuthProvider());
}

export async function signOutUser(): Promise<void> {
  await signOut(auth);
}
```

- [ ] **Step 3: Write `web/src/lib/api.ts`**

```ts
// Typed fetch wrapper for the Lineup API. Attaches a fresh Firebase ID
// token per call (the SDK caches and refreshes internally, so this is
// cheap) and normalizes the API's {"error":"<code>"} bodies to ApiError.

import { auth } from "@/lib/firebase";
import { config } from "@/lib/config";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string) {
    super(`api ${status}: ${code}`);
    this.status = status;
    this.code = code;
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const user = auth.currentUser;
  if (!user) {
    throw new ApiError(401, "no_session");
  }
  const token = await user.getIdToken();
  const res = await fetch(`${config.apiUrl}${path}`, {
    ...init,
    headers: {
      ...init.headers,
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
  });
  if (!res.ok) {
    let code = "unknown";
    try {
      const body: unknown = await res.json();
      if (body && typeof body === "object" && "error" in body && typeof body.error === "string") {
        code = body.error;
      }
    } catch {
      // non-JSON error body; keep "unknown"
    }
    throw new ApiError(res.status, code);
  }
  return (await res.json()) as T;
}
```

- [ ] **Step 4: Verify**

Run: `cd web && pnpm run build && pnpm run lint`
Expected: green (modules compile; nothing imports them yet — that's fine, unused files are not lint errors here).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/lib/firebase.ts web/src/lib/api.ts
git commit -m "feat(web): firebase singleton, typed api client, shared types"
```

### Task 4: Providers, AuthGate, Nav + layout wiring

**Files:**
- Create: `web/src/components/Providers.tsx`
- Create: `web/src/components/AuthGate.tsx`
- Create: `web/src/components/Nav.tsx`
- Modify: `web/src/app/layout.tsx` (wrap children in Providers)

**Interfaces:**
- Consumes: `auth`, `signOutUser` (Task 3).
- Produces: `Providers`, `useAuth(): {user, loading}`, `AuthGate`, `Nav`. Task 5 consumes.

- [ ] **Step 1: Read the Next 16 docs for client components + navigation** (`web/node_modules/next/dist/docs/01-app/01-getting-started/` — layouts-and-pages, linking-and-navigating) before writing. Confirm `useRouter` still comes from `next/navigation` and `"use client"` conventions are unchanged; adapt the code below if the docs disagree (report any adaptation).

- [ ] **Step 2: Write `web/src/components/Providers.tsx`**

```tsx
"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { onAuthStateChanged, type User as FirebaseUser } from "firebase/auth";

import { auth } from "@/lib/firebase";

type AuthState = { user: FirebaseUser | null; loading: boolean };

const AuthContext = createContext<AuthState>({ user: null, loading: true });

export function useAuth(): AuthState {
  return useContext(AuthContext);
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
      <AuthContext.Provider value={authState}>{children}</AuthContext.Provider>
    </QueryClientProvider>
  );
}
```

- [ ] **Step 3: Write `web/src/components/AuthGate.tsx`**

```tsx
"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";

// Renders children only for signed-in users; bounces others to /login.
// Renders nothing while auth state is still resolving to avoid a flash of
// protected content.
export function AuthGate({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (!loading && !user) {
      router.replace("/login");
    }
  }, [loading, user, router]);

  if (loading || !user) {
    return null;
  }
  return <>{children}</>;
}
```

- [ ] **Step 4: Write `web/src/components/Nav.tsx`**

```tsx
"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";
import { signOutUser } from "@/lib/firebase";

export function Nav() {
  const { user } = useAuth();
  const router = useRouter();

  return (
    <nav className="flex items-center justify-between border-b border-zinc-200 px-6 py-3 dark:border-zinc-800">
      <Link href="/guide" className="font-semibold text-zinc-950 dark:text-zinc-50">
        Lineup
      </Link>
      <div className="flex items-center gap-4 text-sm">
        <span className="text-zinc-500">{user?.email}</span>
        <button
          onClick={async () => {
            await signOutUser();
            router.replace("/login");
          }}
          className="text-zinc-950 underline underline-offset-4 dark:text-zinc-50"
        >
          Sign out
        </button>
      </div>
    </nav>
  );
}
```

- [ ] **Step 5: Wire Providers into `layout.tsx`**

Add `import { Providers } from "@/components/Providers";` and change the body line to:

```tsx
      <body className="min-h-full flex flex-col">
        <Providers>{children}</Providers>
      </body>
```

(Keep everything else in the file untouched.)

- [ ] **Step 6: Verify**

Run: `cd web && pnpm run build && pnpm run lint`
Expected: green.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/ web/src/app/layout.tsx
git commit -m "feat(web): providers with auth context, auth gate, nav shell"
```

### Task 5: pages — /login, / routing, /guide placeholder

**Files:**
- Create: `web/src/app/login/page.tsx`
- Create: `web/src/app/guide/page.tsx`
- Modify: `web/src/app/page.tsx` (replace hero with auth-state redirect)

**Interfaces:**
- Consumes: everything from Tasks 3–4.
- Produces: the issue's routing contract; `/guide` placeholder that issue #18 replaces (keep the `AuthGate` + `Nav` wrapper pattern when replacing the body).

- [ ] **Step 1: Write `web/src/app/login/page.tsx`**

```tsx
"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";
import { config } from "@/lib/config";
import { signInWithGoogle } from "@/lib/firebase";

export default function LoginPage() {
  const { user, loading } = useAuth();
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!loading && user) {
      router.replace("/guide");
    }
  }, [loading, user, router]);

  return (
    <main className="flex flex-1 items-center justify-center p-8">
      <div className="w-full max-w-sm rounded-xl border border-zinc-200 p-8 text-center dark:border-zinc-800">
        <h1 className="text-2xl font-semibold text-zinc-950 dark:text-zinc-50">Lineup</h1>
        <p className="mt-2 text-sm text-zinc-500">Your week of TV, planned like a lineup</p>
        <button
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            setError(null);
            try {
              await signInWithGoogle();
              router.replace("/guide");
            } catch {
              setError("Sign-in failed. Try again.");
              setBusy(false);
            }
          }}
          className="mt-6 w-full rounded-lg bg-zinc-950 px-4 py-2 text-zinc-50 disabled:opacity-50 dark:bg-zinc-50 dark:text-zinc-950"
        >
          Continue with Google
        </button>
        {config.appleAuth && (
          <button className="mt-3 w-full rounded-lg border border-zinc-300 px-4 py-2 text-zinc-950 dark:border-zinc-700 dark:text-zinc-50">
            Continue with Apple
          </button>
        )}
        {error && <p className="mt-3 text-sm text-red-600">{error}</p>}
      </div>
    </main>
  );
}
```

- [ ] **Step 2: Replace `web/src/app/page.tsx`**

```tsx
"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

import { useAuth } from "@/components/Providers";

// Root route: pure dispatcher on auth state. The marketing landing returns
// with the launch pass (issue #19).
export default function Home() {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (loading) return;
    router.replace(user ? "/guide" : "/login");
  }, [user, loading, router]);

  return null;
}
```

- [ ] **Step 3: Write `web/src/app/guide/page.tsx`**

```tsx
"use client";

import { useQuery } from "@tanstack/react-query";

import { AuthGate } from "@/components/AuthGate";
import { Nav } from "@/components/Nav";
import { api } from "@/lib/api";
import type { User } from "@/lib/types";

// Placeholder guide page: proves the authed API loop end-to-end. Issue #18
// replaces GuideBody; keep the AuthGate + Nav wrapper.
function GuideBody() {
  const { data, error, isPending } = useQuery({
    queryKey: ["me"],
    queryFn: () => api<User>("/v1/me"),
  });

  if (isPending) return <p className="p-8 text-sm text-zinc-500">Loading…</p>;
  if (error) return <p className="p-8 text-sm text-red-600">Could not load your profile.</p>;

  return (
    <div className="p-8">
      <h1 className="text-xl font-semibold text-zinc-950 dark:text-zinc-50">Guide coming soon</h1>
      <p className="mt-2 text-sm text-zinc-500">
        Signed in as {data.email} · region {data.region}
      </p>
    </div>
  );
}

export default function GuidePage() {
  return (
    <AuthGate>
      <Nav />
      <GuideBody />
    </AuthGate>
  );
}
```

- [ ] **Step 4: Verify**

Run: `cd web && pnpm run build && pnpm run lint`
Expected: green; build's route table lists `/`, `/login`, `/guide`.

- [ ] **Step 5: Commit**

```bash
git add web/src/app/
git commit -m "feat(web): login page, auth-routed root, protected guide placeholder"
```

### Task 6: live acceptance + stack submit (controller-inline)

**Files:** none (verification + stacked PRs)

**Interfaces:**
- Consumes: everything; `infra/local-dev.md` process recipe; gh-stack.

- [ ] **Step 1: Boot the stack** — Postgres (`docker start lineup-pg` if stopped), emulator (`firebase emulators:start --only auth --project demo-lineup`), API (env per local-dev.md — `FIREBASE_PROJECT_ID=demo-lineup FIREBASE_AUTH_EMULATOR_HOST=localhost:9099`), web (`cd web && pnpm run dev --port 3001`).
- [ ] **Step 2: Browser-driven acceptance** (claude-in-chrome): open `http://localhost:3001` → expect redirect to `/login`; click "Continue with Google" → emulator account picker popup → add/select an account → expect landing on `/guide` showing "Signed in as <email> · region US"; sign out → expect `/login`; navigate directly to `/guide` signed out → expect bounce to `/login`. Screenshot the signed-in `/guide`.
- [ ] **Step 3: Confirm no console errors** (browser console read) and `GET /v1/me` 200 in the network log.
- [ ] **Step 4: Stack submit** — `gh stack submit --auto --draft` (creates/updates draft PRs for BOTH layers: #6's branch PR based on main, #15's PR based on #6's branch), then `gh pr edit` each with proper bodies: #15's notes deviations (config-object Apple flag, emulator acceptance, /guide placeholder, landing hero replaced) and closes #15; #6's draft notes it stays draft pending the cloud health gate + value fill.
- [ ] **Step 5: Report** PR URLs + acceptance evidence to the user. Merging: user's call, #6 first then #15 (stack order).

---

## Self-review notes

- Deviation coverage: all four spec deviations appear in tasks (config flag T2, emulator acceptance T6, /guide placeholder T5, layout metadata T2).
- Type consistency: `useAuth` shape `{user, loading}` consistent across T4/T5; `api<T>` + `ApiError` signatures consistent T3/T5; config fields T2 match spec.
- dev `projectId` changes to `demo-lineup` in T2 (emulator requires it) — supersedes the placeholder-era value on the #6 layer for the dev block only; prod untouched. Called out in T2's note and the PR body.
- Web has no test runner by design; Go CORS change is TDD'd (T1).
