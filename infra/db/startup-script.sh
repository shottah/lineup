#!/usr/bin/env bash
# GCE startup-script for lineup-db. Runs as root on every boot (GCE re-runs
# startup-script on each reboot, not just first boot), so every phase below is
# idempotent: on first boot (with temporary Cloud NAT egress for apt access,
# see create-vm.sh) it installs and configures everything; on later boots
# (normally with NO internet egress at all) the network-dependent install
# blocks are skipped entirely via "already installed" guards, and only the
# local/no-egress steps re-run.
#
# NOTE: fetching the db secret and pushing backups both talk to Google APIs
# (secretmanager.googleapis.com / storage.googleapis.com), which stay
# reachable without internet egress because create-vm.sh enables Private
# Google Access on the subnet. Only *third-party* apt repos (PGDG, Google
# Cloud CLI) require the temporary NAT, and only on first boot.
set -euo pipefail

SENTINEL=/var/lib/lineup-db-ready
log() { echo "[lineup-db-startup] $*"; }

log "starting"

# --- 1. PostgreSQL 16 from the PGDG apt repo (network required; first boot only) ---
if ! dpkg -s postgresql-16 >/dev/null 2>&1; then
  log "installing postgresql-16 from PGDG"
  apt-get update -y
  apt-get install -y curl ca-certificates gnupg lsb-release

  install -d -m 0755 /usr/share/postgresql-common/pgdg
  curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc \
    -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
  echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] https://apt.postgresql.org/pub/repos/apt $(lsb_release -cs)-pgdg main" \
    > /etc/apt/sources.list.d/pgdg.list

  apt-get update -y
  apt-get install -y postgresql-16
else
  log "postgresql-16 already installed, skipping PGDG install"
fi

# --- 2. Google Cloud CLI (network required; first boot only) -----------------------
# Debian's GCE image does not ship the full gcloud CLI; backup.sh needs
# `gcloud storage cp` and this script needs `gcloud secrets versions access`.
if ! command -v gcloud >/dev/null 2>&1; then
  log "installing google-cloud-cli"
  apt-get install -y apt-transport-https ca-certificates gnupg curl

  install -d -m 0755 /usr/share/keyrings
  curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg \
    | gpg --dearmor -o /usr/share/keyrings/cloud.google.gpg
  echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" \
    > /etc/apt/sources.list.d/google-cloud-sdk.list

  apt-get update -y
  apt-get install -y google-cloud-cli
else
  log "gcloud already installed, skipping"
fi

# --- 3. postgresql.conf / pg_hba.conf (idempotent, no network needed) ---------------
PG_CONF=/etc/postgresql/16/main/postgresql.conf
PG_HBA=/etc/postgresql/16/main/pg_hba.conf
CHANGED=false

if ! grep -qE "^listen_addresses\s*=\s*'\*'" "$PG_CONF"; then
  log "setting listen_addresses='*'"
  sed -i "s/^#*listen_addresses.*/listen_addresses = '*'/" "$PG_CONF"
  CHANGED=true
fi

HBA_LINE="host lineup lineup 10.128.0.0/9 scram-sha-256"
if ! grep -qF "$HBA_LINE" "$PG_HBA"; then
  log "appending pg_hba.conf rule for lineup"
  echo "$HBA_LINE" >> "$PG_HBA"
  CHANGED=true
fi

systemctl enable postgresql >/dev/null 2>&1 || true
if [[ "$CHANGED" == true ]]; then
  log "restarting postgresql (config changed)"
  systemctl restart postgresql
elif ! systemctl is-active --quiet postgresql; then
  log "starting postgresql (not active)"
  systemctl start postgresql
fi

# Wait (bounded) for Postgres to accept local connections after a restart/start.
for _ in $(seq 1 30); do
  sudo -u postgres pg_isready -q && break
  sleep 1
done

# --- 4. Fetch the db password and create/refresh role + database -------------------
# The password is only ever held in a shell variable and piped to psql over
# stdin — never written to disk, never a psql -c argument (which would be
# visible in `ps`/argv; printf is a shell builtin so it has no argv either),
# and never echoed/logged. The secret is base64 text, so it contains no
# single quotes and is safe to embed in a SQL string literal.
DB_PASSWORD="$(gcloud secrets versions access latest --secret=db-password)"

ROLE_EXISTS=$(sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='lineup'")

if [[ "$ROLE_EXISTS" == "1" ]]; then
  log "role lineup exists, refreshing password"
  printf "ALTER ROLE lineup WITH PASSWORD '%s';\n" "$DB_PASSWORD" \
    | sudo -u postgres psql -v ON_ERROR_STOP=1 >/dev/null
else
  log "creating role lineup"
  printf "CREATE ROLE lineup LOGIN PASSWORD '%s';\n" "$DB_PASSWORD" \
    | sudo -u postgres psql -v ON_ERROR_STOP=1 >/dev/null
fi
unset DB_PASSWORD

DB_EXISTS=$(sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='lineup'")
if [[ "$DB_EXISTS" != "1" ]]; then
  log "creating database lineup"
  sudo -u postgres psql -v ON_ERROR_STOP=1 -c "CREATE DATABASE lineup OWNER lineup;"
else
  log "database lineup already exists"
fi

# --- 5. Install backup.sh + nightly cron (idempotent, no network needed) -----------
# backup.sh's source of truth is infra/db/backup.sh in the repo; create-vm.sh
# uploads it as the `backup-script` instance-metadata key so there is exactly
# one copy of the logic, fetched here via the (always-reachable, link-local)
# metadata server.
curl -s -H 'Metadata-Flavor: Google' \
  'http://metadata.google.internal/computeMetadata/v1/instance/attributes/backup-script' \
  -o /usr/local/bin/lineup-db-backup.sh
chmod 0755 /usr/local/bin/lineup-db-backup.sh

cat > /etc/cron.d/lineup-db-backup <<'EOF'
0 3 * * * root /usr/local/bin/lineup-db-backup.sh >> /var/log/lineup-db-backup.log 2>&1
EOF
chmod 0644 /etc/cron.d/lineup-db-backup

# --- 6. Sentinel: signals create-vm.sh that provisioning is complete ----------------
mkdir -p "$(dirname "$SENTINEL")"
date -u --iso-8601=seconds > "$SENTINEL"

log "done"
