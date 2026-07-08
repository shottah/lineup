# Firebase setup runbook (issue #6)

Firebase Auth (Google) + App Hosting for `web/`. Execution rule: every
mutating command is run one at a time, shown and explained first, with
explicit approval. Steps are independently re-runnable. CLI where possible,
console where genuinely browser-only.

Prerequisites: `firebase` CLI ≥ 15 logged in as the project owner
(`firebase login`), `gcloud` authed, Docker running with the Artifact
Registry credential helper configured
(`gcloud auth configure-docker us-central1-docker.pkg.dev`) for the
health gate.

## 0. Health gate — run before ANY step below

The project was soft-deleted and restored on 2026-07-08. Firebase enablement
(step 1) is irreversible, so do not run it until Google's data planes all see
the project as alive. ALL THREE must pass:

```bash
curl -s -o /dev/null -w '%{http_code}\n' https://lineup-api-zzwkjc5sdq-uc.a.run.app/healthz   # want: 200
docker pull us-central1-docker.pkg.dev/lineup-app-ae6b/api/api:1830fa4                        # want: pull succeeds
gcloud compute disks describe lineup-db --zone=us-central1-a --project=lineup-app-ae6b --format='value(status)'  # want: READY
```

Any failure = still propagating; wait and re-run. Do not proceed partially.

TCP-wedge caveat (see `.superpowers/sdd/progress.md`, 2026-07-05 diagnosis):
this project previously had a Google-side fault where run.app hostnames were
unreachable over TCP (curl) while fine over HTTP/3 (Chrome). If the curl
check above still fails long after the other two pass, ALSO test the same
URL in Chrome. Chrome-200-but-curl-404 means the wedge survived the
delete/restore cycle: STOP — the project needs the re-pave path (ledger's
checklist, new project ID), and this runbook's project IDs must be
retargeted before use.

## 1. Add the project to Firebase (IRREVERSIBLE)

Enabling `firebase.googleapis.com` (done by bootstrap.sh) does NOT do this;
this is the step that cannot be automated away and cannot be undone.

```bash
firebase projects:addfirebase lineup-app-ae6b
```

Verify: `firebase projects:list` shows `lineup-app-ae6b`.
Console fallback: console.firebase.google.com → Add project → select
`lineup-app-ae6b`.

## 2. Register the web app

```bash
firebase apps:create web lineup-web --project lineup-app-ae6b
```

Verify: `firebase apps:list --project lineup-app-ae6b` shows `lineup-web`.

## 3. Fetch the SDK config and commit it

```bash
firebase apps:sdkconfig web --project lineup-app-ae6b
```

Copy `apiKey` into BOTH the development and production blocks of
`web/src/lib/config.ts`, replacing every `FILLED_IN_BY_RUNBOOK_STEP_3`.
Confirm `authDomain` and the PRODUCTION block's `projectId` match what the
command prints; fix production values if Firebase reports different ones.
Do NOT "fix" the development block's `projectId: "demo-lineup"` — it is
deliberate (the Auth emulator only issues tokens for that id; reverting it
to the real project id breaks all local sign-in with 401s).
Then: `cd web && pnpm run build` must pass, and
`git grep -n FILLED_IN_BY_RUNBOOK_STEP_3 -- ':/web'` must return nothing. Commit.

## 4. Enable Google sign-in (console-only, deliberately)

Firebase console → Authentication → Get started → Sign-in method → Google →
Enable. Pick the support email when prompted. The console toggle
auto-provisions the OAuth client; the CLI path requires wiring one by hand,
which is why this step stays in the console.

Verify: provider row shows "Enabled".
Apple: NOT now — see §7.

## 5. Create the App Hosting backend

```bash
firebase apphosting:backends:create --project lineup-app-ae6b --location us-central1
```

Interactive prompts: connect GitHub repo `shottah/lineup`, root directory
`web`, live branch `main`, backend name `lineup-web`. The GitHub app grant
is browser-only: if the CLI pauses with a URL, complete the grant as a
github.com/shottah admin, then re-run the command (same
run-pause-grant-rerun pattern as `infra/create-trigger.sh`).

Verify: `firebase apphosting:backends:list --project lineup-app-ae6b` shows
the backend with repo `shottah/lineup` and branch `main`.

Then add the backend's `*.hosted.app` domain to the API's CORS allowlist
(`AllowedOrigins` in `api/internal/httpserver/server.go`) and deploy —
without it, the hosted web app's API calls fail CORS preflight.

## 6. Harden the web API key (public, not secret)

GCP console → APIs & Services → Credentials → auto-created browser key
(created by step 2):

- Application restrictions → HTTP referrers: the App Hosting domain(s) shown
  by step 5's verify (`*.hosted.app`), `lineup-app-ae6b.firebaseapp.com`,
  `lineup-app-ae6b.web.app`, and `http://localhost:3001` + `http://localhost:3000`
  for dev.
- API restrictions → Restrict key: Identity Toolkit API, Token Service API,
  Firebase Installations API. If the hosted site later surfaces a
  blocked-API error, add that one API; never loosen to unrestricted.

This caps third-party quota abuse of a key that ships in the JS bundle
regardless. It is reversible at any time.

Verify: after restrictions propagate (~5 min), sign-in still works from the
hosted domain and from `pnpm run dev` locally (checkable only once issue #15
lands the auth flow; until then just confirm the hosted landing page loads).

## 7. Apple sign-in — deferred

Requires: Apple Developer Program membership, a Services ID, a Sign in with
Apple private key, then Firebase console → Authentication → Sign-in method →
Apple with those values. Tracked as a launch-lagging follow-up per the v1
design spec; nothing here blocks on it.
