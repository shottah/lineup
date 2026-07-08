# Firebase Auth + App Hosting (issue #6) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Interactivity warning:** Tasks 3–7 mutate cloud state or need the user's
> browser. They CANNOT be delegated to unattended subagents: every cloud
> mutation must be shown to the user with an explanation and executed only
> after their explicit approval (standing rule from 2026-07-08). Only Tasks
> 1–2 are safely delegable.

**Goal:** Enable Firebase Auth (Google provider) and App Hosting for `web/` on the restored `lineup-app-ae6b` project, with all cloud steps documented in a re-runnable runbook.

**Architecture:** Repo artifacts land first (runbook, committed client-config object); cloud state is then mutated step-by-step behind a propagation health gate, CLI where possible, console where required; real config values replace placeholders before merge; acceptance is verified post-merge against the hosted URL.

**Tech Stack:** Firebase CLI 15.x (installed: 15.22.3), gcloud CLI, Next.js (App Router, TS) with pnpm, Firebase App Hosting.

## Global Constraints

- GCP project: `lineup-app-ae6b`; region `us-central1`; branch `feat/6-firebase-apphosting`; squash-merge.
- Every cloud-mutating command: show the exact command + plain-language explanation, await explicit user approval. One logical step at a time. Read-only verification bundled with an approved step is fine.
- Health gate (spec §Runbook step 1) must pass before ANY Firebase mutation: prod `/healthz` = 200 AND `docker pull us-central1-docker.pkg.dev/lineup-app-ae6b/api/api:1830fa4` succeeds AND `lineup-db` disk `READY`.
- `firebase projects:addfirebase` is IRREVERSIBLE — never run it while any health-gate check fails.
- Durable URLs use the canonical Cloud Run URL: `https://lineup-api-zzwkjc5sdq-uc.a.run.app`.
- Firebase web config values are public-by-design: committed in source, never in Secret Manager. Hardening = key restrictions (spec §Runbook step 6), not secrecy.
- `web/` uses pnpm (`pnpm-lock.yaml`, `packageManager: pnpm@10.33.3`). No `npm` commands.
- `web/` has no test runner (scripts: dev/build/start/lint only). Verification for the config module is `pnpm run build` (type-check) + `pnpm run lint`. Adding a test framework for one static object violates YAGNI; do not add one.
- Placeholder sentinel for not-yet-known values: the literal string `FILLED_IN_BY_RUNBOOK_STEP_3`. Task 7 greps for it and MUST fail the merge if present.
- `web/AGENTS.md` warns this Next.js version may differ from training data; Task 1 touches no Next.js APIs (plain TS module + `process.env.NODE_ENV`), so no docs reading is required for it.

---

### Task 1: `web/src/lib/config.ts` — client config object

