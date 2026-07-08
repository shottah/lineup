# Local-first Firebase auth: emulator harness + API middleware (issue #8) — design

Date: 2026-07-08
Status: approved (user: "sounds good", after design presentation)
Issues: [#8 feat(api): firebase auth middleware and me endpoints](https://github.com/shottah/lineup/issues/8); harness also serves [#15](https://github.com/shottah/lineup/issues/15) later.

## Context

User pivot (2026-07-08): build the Firebase integration locally against the
local stack; infra deployment is deprioritized while `lineup-app-ae6b`
recovers from its restore (see the issue-6 spec and `.superpowers/sdd/progress.md`
for the TCP-wedge saga). The Firebase **Auth Emulator** enables this with
zero cloud dependency: it fakes Google sign-in, mints real-format ID tokens,
and both SDKs support it natively (web: `connectAuthEmulator`; Go Admin SDK:
`FIREBASE_AUTH_EMULATOR_HOST`). Vertical slice: harness → issue #8 (this
spec) → issue #15 (web flow, its own spec later).

## Emulator harness

- `firebase.json` at repo root: Auth emulator only, port 9099, emulator UI
  disabled. No `.firebaserc` (project passed on the command line).
- Project ID for local work: **`demo-lineup`**. The `demo-` prefix is
  Firebase's guaranteed-offline convention — the emulator will not touch any
  real cloud resource, categorically protecting the half-restored project.
- Run: `firebase emulators:start --only auth --project demo-lineup`
  (requires Java ≥ 11; Java 17 confirmed installed).
- `infra/local-dev.md`: the full local recipe — Postgres 16 in Docker on
  :5433, emulator on :9099, API on :8080 (`DATABASE_URL`,
  `FIREBASE_PROJECT_ID=demo-lineup`, `FIREBASE_AUTH_EMULATOR_HOST=localhost:9099`),
  web on :3001. Today this knowledge lives only in a chat session.

## Issue #8 design (branch `feat/8-auth-me`, off `main`, squash-merge)

### Token verification

- `api/internal/fbauth` package:
  - `Identity{UID, Email, DisplayName string}` — what a verified token
    yields. Email is required downstream (`users.email` is `NOT NULL`);
    DisplayName may be empty.
  - `TokenVerifier` interface: `VerifyIDToken(ctx, rawToken string) (Identity, error)`.
  - Real implementation wraps `firebase.google.com/go/v4`'s auth client,
    initialized at boot from `FIREBASE_PROJECT_ID`. The SDK automatically
    targets the emulator when `FIREBASE_AUTH_EMULATOR_HOST` is set — no
    code branch needed.
  - `fbauth.Fake` for tests: maps raw token strings to Identities/errors.
- `config.Load` change: `FIREBASE_PROJECT_ID` becomes **required** (the API
  now consumes it; prod already sets it in `run-service.yaml`; local uses
  `demo-lineup`). Error type mirrors `ErrMissingDatabaseURL`.

### Middleware and endpoints

- `RequireAuth` middleware (httpserver): `Authorization: Bearer <token>` →
  verify → `UpsertUserByFirebaseUID(Identity)` → user on request context.
  401 (`{"error":"unauthorized"}`) on missing/malformed header or failed
  verification. Upsert failure → 500.
- `GET /v1/me` → 200 with the user row:
  `{"id":1,"email":"…","display_name":"…","region":"US","schedule_prefs":{…}}`.
- `PATCH /v1/me` → partial update of `region` and/or `schedule_prefs`;
  200 with updated row; **422** with field-naming error body on invalid
  input; 400 on non-JSON body.
- Prefs shape (issue-fixed): `{"windows":{"mon":{"enabled":true,"start":"19:00","end":"23:00"}, … "sun":{…}}}`.
  Validation: exactly the seven day keys, `start`/`end` must be `HH:MM`
  (24h), `start < end`. Defaults (applied at first upsert): all seven days
  enabled 19:00–23:00.
- Store (`api/internal/store/users.go`): `UpsertUserByFirebaseUID`
  (INSERT … ON CONFLICT (firebase_uid) DO UPDATE email/display_name/updated_at,
  RETURNING row; inserts default prefs) and `UpdateUserPrefs`
  (region and/or prefs, RETURNING row).

### Testing

- Handlers/middleware: httptest against the chi router with `fbauth.Fake`
  and a `UserStore` interface (implemented by `*store.Store`) — hermetic,
  no emulator or Postgres in unit tests. CI unchanged.
- Store methods: integration tests gated on `TEST_DATABASE_URL`
  (`t.Skip` when unset) against local dockerized Postgres.
- Prefs validation: pure-function table tests.
- **Live local verification** (the emulator's unique value): emulator up →
  mint a token via its REST signUp endpoint → `curl /v1/me` with the Bearer
  token → 200 and a real row in local Postgres. Proves the *real* verifier
  path before any cloud project exists.

### Out of scope

Issue #15 (web flow, CORS middleware — its issue text puts CORS on the #15
branch), Apple sign-in, everything cloud (parked issue-6 plan Tasks 3–7).

## Rollback / failure notes

All repo changes; no cloud state. The one behavior change to existing code
is `config.Load` requiring `FIREBASE_PROJECT_ID` — deployments without it
fail fast at boot with a clear error (correct: the API cannot verify tokens
without it).
