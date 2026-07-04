# infra/db — Postgres 16 on the always-free e2-micro VM

Provisions `lineup-db`: a single `e2-micro` GCE VM (Debian 12) running
Postgres 16, reachable only from inside the VPC, backed up nightly to Cloud
Storage. This is the production database for the Cloud Run API (task 5),
reached over Direct VPC egress on the `default` VPC.

## Files

- `create-vm.sh` — provisions everything: backup bucket + lifecycle,
  firewall rules, Private Google Access, and the VM itself. Idempotent —
  safe to re-run.
- `startup-script.sh` — GCE metadata startup script. Installs Postgres 16
  (PGDG apt repo) and the Google Cloud CLI, configures `postgresql.conf` /
  `pg_hba.conf`, creates the `lineup` role + database, installs `backup.sh`
  and its cron job. Runs as root on every boot; every phase is guarded so
  reboots (normally with no external IP) don't fail.
- `backup.sh` — nightly `pg_dump` → `gs://$PROJECT_ID-db-backups`. Installed
  onto the VM by `startup-script.sh` (single source of truth lives here in
  the repo; the startup script fetches it via instance metadata rather than
  keeping a second copy).
- `maintenance-ip.sh` — `attach`/`detach` temporary outbound internet egress
  (a short-lived Cloud Router + Cloud NAT) for one-off maintenance
  (e.g. `apt upgrade`).
- `backup-lifecycle.json` — GCS lifecycle rule (delete objects older than 14
  days), applied to the backup bucket by `create-vm.sh`.

## Why bootstrap uses a temporary Cloud NAT

Two constraints combine here:

- The org policy `constraints/compute.vmExternalIpAccess` **denies external
  IPs on VMs** in this organization (effective policy: `denyAll`), so the
  VM can never get an ephemeral public address, even briefly.
