#!/usr/bin/env bash
# Idempotent setup for the Cloud Build -> Artifact Registry -> Cloud Deploy -> Cloud Run
# pipeline: IAM grants for the build/deploy chain, the `database-url` secret, and the
# GitHub trigger (via a 2nd-gen Cloud Build GitHub connection).
#
# Safe to re-run: every step checks current state before mutating anything.
set -euo pipefail

PROJECT_ID="${PROJECT_ID:-lineup-app-ae6b}"
REGION="${REGION:-us-central1}"
CONNECTION_NAME="${CONNECTION_NAME:-lineup-github}"
REPO_ID="${REPO_ID:-lineup}"
REPO_REMOTE_URI="${REPO_REMOTE_URI:-https://github.com/shottah/lineup.git}"
TRIGGER_NAME="${TRIGGER_NAME:-api-deploy}"

PROJECT_NUMBER="$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')"
CLOUDBUILD_SA="${PROJECT_NUMBER}@cloudbuild.gserviceaccount.com"
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
# Cloud Build's default SA builds+pushes the image (infra/cloudbuild.yaml docker
# steps) and then creates the Cloud Deploy release.
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:${CLOUDBUILD_SA}" --role=roles/artifactregistry.writer -q >/dev/null
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:${CLOUDBUILD_SA}" --role=roles/clouddeploy.releaser -q >/dev/null

# infra/clouddeploy.yaml's `prod` Target sets no custom executionConfigs, so Cloud
# Deploy renders/deploys using the project's default Compute Engine SA. That SA is
# the one that actually calls the Cloud Run API and must be able to run the
# resulting revision as api-runtime.
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:${COMPUTE_SA}" --role=roles/run.developer -q >/dev/null
gcloud iam service-accounts add-iam-policy-binding "$RUNTIME_SA" \
  --member="serviceAccount:${COMPUTE_SA}" --role=roles/iam.serviceAccountUser \
  --project="$PROJECT_ID" -q >/dev/null

echo "== 3. database-url secret =="
# Built from db-password (task 4's VM secret) + the lineup-db VM's internal IP.
# The password is never printed: it flows from `gcloud secrets versions access`
# straight into `printf` and then into `gcloud secrets create --data-file=-` over a
# pipe, so it never appears in argv, logs, or shell history.
if ! gcloud secrets describe database-url --project="$PROJECT_ID" >/dev/null 2>&1; then
  printf 'postgres://lineup:%s@10.128.0.2:5432/lineup?sslmode=disable' \
    "$(gcloud secrets versions access latest --secret=db-password --project="$PROJECT_ID")" | \
    gcloud secrets create database-url --data-file=- --project="$PROJECT_ID"
fi
# api-runtime already has project-level secretmanager.secretAccessor (task 3),
# which covers this new secret too.

echo "== 4. GitHub connection (2nd-gen) =="
# Creating a fresh connection requires the Cloud Build P4SA to manage a Secret
# Manager secret holding the OAuth token; grant that once, up front. (Can be
# revoked once the connection reaches installationState COMPLETE, per Google's
# docs on connect-repo-github.)
gcloud projects add-iam-policy-binding "$PROJECT_ID" \
  --member="serviceAccount:${CLOUDBUILD_P4SA}" --role=roles/secretmanager.admin -q >/dev/null

if ! gcloud builds connections describe "$CONNECTION_NAME" --region="$REGION" \
    --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud builds connections create github "$CONNECTION_NAME" --region="$REGION" \
    --project="$PROJECT_ID"
fi

CONN_STAGE="$(gcloud builds connections describe "$CONNECTION_NAME" --region="$REGION" \
  --project="$PROJECT_ID" --format='value(installationState.stage)')"

if [[ "$CONN_STAGE" != "COMPLETE" ]]; then
  ACTION_URI="$(gcloud builds connections describe "$CONNECTION_NAME" --region="$REGION" \
    --project="$PROJECT_ID" --format='value(installationState.actionUri)')"
  cat <<EOF

Connection '$CONNECTION_NAME' is not yet usable (state: $CONN_STAGE).
A human with admin access to github.com/shottah/lineup must complete this
one-time OAuth + GitHub App install in a browser, then re-run this script:

  $ACTION_URI

Skipping repository + trigger creation until the connection is COMPLETE.
EOF
  exit 0
fi

echo "== 5. Repository registration =="
if ! gcloud builds repositories describe "$REPO_ID" --connection="$CONNECTION_NAME" \
    --region="$REGION" --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud builds repositories create "$REPO_ID" \
    --remote-uri="$REPO_REMOTE_URI" --connection="$CONNECTION_NAME" \
    --region="$REGION" --project="$PROJECT_ID"
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
    --project="$PROJECT_ID"
fi

echo "Trigger setup complete: $TRIGGER_NAME (project=$PROJECT_ID region=$REGION)"
