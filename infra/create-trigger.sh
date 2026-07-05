#!/usr/bin/env bash
# Idempotent setup for the Cloud Build -> Artifact Registry -> Cloud Deploy -> Cloud Run
# pipeline: IAM grants for the build/deploy chain, the `database-url` secret, and the
# GitHub trigger (via a 2nd-gen Cloud Build GitHub connection).
#
# Ordering: infra/bootstrap.sh and infra/db/create-vm.sh must have run first (this
# script reads the db-password secret and the lineup-db VM's internal IP).
#
# Safe to re-run: every step checks current state before mutating anything.
set -euo pipefail

PROJECT_ID="${PROJECT_ID:-lineup-app-ae6b}"
REGION="${REGION:-us-central1}"
ZONE="${ZONE:-us-central1-a}"
CONNECTION_NAME="${CONNECTION_NAME:-lineup-github}"
REPO_ID="${REPO_ID:-lineup}"
REPO_REMOTE_URI="${REPO_REMOTE_URI:-https://github.com/shottah/lineup.git}"
TRIGGER_NAME="${TRIGGER_NAME:-api-deploy}"

PROJECT_NUMBER="$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')"
CLOUDBUILD_P4SA="service-${PROJECT_NUMBER}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
COMPUTE_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
RUNTIME_SA="api-runtime@${PROJECT_ID}.iam.gserviceaccount.com"

echo "== 1. Cloud Deploy service agent =="
# clouddeploy.googleapis.com's per-product service agent isn't created until first
# use; ensure it exists so Cloud Deploy can run render/deploy jobs. No-op if it
# already exists.
gcloud beta services identity create --service=clouddeploy.googleapis.com \
  --project="$PROJECT_ID" >/dev/null

echo "== 2. IAM grants: build/deploy chain =="
# Two principals matter, and they may or may not be the same account:
#
#   BUILD_SA : whatever Cloud Build runs builds as by default. On newer projects
#              that is the default Compute Engine SA, on older ones the legacy
#              <num>@cloudbuild.gserviceaccount.com. Detect it instead of assuming.
#   EXEC_SA  : the Cloud Deploy execution SA. infra/clouddeploy.yaml's `prod`
#              Target sets no custom executionConfigs, so this is always the
#              default Compute Engine SA.
BUILD_SA="$(gcloud builds get-default-service-account --project="$PROJECT_ID" \
  --format='value(serviceAccountEmail)' 2>/dev/null || true)"
BUILD_SA="${BUILD_SA##*/}"   # strip a projects/.../serviceAccounts/ prefix if present
if [[ "$BUILD_SA" != *@* ]]; then
  echo "WARN: could not detect the default Cloud Build service account;" \
       "falling back to the default compute SA (${COMPUTE_SA})" >&2
  BUILD_SA="$COMPUTE_SA"
fi
EXEC_SA="$COMPUTE_SA"
echo "build SA:            $BUILD_SA"
echo "deploy execution SA: $EXEC_SA"

# BUILD_SA runs infra/cloudbuild.yaml:
#   - cloudbuild.builds.builder : staged-source access, build logs, workspace
#                                 bucket (no-op if BUILD_SA is the legacy Cloud
#                                 Build SA, which has it by default)
#   - artifactregistry.writer   : push us-central1-docker.pkg.dev/<project>/api
#   - clouddeploy.releaser      : `gcloud deploy releases create` (last build step)
for role in roles/cloudbuild.builds.builder roles/artifactregistry.writer \
            roles/clouddeploy.releaser; do
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:${BUILD_SA}" --role="$role" -q >/dev/null
done

# EXEC_SA runs the Cloud Deploy render/deploy jobs:
#   - clouddeploy.jobRunner : artifact bucket access + job logs
#   - run.admin             : create/update the lineup-api Cloud Run service. The
#                             plan named run.developer, but deploying a manifest
#                             carrying run.googleapis.com/invoker-iam-disabled
#                             requires run.services.setIamPolicy (Cloud Run rejects
#                             the deploy otherwise: "Changes to invoker_iam_disabled
#                             require run.services.setIamPolicy permissions"), and
#                             only run.admin carries it.
for role in roles/clouddeploy.jobRunner roles/run.admin; do
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:${EXEC_SA}" --role="$role" -q >/dev/null
done

# actAs chain:
#   - creating a release requires the caller (BUILD_SA) to actAs the target's
#     execution SA (even when they are the same account -- self-actAs is still an
#     IAM check);
#   - deploying a revision that runs as api-runtime requires EXEC_SA to actAs
#     api-runtime.
gcloud iam service-accounts add-iam-policy-binding "$EXEC_SA" \
  --member="serviceAccount:${BUILD_SA}" --role=roles/iam.serviceAccountUser \
  --project="$PROJECT_ID" -q >/dev/null
