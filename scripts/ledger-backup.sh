#!/bin/sh
set -eu
# Run from the repository root. Credentials remain inside the ledger container.
llmgw_project=${1:?usage: ledger-backup.sh COMPOSE_PROJECT BACKUP_DIRECTORY}
llmgw_backup_directory=${2:?backup directory required}
umask 077
mkdir -p "$llmgw_backup_directory"
llmgw_snapshot="$llmgw_backup_directory/ledger-$(date -u +%Y%m%dT%H%M%SZ).rdb"
if [ -e "$llmgw_snapshot" ]; then
  echo 'Snapshot path already exists; refusing to overwrite.' >&2
  exit 1
fi
llmgw_stage=$(mktemp -d "$llmgw_backup_directory/.ledger-backup.XXXXXX")
trap 'rm -rf "$llmgw_stage"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
docker compose -p "$llmgw_project" -f docker-compose.yml -f docker-compose.dev.yml exec -T ledger sh -ec '
  REDISCLI_AUTH=$(cat /run/secrets/ledger_backup_password)
  export REDISCLI_AUTH
  redis-cli --tls --cacert /run/secrets/redis_ca --user backup -e BGSAVE >/dev/null
  attempts=0
  while [ "$attempts" -lt 30 ]; do
    state=$(redis-cli --tls --cacert /run/secrets/redis_ca --user backup -e INFO persistence)
    if printf "%s\n" "$state" | tr -d "\r" | grep -q "^rdb_bgsave_in_progress:0$"; then
      printf "%s\n" "$state" | tr -d "\r" | grep -q "^rdb_last_bgsave_status:ok$"
      exit 0
    fi
    attempts=$((attempts + 1))
    sleep 1
  done
  echo "Snapshot did not finish within 30 seconds" >&2
  exit 1
'
docker compose -p "$llmgw_project" -f docker-compose.yml -f docker-compose.dev.yml cp ledger:/data/dump.rdb "$llmgw_stage/snapshot.rdb"
chmod 0600 "$llmgw_stage/snapshot.rdb"
# A hard link publishes complete bytes atomically and refuses every existing target type.
python3 - "$llmgw_stage/snapshot.rdb" "$llmgw_snapshot" <<'PY'
import os, sys
try:
    os.link(sys.argv[1], sys.argv[2])
except FileExistsError:
    sys.exit('Snapshot path already exists; refusing to overwrite.')
PY
shasum -a 256 "$llmgw_snapshot"
