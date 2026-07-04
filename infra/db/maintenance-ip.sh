#!/usr/bin/env bash
# Temporarily give lineup-db outbound internet access for maintenance
# (e.g. apt upgrades), then remove it again.
#
# Originally specced as `gcloud compute instances add-access-config` /
# `delete-access-config` (attach/detach an ephemeral external IP), but the
# org policy `constraints/compute.vmExternalIpAccess` denies external IPs on
# VMs in this organization, so that mechanism cannot work here. Instead this
# attaches/detaches a TEMPORARY Cloud Router + Cloud NAT — same purpose
# (temporary egress for apt), and strictly tighter security: the VM never
# gets a publicly-addressable IP at all, and there is zero standing cost
# once detached.
#
# Usage:
#   infra/db/maintenance-ip.sh attach [PROJECT_ID]
#   infra/db/maintenance-ip.sh detach [PROJECT_ID]
set -euo pipefail

ACTION="${1:?usage: maintenance-ip.sh attach|detach [PROJECT_ID]}"
PROJECT_ID="${2:-lineup-app-ae6b}"
REGION=us-central1
# Maintenance-only NAT names, deliberately DISTINCT from create-vm.sh's
# bootstrap NAT (lineup-db-nat-bootstrap-*). create-vm.sh auto-deletes
# anything matching its own names as leftover bootstrap debris; keeping the
# maintenance NAT under different names guarantees a create-vm.sh re-run can
# never tear down an operator's egress mid-upgrade. This script only ever
# creates/deletes the -maint- pair.
ROUTER=lineup-db-nat-maint-router
NAT=lineup-db-nat-maint

log() { echo "==> $*"; }

case "$ACTION" in
  attach)
    if gcloud compute routers describe "$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1; then
      log "router $ROUTER already exists"
    else
      log "creating Cloud Router $ROUTER"
      gcloud compute routers create "$ROUTER" --region="$REGION" --network=default --project "$PROJECT_ID"
    fi
    if gcloud compute routers nats describe "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1; then
      log "NAT $NAT already exists"
    else
      log "creating Cloud NAT $NAT"
      gcloud compute routers nats create "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" \
        --auto-allocate-nat-external-ips --nat-all-subnet-ip-ranges
    fi
    log "outbound internet egress is ON for the default VPC ($REGION)"
    log "remember to run: $0 detach $PROJECT_ID   when maintenance is done"
    ;;
  detach)
    if gcloud compute routers nats describe "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1; then
      log "deleting Cloud NAT $NAT"
      gcloud compute routers nats delete "$NAT" --router="$ROUTER" --region="$REGION" --project "$PROJECT_ID" -q
    else
      log "NAT $NAT not present, nothing to do"
    fi
    if gcloud compute routers describe "$ROUTER" --region="$REGION" --project "$PROJECT_ID" >/dev/null 2>&1; then
      log "deleting Cloud Router $ROUTER"
      gcloud compute routers delete "$ROUTER" --region="$REGION" --project "$PROJECT_ID" -q
    else
      log "router $ROUTER not present, nothing to do"
    fi
    log "outbound internet egress is OFF"
    ;;
  *)
    echo "usage: maintenance-ip.sh attach|detach [PROJECT_ID]" >&2
    exit 1
    ;;
esac
