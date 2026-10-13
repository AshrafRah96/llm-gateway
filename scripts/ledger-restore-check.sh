#!/bin/sh
set -eu
# Only the copied snapshot and newly named drill resources are writable.
llmgw_snapshot=${1:?usage: ledger-restore-check.sh SNAPSHOT.rdb}
[ -f "$llmgw_snapshot" ] || { echo 'Snapshot must be a regular file.' >&2; exit 1; }
umask 077
mkdir -p .cache
llmgw_scratch=$(mktemp -d "$PWD/.cache/ledger-restore.XXXXXX")
llmgw_id=${llmgw_scratch##*/}
llmgw_container="$llmgw_id"
llmgw_volume="$llmgw_id-data"
llmgw_created=false
llmgw_volume_created=false
cleanup() {
  if [ "$llmgw_created" = true ]; then docker rm -f "$llmgw_container" >/dev/null; fi
  if [ "$llmgw_volume_created" = true ]; then docker volume rm "$llmgw_volume" >/dev/null; fi
  rm -rf "$llmgw_scratch"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
llmgw_image=$(awk '/^  ledger:/{ledger=1;next} ledger && /^    image:/{print $2;exit}' docker-compose.yml)
printf '%s\n' "$llmgw_image" | grep -Eq '^[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}$' || {
  echo 'Compose ledger image must be pinned by SHA-256 digest.' >&2; exit 1;
}
docker image inspect "$llmgw_image" >/dev/null
cp "$llmgw_snapshot" "$llmgw_scratch/dump.rdb"
chmod 0600 "$llmgw_scratch/dump.rdb"
llmgw_sha=$(shasum -a 256 "$llmgw_scratch/dump.rdb" | awk '{print $1}')
od -An -tx1 -N32 /dev/urandom | tr -d ' \n' > "$llmgw_scratch/password"
llmgw_password_hash=$(shasum -a 256 "$llmgw_scratch/password" | awk '{print $1}')
printf 'user default off\nuser restore on #%s ~* +@all\n' "$llmgw_password_hash" > "$llmgw_scratch/users.acl"
cat > "$llmgw_scratch/redis.conf" <<'CONFIG'
bind 127.0.0.1
protected-mode yes
port 6379
dir /data
dbfilename dump.rdb
save ""
appendonly no
appendfsync always
aclfile /data/restore/users.acl
maxmemory 256mb
maxmemory-policy noeviction
CONFIG
if docker volume inspect "$llmgw_volume" >/dev/null 2>&1; then
  echo 'Drill volume already exists; refusing to reuse it.' >&2; exit 1
fi
docker volume create --label "llm-gateway.restore-check=$llmgw_id" "$llmgw_volume" >/dev/null
llmgw_volume_created=true
docker create --name "$llmgw_container" --label "llm-gateway.restore-check=$llmgw_id" \
  --network none --read-only --tmpfs /tmp --memory 512m --cpus 1 --pids-limit 128 \
  --security-opt no-new-privileges --cap-drop ALL \
  -v "$llmgw_volume:/data" \
  "$llmgw_image" /opt/redis-stack/bin/redis-server /data/restore/redis.conf >/dev/null
llmgw_created=true
# Container-owned copies keep host files private without relying on DAC override.
python3 - "$llmgw_scratch" <<'PY_ARCHIVE' | docker cp - "$llmgw_container:/data"
import pathlib, sys, tarfile
root = pathlib.Path(sys.argv[1])
with tarfile.open(fileobj=sys.stdout.buffer, mode='w|') as archive:
    directory = tarfile.TarInfo('restore')
    directory.type = tarfile.DIRTYPE
    directory.mode = 0o700
    archive.addfile(directory)
    for name in ('dump.rdb', 'redis.conf', 'users.acl', 'password'):
        path = root / name
        target = name if name == 'dump.rdb' else 'restore/' + name
        info = archive.gettarinfo(str(path), arcname=target)
        info.uid = info.gid = 0
        info.uname = info.gname = ''
        info.mode = 0o600
        with path.open('rb') as source:
            archive.addfile(info, source)
PY_ARCHIVE

redis() {
  docker exec "$llmgw_container" sh -ec '
    REDISCLI_AUTH=$(cat /data/restore/password)
    export REDISCLI_AUTH
    exec redis-cli --user restore --raw -e "$@"
  ' sh "$@"
}
ready() {
  llmgw_attempt=0
  while [ "$llmgw_attempt" -lt 30 ]; do
    if [ "$(redis PING 2>/dev/null || true)" = PONG ]; then return; fi
    llmgw_attempt=$((llmgw_attempt + 1))
    sleep 1
  done
  docker logs --tail 20 "$llmgw_container" >&2
  echo 'Isolated Redis did not become ready.' >&2; exit 1
}
# Stable hashes verify every auth/ledger field without printing records or credentials.
llmgw_fingerprint='
local keys=redis.call("KEYS","project_key:*")
for _,k in ipairs(redis.call("KEYS","ledger:*")) do table.insert(keys,k) end
table.sort(keys)
local records={};local counts={0,0,0,0,0}
for _,k in ipairs(keys) do
  if redis.call("TYPE",k).ok~="hash" then error("non-hash auth/ledger record") end
  local fields=redis.call("HKEYS",k);table.sort(fields);local values={}
  for _,f in ipairs(fields) do table.insert(values,{f,redis.call("HGET",k,f)}) end
  table.insert(records,{k,values})
  if string.sub(k,1,12)=="project_key:" then counts[1]=counts[1]+1
  elseif string.match(k,":events$") then counts[2]=counts[2]+#fields
  elseif string.match(k,":pending$") then counts[3]=counts[3]+#fields
  elseif string.match(k,":totals$") then counts[4]=counts[4]+1
  elseif string.match(k,":day:") then counts[5]=counts[5]+1 end
end
return {redis.sha1hex(cjson.encode(records)),unpack(counts)}'
docker start "$llmgw_container" >/dev/null
ready
llmgw_before=$(redis EVAL "$llmgw_fingerprint" 0)
# Enable AOF only after Redis has loaded the RDB into this fresh volume.
redis CONFIG SET appendonly yes >/dev/null
llmgw_attempt=0
while :; do
  llmgw_state=$(redis INFO persistence | tr -d '\r')
  if printf '%s\n' "$llmgw_state" | grep -q '^aof_rewrite_in_progress:0$' && \
     printf '%s\n' "$llmgw_state" | grep -q '^aof_last_bgrewrite_status:ok$'; then break; fi
  llmgw_attempt=$((llmgw_attempt + 1))
  [ "$llmgw_attempt" -lt 30 ] || { echo 'AOF creation did not finish.' >&2; exit 1; }
  sleep 1
done
redis SET restore-check:canary "$llmgw_id" >/dev/null
# Persist only the drill configuration in its isolated writable volume.
redis CONFIG REWRITE >/dev/null
docker restart -t 30 "$llmgw_container" >/dev/null
ready
[ "$(redis GET restore-check:canary)" = "$llmgw_id" ] || { echo 'AOF canary did not survive restart.' >&2; exit 1; }
[ "$(redis EVAL "$llmgw_fingerprint" 0)" = "$llmgw_before" ] || { echo 'Restored records changed across restart.' >&2; exit 1; }
redis INFO persistence | tr -d '\r' | grep -q '^aof_enabled:1$'
[ "$(shasum -a 256 "$llmgw_snapshot" | awk '{print $1}')" = "$llmgw_sha" ] || {
  echo 'Input snapshot changed during the drill.' >&2; exit 1;
}
printf 'PASS: isolated RDB load and AOF restart; snapshot SHA-256 %s\n' "$llmgw_sha"
printf '%s\n' "$llmgw_before" | awk 'NR==1 {print "record fingerprint: " $0} NR==2 {print "auth records: " $0} NR==3 {print "usage events: " $0} NR==4 {print "pending reservations: " $0} NR==5 {print "project totals: " $0} NR==6 {print "UTC-day records: " $0}'
