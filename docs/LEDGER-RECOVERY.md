# Ledger recovery

The ledger contains authentication records, usage events, unresolved reservations,
project budgets and totals. Cache data is separate. Never replace ledger storage
with a cache snapshot or release unresolved reservations without provider evidence.

## Backup

Run from repository root against the intended Compose project:

```bash
sh scripts/ledger-backup.sh YOUR_COMPOSE_PROJECT .cache/ledger-backups
```

The backup command waits for successful RDB creation, copies it with mode `0600`,
and prints its SHA-256. Record the hash, time, project and source revision privately.
Copy backups to encrypted off-host storage with operator-defined retention. Preserve
the deployment pepper separately: restored key fingerprints need the same pepper.
Certificate and ACL files are configuration secrets, not RDB contents.

An RDB covers only its snapshot point. Ledger AOF uses `appendfsync always`, but an
RDB backup alone cannot recover later events. Choose backup frequency from the
acceptable loss window; reconcile provider charges before admitting paid work.

## Isolated restore drill

Prerequisites: Docker, Python 3, the pinned Compose ledger image already downloaded, `shasum`,
and a backup with a verified checksum. No provider credentials or API calls needed.
Run from repository root:

```bash
shasum -a 256 .cache/ledger-backups/YOUR_SNAPSHOT.rdb
sh scripts/ledger-restore-check.sh .cache/ledger-backups/YOUR_SNAPSHOT.rdb
```

Compare the first hash with your recorded backup hash. The drill copies the RDB
into a new uniquely named volume and container. It publishes no ports, has no
network, and uses a private temporary credential directory beneath ignored `.cache/`.
It loads the RDB, fingerprints all auth/ledger hash fields, reports record counts,
enables AOF, writes a drill-only canary and verifies both records and canary after
restarting the container. It checks the input checksum remains unchanged, then
removes only its own container, volume and temporary directory.

Success proves snapshot readability and isolated AOF persistence. Zero counts mean
that category was absent; they do not prove recovery of records that never existed.
The fingerprint verifies preservation, not agreement with provider invoices or
correctness of historical totals. Save the command output privately with the hash.
An interrupted cleanup can leave resources labelled `llm-gateway.restore-check`;
inspect their names and labels before removing them. Never target live ledger volumes.

## Recover service

1. Stop gateway admission and preserve the failed ledger volume, its AOF files,
   backups and logs. Do not overwrite the only copy of any recovery material.
2. Verify a backup checksum and complete the isolated drill. Select the recovery
   point explicitly; account for provider work after that snapshot.
3. Prepare a **new** ledger volume. With Redis stopped, copy the verified RDB into
   its configured `/data/dump.rdb`. Start Redis without AOF for the initial RDB
   load; enable AOF after loading and verify a restart before restoring admission.
   An existing AOF takes precedence over RDB: never mix it into this fresh volume.
4. Restore the original pepper, deploy current TLS/ACL secrets, and verify key
   expiry/revocation, project identity, event totals and pending reservations.
   Use ledger operator audit/reconciliation commands; preserve an audit trail.
5. Reconcile uncertain provider outcomes and snapshot-window charges. Keep pending
   reservations charged until evidence resolves them. Confirm ledger readiness,
   then switch the deployment to the recovered volume and reopen admission.

A restore drill does not perform live recovery, failover, billing reconciliation,
off-host backup storage or a production durability guarantee. Operators own those
steps and the agreed recovery time/loss objectives.
