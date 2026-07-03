# infra

Bootstrap and infrastructure scripts for the Lineup GCP project.

## GCP project

- **Project ID:** `lineup-app-ae6b` (the plain `lineup-app` project ID was already taken globally, so a random 4-hex-char suffix was appended per the bootstrap task's fallback rule)
- **Billing account:** Strata Org Billing Account (linked; the account ID itself is intentionally not recorded in this public repo — see Security note below)
- **Region:** `us-central1`

## What `bootstrap.sh` provisions

Running the script against the project ID above (idempotent — safe to re-run):

- Creates the GCP project (if it doesn't already exist) and links it to the given billing account
- Enables the required APIs: Cloud Run, Cloud Build, Cloud Deploy, Artifact Registry, Secret Manager, Compute Engine, IAP, Firebase, Identity Toolkit, Firebase App Hosting
- Creates an Artifact Registry Docker repo named `api` in `us-central1`
- Creates two Secret Manager secrets:
  - `db-password` — auto-generated random value (24 bytes, base64)
  - `tmdb-api-key` — placeholder value `REPLACE_ME` (see manual step below)
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

## Manual steps checklist

- [ ] **Replace the placeholder TMDB API key** with the real value:

  ```bash
  printf '%s' 'YOUR_REAL_TMDB_KEY' | gcloud secrets versions add tmdb-api-key --data-file=- --project lineup-app-ae6b
  ```

- [ ] Firebase console configuration (Firebase Auth / Identity Platform setup, App Hosting backend wiring) — tracked as part of Task 6, not covered by this script.

## Security note

This is a public repository. The GCP billing account ID must never be committed to any tracked file — only the project ID is safe to commit. When running `bootstrap.sh` locally or in CI, pass the billing account ID as a command-line argument (or via a secret store), never hardcode it here.
