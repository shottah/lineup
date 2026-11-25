#!/usr/bin/env bash
# Store the TMDB v4 "API Read Access Token" in Secret Manager as
# `tmdb-read-token`, the secret the Cloud Run service mounts as TMDB_READ_TOKEN.
#
# The token never appears in argv, shell history, or logs: it is read from a
# hidden prompt (or stdin when piped) and handed to gcloud over a pipe.
#
# Usage:
#   bash infra/set-tmdb-token.sh                     # hidden prompt
#   bash infra/set-tmdb-token.sh < ~/.lineup/tmdb    # from a file
#
# Safe to re-run: each run adds a new secret version and disables the older
# ones, so only the latest version is usable. Cloud Run reads `latest` when a
# new revision starts; redeploy (or wait for the next deploy) to pick it up.
set -euo pipefail

PROJECT_ID="${PROJECT_ID:-lineup-app-ae6b}"
SECRET="tmdb-read-token"

if [[ -t 0 ]]; then
  read -rsp "TMDB API Read Access Token (v4): " TOKEN
  echo
else
  IFS= read -r TOKEN || true
fi

if [[ -z "${TOKEN}" || "${TOKEN}" == "REPLACE_ME" ]]; then
  echo "ERROR: empty or placeholder token; nothing stored." >&2
  exit 1
fi
# v4 read tokens are JWTs. A v3 "API Key" (32 hex chars) will not work with
# the Bearer auth the API uses, so refuse it rather than store a dud.
if [[ "${TOKEN}" != eyJ* ]]; then
  echo "ERROR: that doesn't look like a v4 read access token (expected a JWT" >&2
  echo "starting with 'eyJ'). Use 'API Read Access Token' from" >&2
  echo "https://www.themoviedb.org/settings/api, not the v3 API Key." >&2
  exit 1
fi

if ! gcloud secrets describe "$SECRET" --project "$PROJECT_ID" >/dev/null 2>&1; then
  gcloud secrets create "$SECRET" --replication-policy=automatic \
    --project "$PROJECT_ID" >/dev/null
  echo "Created secret $SECRET."
fi

NEW_VERSION="$(printf '%s' "$TOKEN" | gcloud secrets versions add "$SECRET" \
  --data-file=- --project "$PROJECT_ID" --format='value(name)')"
unset TOKEN
NEW_VERSION="${NEW_VERSION##*/}"
echo "Added $SECRET version $NEW_VERSION."

for v in $(gcloud secrets versions list "$SECRET" --project "$PROJECT_ID" \
    --filter='state=ENABLED' --format='value(name)'); do
  v="${v##*/}"
  if [[ "$v" != "$NEW_VERSION" ]]; then
    gcloud secrets versions disable "$v" --secret "$SECRET" \
      --project "$PROJECT_ID" -q >/dev/null
    echo "Disabled older version $v."
  fi
done

echo "Done. api-runtime reads it via its project-level secretAccessor role."
