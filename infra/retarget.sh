#!/usr/bin/env bash
# Retarget the infra files at a new GCP project id.
#
# Usage: infra/retarget.sh NEW_PROJECT_ID
#
# The current project id is detected automatically from clouddeploy.yaml (the
# `run.location: projects/<id>/...` line is the single source of truth), then every
# literal occurrence of it is rewritten in the files that carry it:
#
#   - infra/clouddeploy.yaml   (target run.location)
#   - infra/run-service.yaml   (serviceAccountName, FIREBASE_PROJECT_ID)
#   - infra/README.md          (documented project id + example commands)
#   - infra/create-trigger.sh  (the PROJECT_ID default)
#
# infra/cloudbuild.yaml uses Cloud Build's native $PROJECT_ID substitution and
# needs no rewriting. Idempotent: re-running with the already-targeted id is a
# no-op. Makes no cloud calls.
set -euo pipefail

NEW_ID="${1:?usage: retarget.sh NEW_PROJECT_ID}"
INFRA_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FILES=(clouddeploy.yaml run-service.yaml README.md create-trigger.sh)

if ! [[ "$NEW_ID" =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]]; then
  echo "ERROR: '$NEW_ID' is not a valid GCP project id (6-30 chars, lowercase" >&2
  echo "letters/digits/hyphens, starts with a letter, doesn't end with a hyphen)." >&2
  exit 1
fi

CURRENT_ID="$(sed -n 's|.*location: projects/\([^/]*\)/locations/.*|\1|p' \
  "$INFRA_DIR/clouddeploy.yaml" | head -1)"
if [[ -z "$CURRENT_ID" ]]; then
  echo "ERROR: could not detect the current project id from" >&2
  echo "$INFRA_DIR/clouddeploy.yaml (no 'location: projects/<id>/locations/...' line" >&2
  echo "under the Target's run: block)." >&2
  exit 1
fi

if [[ "$CURRENT_ID" == "$NEW_ID" ]]; then
  echo "Already targeted at '$NEW_ID'; nothing to do."
  exit 0
fi

echo "Retargeting: $CURRENT_ID -> $NEW_ID"
for f in "${FILES[@]}"; do
  path="$INFRA_DIR/$f"
  n="$(grep -c "$CURRENT_ID" "$path" || true)"
  # sed -i.bak is portable across BSD (macOS) and GNU sed
  sed -i.bak "s/${CURRENT_ID}/${NEW_ID}/g" "$path" && rm -f "${path}.bak"
  echo "  $f: $n line(s) rewritten"
done

leftovers="$(grep -rn "$CURRENT_ID" "$INFRA_DIR" --include='*.yaml' --include='*.sh' --include='*.md' || true)"
if [[ -n "$leftovers" ]]; then
  echo ""
  echo "WARN: occurrences of '$CURRENT_ID' remain in infra/ (review manually):"
  echo "$leftovers"
fi

echo "Done. Review with: git diff -- infra/"
