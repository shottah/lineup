#!/usr/bin/env bash
# Provisions the lineup-db VM: e2-micro, Debian 12, Postgres 16, no external IP.
#
# Idempotent: safe to re-run against an already-provisioned project. Every
# resource-creation step first checks for existence and skips if present, same
# pattern as infra/bootstrap.sh.
#
# Bootstrap-egress note (see infra/db/README.md for the full rationale): the
# org policy `constraints/compute.vmExternalIpAccess` denies external IPs on
# VMs in this org, and this VPC has no permanent Cloud NAT (by design — zero
# standing cost). A VM with neither can't reach apt repos on first boot. So
# this script creates a TEMPORARY Cloud Router + Cloud NAT, creates the VM
# with --no-address, polls over IAP SSH for a sentinel file the startup
# script writes when done, then deletes the NAT + router. Final state: no
# external IP (never had one), no NAT, reachable only via IAP.
set -euo pipefail

PROJECT_ID="${1:-lineup-app-ae6b}"
REGION=us-central1
ZONE=us-central1-a
VM_NAME=lineup-db
SA_EMAIL="api-runtime@${PROJECT_ID}.iam.gserviceaccount.com"
BUCKET="gs://${PROJECT_ID}-db-backups"
SENTINEL=/var/lib/lineup-db-ready
# Bootstrap-only NAT names, deliberately DISTINCT from maintenance-ip.sh's
# (lineup-db-nat-maint-*). This script deletes any NAT/router matching its
# own names on the assumption they are leftover bootstrap debris, so it must
# never share names with the operator's maintenance NAT — otherwise a re-run
# during a maintenance window would kill egress mid-upgrade.
ROUTER=lineup-db-nat-bootstrap-router
NAT=lineup-db-nat-bootstrap
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

log() { echo "==> $*"; }

log "project=$PROJECT_ID zone=$ZONE vm=$VM_NAME bucket=$BUCKET"

# --- 1. Backup bucket + 14-day lifecycle -------------------------------------------
if ! gcloud storage buckets describe "$BUCKET" --project "$PROJECT_ID" >/dev/null 2>&1; then
  log "creating backup bucket $BUCKET"
  gcloud storage buckets create "$BUCKET" \
    --project "$PROJECT_ID" --location="$REGION" --uniform-bucket-level-access
else
  log "backup bucket $BUCKET already exists"
fi
gcloud storage buckets update "$BUCKET" \
  --project "$PROJECT_ID" --lifecycle-file="$SCRIPT_DIR/backup-lifecycle.json" >/dev/null

# --- 2. Grant the VM's service account object admin on the bucket ONLY ------------
# (bucket-level binding, not a project-level role grant)
gcloud storage buckets add-iam-policy-binding "$BUCKET" \
  --project "$PROJECT_ID" \
  --member="serviceAccount:${SA_EMAIL}" --role="roles/storage.objectAdmin" >/dev/null

# --- 3. Firewall rules --------------------------------------------------------------
gcloud compute firewall-rules describe allow-pg-internal --project "$PROJECT_ID" >/dev/null 2>&1 || {
  log "creating firewall rule allow-pg-internal"
  gcloud compute firewall-rules create allow-pg-internal \
    --project "$PROJECT_ID" --network=default --direction=INGRESS --action=ALLOW \
    --rules=tcp:5432 --source-ranges=10.128.0.0/9
}

gcloud compute firewall-rules describe allow-iap-ssh --project "$PROJECT_ID" >/dev/null 2>&1 || {
  log "creating firewall rule allow-iap-ssh"
  gcloud compute firewall-rules create allow-iap-ssh \
    --project "$PROJECT_ID" --network=default --direction=INGRESS --action=ALLOW \
    --rules=tcp:22 --source-ranges=35.235.240.0/20
}

# --- 4. Private Google Access on the default subnet ---------------------------------
# Required so the VM (which never has an external IP) can reach
# secretmanager/storage APIs once the temporary bootstrap NAT is deleted.
PGA="$(gcloud compute networks subnets describe default --region="$REGION" \
  --project "$PROJECT_ID" --format='value(privateIpGoogleAccess)')"
