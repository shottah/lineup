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

sudo -u postgres pg_dump -Fc lineup | gcloud storage cp - "${OBJECT}"

echo "[lineup-db-backup] $(date -u --iso-8601=seconds) done: ${OBJECT}"
