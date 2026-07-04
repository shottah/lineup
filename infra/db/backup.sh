#!/usr/bin/env bash
# Nightly logical backup of the `lineup` database to Cloud Storage.
#
# Installed on the VM at /usr/local/bin/lineup-db-backup.sh by startup-script.sh
# (copied in verbatim from GCE instance metadata, see the `backup-script` key),
# and invoked nightly at 03:00 UTC by /etc/cron.d/lineup-db-backup running as root.
#
# No password handling here: pg_dump runs as the local `postgres` OS/DB
# superuser over the Unix socket (peer auth), and `gcloud storage cp` uses the
# VM's attached service account (api-runtime@) via metadata-based ADC — no
# credentials are read, printed, or passed on the command line.
set -euo pipefail

PROJECT_ID="$(curl -s -H 'Metadata-Flavor: Google' \
  'http://metadata.google.internal/computeMetadata/v1/project/project-id')"
BUCKET="gs://${PROJECT_ID}-db-backups"
DATE="$(date -u +%F)"
OBJECT="${BUCKET}/lineup-${DATE}.dump"

echo "[lineup-db-backup] $(date -u --iso-8601=seconds) starting dump -> ${OBJECT}"

# Two-step dump-then-upload: dumping straight into `gcloud storage cp -` would
# publish a truncated object at the canonical name if pg_dump died mid-stream
# (the partial upload lands before pipefail kills the script). Instead, dump
# to a local temp file first — set -e aborts here if pg_dump fails, so nothing
# is ever uploaded — and only upload a dump that completed successfully.
TMP_DUMP="$(mktemp /var/tmp/lineup-backup-XXXXXX.dump)"
trap 'rm -f "$TMP_DUMP"' EXIT

sudo -u postgres pg_dump -Fc lineup > "$TMP_DUMP"
gcloud storage cp "$TMP_DUMP" "${OBJECT}"

echo "[lineup-db-backup] $(date -u --iso-8601=seconds) done: ${OBJECT}"