- This VPC has no permanent Cloud NAT (by design — the free-tier setup
  avoids a NAT gateway's standing cost).

A VM with neither can't reach the public internet at all, including the apt
repos the first boot needs (PGDG Postgres packages + the Google Cloud CLI).
So `create-vm.sh`:

1. Creates a **temporary** Cloud Router + Cloud NAT
   (`lineup-db-nat-bootstrap-router` / `lineup-db-nat-bootstrap`) on the
   `default` network. These names are deliberately distinct from
   `maintenance-ip.sh`'s (`lineup-db-nat-maint-*`): create-vm.sh auto-deletes
   anything matching its own bootstrap names as leftover debris, so separate
   namespaces guarantee a create-vm.sh re-run can never tear down an
   operator's maintenance NAT mid-upgrade.
2. Creates the VM with `--no-address`.
3. Polls (`gcloud compute ssh ... --tunnel-through-iap --command=...`) for a
   sentinel file (`/var/lib/lineup-db-ready`) that `startup-script.sh` writes
   once Postgres, the role/database, and the backup cron job are all set up.
4. Deletes the NAT + router.

Final state: **no external IP (the VM never had one) and no NAT**. All
further access is via IAP SSH tunneling (`allow-iap-ssh` firewall rule,
source range `35.235.240.0/20`), and Postgres is reachable only from
`10.128.0.0/9` (`allow-pg-internal`).

`create-vm.sh` also enables **Private Google Access** on the `default`
subnet. Without it, once the NAT is gone the VM would have no route to
`secretmanager.googleapis.com` / `storage.googleapis.com` either, and
nightly backups (and any future secret refresh) would fail. Private Google
Access only covers Google API domains — it does *not* restore access to
third-party apt mirrors, which is why `startup-script.sh`'s apt-install
blocks are guarded to run only once, on first boot, while the NAT is still
up. On later boots those blocks detect the packages are already installed
and skip straight to the (network-free, or Google-API-only) steps.

If `create-vm.sh` fails partway through the sentinel wait, it deliberately
leaves the bootstrap NAT in place so a re-run can resume; the re-run cleans
it up once the sentinel appears. (`maintenance-ip.sh detach` does *not*
touch the bootstrap NAT — it only manages its own `-maint-` pair.)

## Running it

```bash
bash infra/db/create-vm.sh [PROJECT_ID]   # defaults to lineup-app-ae6b
```

Prints the VM's internal IP and the `DATABASE_URL` template at the end.
Re-running against an already-provisioned project is safe: every step is a
describe-or-create guard, and the VM-creation step is skipped entirely if
`lineup-db` already exists (in which case the NAT/sentinel-wait steps are
also skipped, unless a leftover bootstrap NAT from an interrupted run is
detected, in which case the run resumes the wait and cleans it up).

## How `DATABASE_URL` is composed

```
postgres://lineup:<pw>@<INTERNAL_IP>:5432/lineup?sslmode=disable
```

- `<INTERNAL_IP>` — the VM's internal IP:

  ```bash
  gcloud compute instances describe lineup-db --zone=us-central1-a \
    --project lineup-app-ae6b --format='value(networkInterfaces[0].networkIP)'
  ```

- `<pw>` — the `lineup` role's password, which is the literal contents of
  the `db-password` Secret Manager secret:

  ```bash
  gcloud secrets versions access latest --secret=db-password --project lineup-app-ae6b
  ```

Never paste the password into a shell history file, log, or committed
config — fetch it at deploy time (e.g. in the Cloud Run service's runtime
secret wiring for task 5) or on-VM when composing a `psql` connection
string interactively.

`sslmode=disable` is intentional here: the connection never leaves the
private VPC (`10.128.0.0/9`), so there's no on-path attacker to defend
against with TLS, and Postgres 16 on this VM is not configured with a TLS
certificate.

## Maintenance: installing OS/package updates

The VM has no external IP (org policy forbids one) and normally no NAT, so
`apt upgrade` etc. need temporary egress. `maintenance-ip.sh attach` stands
up a short-lived Cloud Router + Cloud NAT; `detach` tears it down:

```bash
bash infra/db/maintenance-ip.sh attach [PROJECT_ID]
gcloud compute ssh lineup-db --zone=us-central1-a --tunnel-through-iap --project lineup-app-ae6b
# on the VM: sudo apt-get update && sudo apt-get upgrade -y
exit
bash infra/db/maintenance-ip.sh detach [PROJECT_ID]
```

Always run `detach` when done — the final/normal state for this VM is no
internet egress at all (Cloud NAT bills hourly while it exists). While
attached, the VM still has no public inbound address; NAT is egress-only.

## Restore drill

Restoring a nightly dump onto a **fresh** VM (disaster-recovery scenario —
the existing `lineup-db` is gone or corrupted):

1. **Re-provision the VM.** Run `create-vm.sh` again — it creates a new
   `lineup-db` with Postgres 16, an empty `lineup` role/database, and the
   backup cron job, exactly as before. (If the old VM still exists and you
   just want to restore onto it — e.g. rolling back after a bad migration —
   skip this step and go straight to step 2; no other preparation is needed,
   the `--clean --if-exists` flags in step 4 handle the existing objects.)

2. **Get a shell on the VM** (IAP SSH; no external IP needed for this):

   ```bash
   gcloud compute ssh lineup-db --zone=us-central1-a --tunnel-through-iap \
     --project lineup-app-ae6b
   ```

3. **Pick a dump and copy it down** (from the VM):

   ```bash
   gcloud storage ls gs://lineup-app-ae6b-db-backups/
   gcloud storage cp gs://lineup-app-ae6b-db-backups/lineup-2026-07-03.dump /tmp/restore.dump
   ```

4. **Restore it.** The same command works for both scenarios — a freshly
   provisioned empty `lineup` database or an existing one being rolled back:

   ```bash
   sudo -u postgres pg_restore -d lineup --clean --if-exists --no-owner /tmp/restore.dump
   ```

   - `--clean --if-exists` drops each object before recreating it (and skips
     the drop harmlessly when the object doesn't exist yet), so no
     `dropdb`/`createdb` or any other preparation is ever needed.
   - `--no-owner` avoids failing on role-ownership statements if you ever
     restore into a database owned by a different role name.

5. **Verify:**

   ```bash
   sudo -u postgres psql -d lineup -c '\dt'
   sudo -u postgres psql -d lineup -c 'select count(*) from <some_table>;'
   ```

6. **Clean up the scratch file:** `rm /tmp/restore.dump`.

7. If you re-pointed the Cloud Run API (task 5) at a *new* VM's internal IP,
   update its `DATABASE_URL` runtime config accordingly (see above for how
   the internal IP and password are fetched).

## Security notes

- No password ever appears in a committed file, in `argv` (so it doesn't
  show up in `ps`/process listings), in a log line, or on disk —
  `startup-script.sh` holds it in a shell variable and pipes the SQL to
  `psql` over stdin.
- Final VM state has **no external IP**. SSH is IAP-tunnel-only
  (`allow-iap-ssh`, source `35.235.240.0/20`); Postgres is reachable only
  from `10.128.0.0/9` (`allow-pg-internal`) — never from the public internet.
- The VM's service account (`api-runtime@`) is granted
  `roles/storage.objectAdmin` on the backup bucket **only** (a bucket-level
  IAM binding), not at the project level.