if [[ "$PGA" != "True" ]]; then
  log "enabling Private Google Access on default subnet ($REGION)"
  gcloud compute networks subnets update default --region="$REGION" \
    --project "$PROJECT_ID" --enable-private-ip-google-access
else
  log "Private Google Access already enabled on default subnet"
fi

# --- 5. Create the VM (temporary Cloud NAT for first-boot apt access) --------------
NEED_BOOTSTRAP=false
if ! gcloud compute instances describe "$VM_NAME" --zone="$ZONE" --project "$PROJECT_ID" >/dev/null 2>&1; then
  NEED_BOOTSTRAP=true

  log "creating temporary Cloud Router + NAT for bootstrap egress"
  gcloud compute routers describe "$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1 || \
    gcloud compute routers create "$ROUTER" --region="$REGION" --network=default --project "$PROJECT_ID"
  gcloud compute routers nats describe "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1 || \
    gcloud compute routers nats create "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" \
      --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges

  log "creating instance $VM_NAME (no external IP)"
  gcloud compute instances create "$VM_NAME" \
    --project "$PROJECT_ID" --zone="$ZONE" --machine-type=e2-micro \
    --image-family=debian-12 --image-project=debian-cloud \
    --boot-disk-size=30GB --boot-disk-type=pd-standard --no-address \
    --service-account="$SA_EMAIL" --scopes=cloud-platform \
    --metadata-from-file=startup-script="$SCRIPT_DIR/startup-script.sh",backup-script="$SCRIPT_DIR/backup.sh"
else
  log "instance $VM_NAME already exists, skipping create"
  # If a previous run was interrupted mid-bootstrap, its NAT may still exist —
  # detect that so we still wait for the sentinel and clean up below.
  if gcloud compute routers describe "$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1; then
    log "leftover bootstrap NAT found; will wait for sentinel and clean it up"
    NEED_BOOTSTRAP=true
  fi
fi

# --- 6. Wait for the startup script to finish (poll sentinel over IAP SSH) ---------
if [[ "$NEED_BOOTSTRAP" == true ]]; then
  log "waiting for startup script to report ready ($SENTINEL)..."
  ATTEMPTS=40
  SLEEP_SECS=15
  ready=false
  for ((i = 1; i <= ATTEMPTS; i++)); do
    if gcloud compute ssh "$VM_NAME" --zone="$ZONE" --project "$PROJECT_ID" --tunnel-through-iap \
      --command="test -f $SENTINEL" >/dev/null 2>&1; then
      ready=true
      break
    fi
    log "  [$i/$ATTEMPTS] not ready yet, retrying in ${SLEEP_SECS}s"
    sleep "$SLEEP_SECS"
  done

  if [[ "$ready" != true ]]; then
    echo "ERROR: startup script did not report ready within $((ATTEMPTS * SLEEP_SECS))s." >&2
    echo "Inspect with:" >&2
    echo "  gcloud compute instances get-serial-port-output $VM_NAME --zone=$ZONE --project=$PROJECT_ID" >&2
    echo "The temporary NAT ($NAT/$ROUTER) was left in place so a re-run can resume." >&2
    exit 1
  fi
  log "startup script finished"

  # --- 7. Delete the temporary NAT + router: final state has no internet egress ----
  log "deleting temporary Cloud NAT + router"
  gcloud compute routers nats delete "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" -q
  gcloud compute routers delete "$ROUTER" --region="$REGION" --project "$PROJECT_ID" -q
fi

INTERNAL_IP="$(gcloud compute instances describe "$VM_NAME" --zone="$ZONE" --project "$PROJECT_ID" \
  --format='value(networkInterfaces[0].networkIP)')"

log "lineup-db internal IP: $INTERNAL_IP"
log "DATABASE_URL=postgres://lineup:<pw>@${INTERNAL_IP}:5432/lineup?sslmode=disable"
log "(fetch <pw> via: gcloud secrets versions access latest --secret=db-password --project $PROJECT_ID)"
