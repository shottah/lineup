#!/usr/bin/env bash
set -euo pipefail
PROJECT_ID="${1:?usage: bootstrap.sh PROJECT_ID BILLING_ACCOUNT_ID}"
BILLING="${2:?usage: bootstrap.sh PROJECT_ID BILLING_ACCOUNT_ID}"
REGION=us-central1
gcloud projects describe "$PROJECT_ID" >/dev/null 2>&1 || gcloud projects create "$PROJECT_ID"
gcloud billing projects link "$PROJECT_ID" --billing-account="$BILLING"
gcloud services enable run.googleapis.com cloudbuild.googleapis.com clouddeploy.googleapis.com \
  artifactregistry.googleapis.com secretmanager.googleapis.com compute.googleapis.com \
  iap.googleapis.com firebase.googleapis.com identitytoolkit.googleapis.com \
  firebaseapphosting.googleapis.com --project "$PROJECT_ID"
gcloud artifacts repositories describe api --location=$REGION --project "$PROJECT_ID" >/dev/null 2>&1 || \
  gcloud artifacts repositories create api --repository-format=docker --location=$REGION --project "$PROJECT_ID"
gcloud secrets describe db-password --project "$PROJECT_ID" >/dev/null 2>&1 || \
  (openssl rand -base64 24 | tr -d '\n' | gcloud secrets create db-password --data-file=- --project "$PROJECT_ID")
gcloud secrets describe tmdb-api-key --project "$PROJECT_ID" >/dev/null 2>&1 || \
  (printf 'REPLACE_ME' | gcloud secrets create tmdb-api-key --data-file=- --project "$PROJECT_ID")
gcloud iam service-accounts describe api-runtime@"$PROJECT_ID".iam.gserviceaccount.com --project "$PROJECT_ID" >/dev/null 2>&1 || \
  gcloud iam service-accounts create api-runtime --project "$PROJECT_ID"
for role in roles/secretmanager.secretAccessor; do
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:api-runtime@$PROJECT_ID.iam.gserviceaccount.com" --role="$role" -q >/dev/null
done
echo "Bootstrap complete for $PROJECT_ID"
