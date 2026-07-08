# Firebase Auth + App Hosting (issue #6) — design

Date: 2026-07-08
Status: approved pending user review
Issue: [#6 feat(infra): firebase auth and app hosting](https://github.com/shottah/lineup/issues/6)

## Context

The GCP project `lineup-app-ae6b` was soft-deleted on 2026-07-05 (over what turned
out to be a misread symptom: the API's correct 404 on `/`, a route it does not
register) and restored on 2026-07-08. At the time of writing, Google's data
planes are still propagating the restore (Artifact Registry pulls fail with
"project has been deleted"; the Cloud Run edge serves generic 404s; the
`lineup-db` boot disk is `RESTORING`). Two background watchers track this.

Three lessons from that episode are baked into this design:

1. **Firebase enablement is a deliberate, gated, manual-initiated step.**
   Enabling `firebase.googleapis.com` (bootstrap.sh) does not add a project to
   Firebase; `firebase projects:addfirebase` does, and it is irreversible.
2. **Use the canonical Cloud Run URL** (`status.url`,
   `https://lineup-api-zzwkjc5sdq-uc.a.run.app`) in anything durable.
3. **Cloud mutations happen one at a time with explicit user approval.**

## Scope

**Repo artifacts** (branch `feat/6-firebase-apphosting`, squash-merge):

- `infra/firebase.md` — runbook for all cloud steps (below)
- `web/lib/config.ts` — committed client config object, `NODE_ENV`-switched
- `web/apphosting.yaml` — `runConfig` cost guard only (no env section)
- `infra/README.md` — link the runbook from the manual-steps checklist

**Cloud state** (CLI-assisted, per-step user approval, health-gated):

- Project added to Firebase; web app `lineup-web` registered
- Google sign-in provider enabled
- Web API key hardened (restrictions, not secrecy — see below)
- App Hosting backend connected to `shottah/lineup`, `rootDir: web`, live
  branch `main`, region `us-central1`

**Out of scope:** Apple sign-in (deferred; documented), the web auth flow
(issue #15), API token-verification middleware (issue #8).

**Deviation from the issue text:** issue #6 specifies `NEXT_PUBLIC_*` env vars
in `apphosting.yaml`. All four values are public client config, so they are
committed in `web/lib/config.ts` instead and the env section is dropped. The
PR description notes this.

## `web/lib/config.ts`

```ts
const configs = {
  development: {
    firebase: {
      apiKey: "<from sdkconfig>", // public client key; hardened via restrictions
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "lineup-app-ae6b",
    },
    apiUrl: "http://localhost:8080",
  },
  production: {
    firebase: {
      apiKey: "<from sdkconfig>",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "lineup-app-ae6b",
    },
    apiUrl: "https://lineup-api-zzwkjc5sdq-uc.a.run.app",
  },
} as const;

export const config =
  configs[process.env.NODE_ENV === "production" ? "production" : "development"];
```

- `next build` (App Hosting) → production; `next dev` → development.
- Both environments share the single Firebase project for now; a dev project
  has an obvious slot if one ever exists.
- The `<from sdkconfig>` placeholders are filled during cloud execution and
  MUST be real values before the branch merges (acceptance depends on it).

## `web/apphosting.yaml`

```yaml
runConfig:
  minInstances: 0 # scale to zero, free-tier ethos
  maxInstances: 2 # cap runaway scaling
```

No env section. The file exists for the cost guard and as the anchor for any
future App Hosting build config.

## Runbook (`infra/firebase.md`) — ordered steps

Each step: CLI command (console fallback where it exists), what it does, and a
read-only verification. Steps are independently re-runnable.

1. **Health gate.** Do not proceed until ALL of: prod `/healthz` returns 200;
   `docker pull us-central1-docker.pkg.dev/lineup-app-ae6b/api/api:1830fa4`
   succeeds; `lineup-db` disk is `READY`. Any failure = data planes still see
   a deleted project; enabling Firebase onto that risks a wedged,
   irreversible state.
2. **Add project to Firebase** — `firebase projects:addfirebase
   lineup-app-ae6b`. THE irreversible step (Firebase cannot be removed from a
   project). Verify: `firebase projects:list` shows the project.
3. **Register web app** — `firebase apps:create web lineup-web`, then
   `firebase apps:sdkconfig web` to fetch apiKey/authDomain/projectId; commit
   them into `web/lib/config.ts`. Verify: `firebase apps:list`.
4. **Enable Google sign-in** — Firebase console → Authentication → Sign-in
   method → Google. Console-only on purpose: the toggle auto-provisions the
   OAuth client that the CLI path requires wiring by hand. Verify: provider
   listed as enabled in console.
5. **Create App Hosting backend** — `firebase apphosting:backends:create`
   (region `us-central1`, repo `shottah/lineup`, `rootDir: web`, live branch
   `main`). Includes the browser-only GitHub app grant; documented
   run-pause-grant-rerun, same pattern as `create-trigger.sh`. Verify:
   `firebase apphosting:backends:list`.
6. **Harden the web API key** (public, not secret) — GCP console → APIs &
   Services → Credentials → the auto-created browser key:
   - Application restriction: HTTP referrers — the App Hosting domain(s),
     `lineup-app-ae6b.firebaseapp.com`, `lineup-app-ae6b.web.app`, and
     `localhost:*` for dev.
   - API restriction: start with only Identity Toolkit API, Token Service
     API, and Firebase Installations API; if the hosted site surfaces a
     blocked-API error, add that specific API rather than loosening broadly.
   Rationale: caps quota-burn/abuse by third parties reusing the key. This is
   abuse mitigation, NOT secrecy — the key ships in the JS bundle regardless.
   Reversible at any time. Verify: sign-in still works from the hosted domain
   and localhost after restrictions apply.
7. **Apple sign-in — deferred.** Needs: Apple Developer Program membership,
   a Services ID, a Sign in with Apple key, provider config in Firebase.
   Documented so future work doesn't rediscover it.

## Verification and acceptance

- Per-step read-only verifications as listed above.
- **Acceptance (from the issue):** after squash-merge, App Hosting auto-builds
  `web/` from `main`; the hosted URL serves the landing page (curl for the
  hero text + a browser check). The landing page has no runtime Firebase
  dependency (auth flow is issue #15), so acceptance proves the hosting
  pipeline, not auth.
- Local check before merge: `pnpm run build` in `web/` passes with the real
  config values. (`web/` uses pnpm — App Hosting detects it via
  `pnpm-lock.yaml`; bun was ruled out because App Hosting's buildpacks only
  support npm/yarn/pnpm.)

## Rollback and failure handling

- Reversible: App Hosting backend (delete), web app (delete), provider
  (toggle off), key restrictions (edit). Irreversible: `projects:addfirebase`
  — hence the health gate in front of it.
- First App Hosting build failure = hosted URL serves nothing; fix forward on
  `main`. No partial user-facing state.
- Any CLI step failing mid-run: re-run that step; all steps are idempotent or
  safely re-runnable.

## Workflow

Branch `feat/6-firebase-apphosting`; spec + repo artifacts land first with
placeholders; cloud steps execute behind the health gate with per-step
approval; real config values committed and `npm run build` verified locally;
squash-merge; acceptance verified against the hosted URL after the rollout
(builds only trigger from `main`, so final acceptance is necessarily
post-merge).