gcloud iam service-accounts add-iam-policy-binding "$RUNTIME_SA" \
  --member="serviceAccount:${EXEC_SA}" --role=roles/iam.serviceAccountUser \
  --project="$PROJECT_ID" -q >/dev/null

echo "== 3. database-url secret =="
# Built from db-password (created by infra/bootstrap.sh) + the lineup-db VM's
# internal IP (resolved live, never hardcoded). The password is never printed: it
# flows from `gcloud secrets versions access` straight into `printf` and then into
# `gcloud secrets create --data-file=-` over a pipe, so it never appears in argv,
# logs, or shell history.
if ! gcloud secrets describe database-url --project="$PROJECT_ID" >/dev/null 2>&1; then
  DB_IP="$(gcloud compute instances describe lineup-db --zone "$ZONE" \
    --project="$PROJECT_ID" --format='value(networkInterfaces[0].networkIP)' 2>/dev/null || true)"
  if [[ -z "$DB_IP" ]]; then
    echo "ERROR: could not resolve the internal IP of VM 'lineup-db' (zone $ZONE," >&2
    echo "project $PROJECT_ID). The database VM must exist before the database-url" >&2
    echo "secret can be built: run infra/db/create-vm.sh first, then re-run this" >&2
    echo "script." >&2
    exit 1
  fi
  printf 'postgres://lineup:%s@%s:5432/lineup?sslmode=disable' \
    "$(gcloud secrets versions access latest --secret=db-password --project="$PROJECT_ID")" \
    "$DB_IP" | \
    gcloud secrets create database-url --data-file=- --project="$PROJECT_ID"
fi
# api-runtime already has project-level secretmanager.secretAccessor (bootstrap.sh),
# which covers this new secret too.

echo "== 4. GitHub connection (2nd-gen) =="
# Creating a fresh connection requires the Cloud Build P4SA to manage a Secret
# Manager secret holding the OAuth token; grant that once, up front. Revoke it
# after the connection reaches installationState COMPLETE -- see the manual-steps
# checklist in infra/README.md.
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:${CLOUDBUILD_P4SA}" --role=roles/secretmanager.admin -q >/dev/null

if ! gcloud builds connections describe "$CONNECTION_NAME" --region="$REGION" \
    --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud builds connections create github "$CONNECTION_NAME" --region="$REGION" \
    --project="$PROJECT_ID" -q
fi

CONN_STAGE="$(gcloud builds connections describe "$CONNECTION_NAME" --region="$REGION" \
  --project="$PROJECT_ID" --format='value(installationState.stage)')"

if [[ "$CONN_STAGE" != "COMPLETE" ]]; then
  ACTION_URI="$(gcloud builds connections describe "$CONNECTION_NAME" --region="$REGION" \
    --project="$PROJECT_ID" --format='value(installationState.actionUri)')"
  echo ""
  echo "Connection '$CONNECTION_NAME' is not yet usable (state: $CONN_STAGE)."
  echo "A human with admin access to github.com/shottah/lineup must complete the"
  echo "one-time OAuth + GitHub App install in a browser, then re-run this script."
  echo ""
  if [[ -n "$ACTION_URI" ]]; then
    echo "  $ACTION_URI"
  else
    echo "No action URL is available from the API right now; open the Cloud Build"
    echo "console (Repositories > 2nd gen) and finish the installation of"
    echo "connection '$CONNECTION_NAME' (project $PROJECT_ID, region $REGION)."
  fi
  echo ""
  echo "Skipping repository + trigger creation until the connection is COMPLETE."
  exit 0
fi

echo "== 5. Repository registration =="
if ! gcloud builds repositories describe "$REPO_ID" --connection="$CONNECTION_NAME" \
    --region="$REGION" --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud builds repositories create "$REPO_ID" \
    --remote-uri="$REPO_REMOTE_URI" --connection="$CONNECTION_NAME" \
    --region="$REGION" --project="$PROJECT_ID" -q
fi

echo "== 6. Build trigger =="
if ! gcloud builds triggers describe "$TRIGGER_NAME" --region="$REGION" \
    --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud builds triggers create github \
    --name="$TRIGGER_NAME" \
    --region="$REGION" \
    --repository="projects/${PROJECT_ID}/locations/${REGION}/connections/${CONNECTION_NAME}/repositories/${REPO_ID}" \
    --branch-pattern='^main$' \
    --included-files='api/**' \
    --build-config=infra/cloudbuild.yaml \
    --project="$PROJECT_ID" -q
fi

echo "Trigger setup complete: $TRIGGER_NAME (project=$PROJECT_ID region=$REGION)"