**Files:**
- Create: `web/src/lib/config.ts`
- Verify (no change expected): `web/apphosting.yaml` (already `runConfig: {minInstances: 0, maxInstances: 2}` from PR #21)

**Interfaces:**
- Produces: `config` named export — `{ firebase: { apiKey: string; authDomain: string; projectId: string }, apiUrl: string }`. Issue #15's auth flow imports it as `import { config } from "@/lib/config"`.

- [ ] **Step 1: Write the file**

```ts
// web/src/lib/config.ts
//
// Client configuration, switched on NODE_ENV at build time.
// Every value here ships to the browser and is public by design; the
// Firebase apiKey is a client identifier (abuse-limited via API key
// restrictions in GCP), NOT a secret. Real secrets live in Secret Manager
// and never in this file.

const configs = {
  development: {
    firebase: {
      apiKey: "FILLED_IN_BY_RUNBOOK_STEP_3",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "lineup-app-ae6b",
    },
    apiUrl: "http://localhost:8080",
  },
  production: {
    firebase: {
      apiKey: "FILLED_IN_BY_RUNBOOK_STEP_3",
      authDomain: "lineup-app-ae6b.firebaseapp.com",
      projectId: "lineup-app-ae6b",
    },
    apiUrl: "https://lineup-api-zzwkjc5sdq-uc.a.run.app",
  },
} as const;

export const config =
  configs[process.env.NODE_ENV === "production" ? "production" : "development"];
```

- [ ] **Step 2: Verify build and lint pass**

Run: `cd web && pnpm run build && pnpm run lint`
Expected: build completes ("prerendered as static content" table), lint exits 0. An unused export is not a lint error in this config.

- [ ] **Step 3: Verify apphosting.yaml needs no change**

Run: `cat web/apphosting.yaml`
Expected output, byte-for-byte:

```yaml
runConfig:
  minInstances: 0
  maxInstances: 2
```

If it differs, STOP — the spec assumed this content; surface the difference instead of editing.

- [ ] **Step 4: Commit**

```bash
git add web/src/lib/config.ts
git commit -m "feat(web): NODE_ENV-switched client config object"
```

### Task 2: `infra/firebase.md` runbook + README link

**Files:**
- Create: `infra/firebase.md`
- Modify: `infra/README.md` (the Firebase bullet in "Manual steps checklist", currently ending "— tracked as part of Task 6, not covered by this script.")

**Interfaces:**
- Produces: the runbook that Tasks 3–6 execute step-by-step. Section numbers below are referenced by those tasks.

- [ ] **Step 1: Write `infra/firebase.md`** with exactly this content:

````markdown
# Firebase setup runbook (issue #6)

Firebase Auth (Google) + App Hosting for `web/`. Execution rule: every
mutating command is run one at a time, shown and explained first, with
explicit approval. Steps are independently re-runnable. CLI where possible,
console where genuinely browser-only.

Prerequisites: `firebase` CLI ≥ 15 logged in as the project owner
(`firebase login`), `gcloud` authed, Docker running (for the health gate).

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
Confirm `authDomain` and `projectId` printed by the command match the values
already committed there; fix them if Firebase reports different ones.
Then: `cd web && pnpm run build` must pass, and
`git grep -n FILLED_IN_BY_RUNBOOK_STEP_3` must return nothing. Commit.

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
````

- [ ] **Step 2: Update the README checklist bullet**

In `infra/README.md`, replace the line:

```markdown
- [ ] Firebase console configuration (Firebase Auth / Identity Platform setup, App Hosting backend wiring) — tracked as part of Task 6, not covered by this script.
```

with:

```markdown
- [ ] Firebase Auth + App Hosting setup — follow `infra/firebase.md` (health-gated runbook; the `projects:addfirebase` step is irreversible).
```

- [ ] **Step 3: Commit**

```bash
git add infra/firebase.md infra/README.md
git commit -m "docs(infra): firebase auth + app hosting runbook"
```

### Task 3: Health gate (runbook §0) — read-only, blocks everything below

**Files:** none (cloud verification only)

**Interfaces:**
- Consumes: background watchers from the 2026-07-08 session (edge `/healthz` poller, disk-restore poller) or manual re-runs of the same checks.
- Produces: a PASS/FAIL statement for each of the three checks, reported to the user. Tasks 4–6 are blocked until three PASSes.

- [ ] **Step 1: Run the three §0 commands** (exact commands in the runbook above). Expected: `200` / successful pull / `READY`.
- [ ] **Step 2: Report results to the user.** If any check fails, STOP — re-check later; do not begin Task 4.

### Task 4: Firebase enablement + web app + real config values (runbook §1–§3)

**Files:**
- Modify: `web/src/lib/config.ts` (replace both `FILLED_IN_BY_RUNBOOK_STEP_3` sentinels with the real `apiKey`)

**Interfaces:**
- Consumes: Task 3 PASS; user approval per command.
- Produces: Firebase-enabled project, registered `lineup-web` app, committed real config values. Task 6's backend create requires the Firebase enablement from here.

- [ ] **Step 1:** Present + run `firebase projects:addfirebase lineup-app-ae6b` (approval required; reconfirm Task 3 passed THIS session). Verify per §1.
- [ ] **Step 2:** Present + run `firebase apps:create web lineup-web --project lineup-app-ae6b` (approval). Verify per §2.
- [ ] **Step 3:** Run `firebase apps:sdkconfig web --project lineup-app-ae6b` (read-only). Copy `apiKey` into both config blocks; confirm `authDomain`/`projectId` match committed values.
- [ ] **Step 4: Verify no sentinel remains and build passes**

Run: `git grep -n FILLED_IN_BY_RUNBOOK_STEP_3 -- web/` → expect no output (exit 1).
Run: `cd web && pnpm run build` → expect success.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/config.ts
git commit -m "feat(web): real firebase client config values"
```

### Task 5: Google provider + API key hardening (runbook §4, §6 — console, user-driven)

**Files:** none (console state)

**Interfaces:**
- Consumes: Task 4 complete (provider needs Firebase Auth to exist; the key to harden is created by app registration).
- Produces: Google sign-in enabled; hardened key. Issue #15 depends on the provider being on.

- [ ] **Step 1:** Walk the user through §4 (Google provider toggle) in their browser; they confirm the provider row shows Enabled.
- [ ] **Step 2:** Walk the user through §6 (referrer + API restrictions). Note: §6's referrer list needs the App Hosting domain, which only exists after Task 6 — if executing in plan order, do §6 AFTER Task 6, or add the domain to the referrer list then. Either order is fine; the runbook's numbering favors doing hardening last.
- [ ] **Step 3:** Record in the PR description that §4/§6 were completed and by whom (console steps leave no repo diff).

### Task 6: App Hosting backend (runbook §5)

**Files:** none (cloud state)

**Interfaces:**
- Consumes: Task 4's Firebase enablement; user approval; user's browser for the GitHub app grant.
- Produces: backend `lineup-web` building `web/` from `main`. Task 7's acceptance depends on it.

- [ ] **Step 1:** Present + run `firebase apphosting:backends:create --project lineup-app-ae6b --location us-central1` (approval). Prompts: repo `shottah/lineup`, rootDir `web`, live branch `main`, name `lineup-web`. Pause for the browser GitHub grant if prompted, then re-run.
- [ ] **Step 2:** Verify per §5; note the `*.hosted.app` domain in the output and feed it back into Task 5 Step 2's referrer list if hardening ran first.

### Task 7: PR, merge, post-merge acceptance

**Files:** none new (assembles the branch: spec, plan, pnpm migration, config.ts, runbook, README)

**Interfaces:**
- Consumes: all prior tasks; user performs the squash-merge decision.
- Produces: issue #6 closed; hosted URL serving the landing page.

- [ ] **Step 1: Pre-PR checks**

Run: `git grep -n FILLED_IN_BY_RUNBOOK_STEP_3 -- web/` → no output.
Run: `cd web && pnpm run build && pnpm run lint` → both pass.

- [ ] **Step 2: Push + open PR** titled `feat(infra): firebase auth and app hosting` with body noting: closes #6; the config-object deviation from the issue's `NEXT_PUBLIC_*` env-var deliverable and why; that §4/§6 console steps were completed out-of-band; squash-merge per issue workflow.
- [ ] **Step 3: User squash-merges** (their call, in the GUI or approved CLI).
- [ ] **Step 4: Post-merge acceptance** — App Hosting auto-builds `main`. Poll (every ~2 min, up to 30 min):

```bash
curl -s https://<backend-domain>/ | grep -o "your week of TV"
```

Expected: match found (the landing-page hero). Also open it in a browser. If the build fails instead: read the build log in the Firebase console, fix forward on `main` — no rollback path exists pre-launch and none is needed.

- [ ] **Step 5:** Report acceptance evidence (curl output + build status) to the user; check the runbook item off in `infra/README.md` on `main` if desired as a follow-up commit.

---

## Self-review notes

- Spec coverage: config object (T1), runbook incl. hardening + Apple deferral (T2), health gate (T3), enablement/app/values (T4), provider + hardening (T5), backend (T6), acceptance + deviation note (T7). apphosting.yaml is verify-only (T1 S3) since PR #21 already committed the exact content.
- Ordering nuance between §6 hardening and §5 backend domain is called out explicitly in both T5 and T6 rather than hidden.
- No TDD cycle: no test runner exists in `web/`; global constraints document the build+lint verification substitute and forbid adding a framework for this.
