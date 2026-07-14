# infra

Bootstrap and infrastructure scripts for the Lineup GCP project.

## GCP project

- **Project ID:** `lineup-app-ae6b` (the plain `lineup-app` project ID was already taken globally, so a random 4-hex-char suffix was appended per the bootstrap task's fallback rule)
- **Billing account:** Strata Org Billing Account (linked; the account ID itself is intentionally not recorded in this public repo — see Security note below)
- **Region:** `us-central1`

## Re-provisioning on a fresh project

If the project is being re-created under a new project id, run these in order:

0. **Retarget the infra files at the new id** (rewrites every literal project id
   in `clouddeploy.yaml`, `run-service.yaml`, this README, and the
   `create-trigger.sh` default; `cloudbuild.yaml` uses Cloud Build's native
   `$PROJECT_ID` and needs no change; idempotent, no cloud calls):

   ```bash
   bash infra/retarget.sh <NEW_PROJECT_ID>
   ```

   The script detects the currently-targeted id from `clouddeploy.yaml`, so it is
   the single source of truth. It warns about any remaining occurrences elsewhere
   under `infra/` (e.g. `infra/db/`) for manual review.
1. `bash infra/bootstrap.sh <NEW_PROJECT_ID> <BILLING_ACCOUNT_ID>` (project, APIs, repo, secrets, SA)
2. `bash infra/db/create-vm.sh` (Postgres VM — must exist before step 3, which reads its internal IP)
3. `gcloud deploy apply --file=infra/clouddeploy.yaml --region=us-central1 --project <NEW_PROJECT_ID>` then `bash infra/create-trigger.sh` (pipeline, IAM, database-url secret, trigger)

## What `bootstrap.sh` provisions

Running the script against the project ID above (idempotent — safe to re-run):

- Creates the GCP project (if it doesn't already exist) and links it to the given billing account
- Enables the required APIs: Cloud Run, Cloud Build, Cloud Deploy, Artifact Registry, Secret Manager, Compute Engine, IAP, Firebase, Identity Toolkit, Firebase App Hosting
- Creates an Artifact Registry Docker repo named `api` in `us-central1`
- Creates two Secret Manager secrets:
  - `db-password` — auto-generated random value (24 bytes, base64)
  - `tmdb-read-token` — placeholder value `REPLACE_ME` (see manual step below)
- Creates a service account `api-runtime@<PROJECT_ID>.iam.gserviceaccount.com`
- Grants `api-runtime@` the `roles/secretmanager.secretAccessor` role at the project level

## Running it

Requires `gcloud` authenticated as a principal with permission to create projects and link billing (e.g. `gcloud auth login`), and the `openssl` CLI.

```bash
bash infra/bootstrap.sh <PROJECT_ID> <BILLING_ACCOUNT_ID>
```

Example (using the recorded project ID):

```bash
bash infra/bootstrap.sh lineup-app-ae6b <BILLING_ACCOUNT_ID>
```

`<BILLING_ACCOUNT_ID>` is deliberately not shown here — pass the real billing account ID (format `XXXXXX-XXXXXX-XXXXXX`) on the command line only. **Never commit a billing account ID to this repo.**

The script is idempotent: every resource-creation step first checks whether the resource exists and skips creation if so. Re-running it after a successful run produces the same "Bootstrap complete" output with no errors.

## Deploy pipeline (Cloud Build -> Artifact Registry -> Cloud Deploy -> Cloud Run)

Files: `cloudbuild.yaml` (test, build, push, create release), `clouddeploy.yaml`
(DeliveryPipeline `lineup-api` -> Target `prod`), `skaffold.yaml` +
`run-service.yaml` (Cloud Run service manifest rendered by the Cloud Run
skaffold deployer), `create-trigger.sh` (idempotent IAM grants, `database-url`
secret, GitHub connection + trigger).

One-time setup (idempotent, safe to re-run):

```bash
gcloud deploy apply --file=infra/clouddeploy.yaml --region=us-central1 --project lineup-app-ae6b
bash infra/create-trigger.sh
```

`create-trigger.sh` creates the `database-url` secret from `db-password` and the
`lineup-db` VM internal IP without ever printing the password (the secret value
is piped straight between gcloud invocations). If the 2nd-gen GitHub connection
is not yet authorized, the script prints the one-time console OAuth/App-install
URL and stops before trigger creation; complete that step in a browser and
re-run the script.

Manual pipeline run (e.g. before the trigger exists — `$SHORT_SHA` is only
populated by triggered builds):

```bash
gcloud builds submit --config=infra/cloudbuild.yaml \
  --substitutions=SHORT_SHA=$(git rev-parse --short HEAD) --project lineup-app-ae6b .
```

The service is public via `run.googleapis.com/invoker-iam-disabled` (the org's
domain-restricted-sharing policy forbids `allUsers` IAM bindings); application
auth is Firebase ID tokens, added in a later task.

## Manual steps checklist

- [ ] **Complete the GitHub connection OAuth grant** (one-time, in a browser,
  as a github.com/shottah admin), then re-run `bash infra/create-trigger.sh` to
  create the `api-deploy` trigger. The URL is printed by the script
  (`gcloud builds connections describe lineup-github --region=us-central1`
  shows it under `installationState.actionUri`).

- [ ] **Revoke the Cloud Build P4SA's `secretmanager.admin` grant** once the
  GitHub connection reaches `installationState: COMPLETE` (it is only needed
  while the connection's OAuth token secret is being created):

  ```bash
  PROJECT_NUMBER=$(gcloud projects describe lineup-app-ae6b --format='value(projectNumber)')
  gcloud projects remove-iam-policy-binding lineup-app-ae6b \
    --member="serviceAccount:service-${PROJECT_NUMBER}@gcp-sa-cloudbuild.iam.gserviceaccount.com" \
    --role=roles/secretmanager.admin -q
  ```

- [ ] **Replace the placeholder TMDB read token** (v4 read access token, sent
  as a Bearer header — not the v3 api key) with the real value:

  ```bash
  printf '%s' "$(cat ~/.lineup/tmdb_read_token)" | gcloud secrets versions add tmdb-read-token --data-file=- --project lineup-app-ae6b
  ```

- [ ] Firebase console configuration (Firebase Auth / Identity Platform setup, App Hosting backend wiring) — tracked as part of Task 6, not covered by this script.

## Security note

This is a public repository. The GCP billing account ID must never be committed to any tracked file — only the project ID is safe to commit. When running `bootstrap.sh` locally or in CI, pass the billing account ID as a command-line argument (or via a secret store), never hardcode it here.
