# Web auth flow, API client, app shell (issue #15) — design

Date: 2026-07-08
Status: approved (user: "Proceed to spec, plan, execution", after design-delta presentation)
Issue: [#15 feat(web): auth flow, api client, app shell](https://github.com/shottah/lineup/issues/15)

## Context

Third slice of the local-first Firebase pivot: the harness and API auth are
on `main` (issue #8, PR #26). This spec covers the web half, developed and
verified entirely against the Auth Emulator. **Stacked branch:**
`feat/15-web-auth-shell` sits on `feat/6-firebase-apphosting` (which holds
the pnpm migration and `web/src/lib/config.ts`); managed with `gh stack`;
#15's PR bases on #6's branch, so it merges after #6.

Web stack reality check: Next.js 16.2.10 (App Router) + React 19 + Tailwind
4 — newer than model training data; `web/AGENTS.md` mandates reading
`web/node_modules/next/dist/docs/` before writing page/routing code.
`next/navigation` (`redirect`, `useRouter`) confirmed unchanged.

## Deviations from the issue text (noted in the PR)

- `NEXT_PUBLIC_APPLE_AUTH=1` env flag → `config.appleAuth: boolean` in
  `config.ts` (config-object pattern, same rationale as #6's env deviation).
- Acceptance's "sign in with Google in dev" runs against the **emulator's**
  fake Google account picker (`connectAuthEmulator`), not live Google — the
  cloud project is not usable yet. The code path is identical in prod.
- Adds a minimal protected `/guide` placeholder page (not in the issue) so
  the signed-in landing target exists; issue #18 replaces it.
- Fixes `layout.tsx` metadata ("Create Next App" → "Lineup") — ledgered
  leftover from the skeleton review.

## Design

### Config (`web/src/lib/config.ts`, extended in this branch)

Dev block gains `authEmulatorHost: "http://localhost:9099"`; both blocks
gain `appleAuth: false`. Production has NO `authEmulatorHost` key — its
absence is what keeps emulator wiring out of prod bundles.

### `web/src/lib/firebase.ts`

Singleton module: `initializeApp(config.firebase)`, `getAuth`, and — only
when `config.authEmulatorHost` is set — `connectAuthEmulator(auth, host)`.
Exports `auth`, `signInWithGoogle()` (GoogleAuthProvider + signInWithPopup;
the emulator serves its own account-picker page in the popup), and
`signOutUser()`.

### `web/src/lib/api.ts` + `web/src/lib/types.ts`

`api<T>(path, init?)`: prefixes `config.apiUrl`, attaches
`Authorization: Bearer <await auth.currentUser.getIdToken()>` (fresh token
each call; firebase SDK caches/refreshes internally), sets JSON headers,
parses JSON. Non-2xx → throws `ApiError{status, code}` where `code` comes
from the API's `{"error":"<code>"}` body (fallback `"unknown"`). No token
(signed out) → throws `ApiError{status: 401, code: "no_session"}` without
calling the network. `types.ts`: `User` and `SchedulePrefs`/`DayWindow`
mirroring `/v1/me` JSON exactly.

### Shell components (`web/src/components/`)

- `Providers` (client): TanStack Query `QueryClientProvider` + auth context
  (`onAuthStateChanged` → `{user: FirebaseUser | null, loading: boolean}`),
  exported `useAuth()` hook. Mounted in `layout.tsx`.
- `AuthGate` (client): while `loading`, render nothing (avoids flash);
  unauthenticated → `router.replace("/login")`; else children.
- `Nav` (client): app name linking `/guide`, user email, sign-out button
  (signs out then `router.replace("/login")`).

### Pages

- `/login`: centered card; "Continue with Google" button →
  `signInWithGoogle()` then `router.replace("/guide")`; Apple button
  rendered only when `config.appleAuth` (dead until Apple lands). If
  already signed in, redirect to `/guide`.
- `/`: client redirect on auth state — `/guide` if signed in, `/login`
  otherwise (replaces the skeleton landing hero; the marketing landing
  returns with issue #19's launch pass).
- `/guide`: wrapped in `AuthGate` + `Nav`; placeholder body that also
  proves the API loop: `useQuery` fetching `/v1/me` and rendering the
  user's email + region ("Guide coming soon" copy). Issue #18 replaces the
  body; the AuthGate/Nav wrapper pattern is the durable part.

### API CORS middleware (preceding commit, `api/`)

`github.com/go-chi/cors` on the router: allowed origins
`http://localhost:3000`, `http://localhost:3001`, and the canonical hosted
origins (`https://lineup-app-ae6b.web.app`, `https://lineup-app-ae6b.firebaseapp.com`,
plus the App Hosting domain once it exists — config left extensible);
methods GET/PATCH/POST/OPTIONS; headers Authorization + Content-Type;
`AllowCredentials: false` (bearer tokens, not cookies). Applied before the
router's routes so preflights succeed. Unit test: OPTIONS preflight from an
allowed origin gets the ACAO header; disallowed origin does not.

### Testing

- Go (CORS): chi httptest, TDD as usual.
- Web: no test runner exists and none is added (consistent with the #8
  decision; the issue's acceptance is build + manual). Verification =
  `pnpm run build` + `pnpm run lint` + the live emulator flow.
- Live acceptance (browser-driven): emulator + API + web up; sign in via
  the emulator account picker; land on `/guide`; see `/v1/me` data render;
  signed-out `/guide` access bounces to `/login`.

### Out of scope

Real Google/Apple providers, cloud deploys, the real guide UI (#18),
search/title pages (#16), profile shelves (#17).

## Rollback / risk

All repo changes; no cloud state. Stacked-merge risk: #15 cannot merge
before #6; if #6 stalls on cloud recovery, the stack can be restructured
(gh stack unstack + cherry-pick) — accepted by user in the merge-order
decision. The skeleton's landing hero is replaced at `/`; acceptable, the
launch pass (#19) owns final landing copy.
