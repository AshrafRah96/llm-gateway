# Operations

Default Compose keeps Redis private and TLS-only. Cache and ledger use separate processes,
ACL users, volumes, and resource limits. Gateway runs without root, write access, or Linux capabilities.
Gateway listens on loopback; add managed HTTPS ingress for external access.

## Local setup

```bash
go run ./cmd/deployment-init
# Supply the provider key securely into .cache/deploy/openai_api_key.
docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build
source .cache/deploy/operator.env
go run ./cmd/project-key -project demo
```

Initialization refuses overwrite. Generated material lives beneath a private 0700 directory.
Docker-mounted files are readable by container users; the containing directory limits host access.
The CA private key is 0600. Local server certificates expire after three months.
No provider key is generated. Empty provider-key files prevent gateway startup.

`docker-compose.dev.yml` publishes only `127.0.0.1:16380` and `127.0.0.1:16381` for operators.
Default Compose exposes neither Redis nor RedisInsight. Set `DEPLOYMENT_SECRET_DIR` for managed material.
Production must supply trusted certificates, secure credential distribution, stable pepper backup,
off-host encrypted snapshots, monitoring, and a tested recovery owner.

## Key lifecycle

Operator credentials can administer keys/budgets. Runtime credentials cannot change either policy.
Never mount operator credentials into the gateway.

```bash
go run ./cmd/project-key -project demo -action provision
# Store old raw key in a private file; rotation prints the new key once.
go run ./cmd/project-key -project demo -action rotate -key-file /private/path/old-key \
  -overlap 24h -ttl 2160h
go run ./cmd/project-key -project demo -action revoke -key-file /private/path/key
```

New expiry defaults to 90 days. Rotation keeps project identity and shortens old-key validity to
the overlap or its existing expiry. Zero overlap revokes immediately. Provisioning cannot reassign
fingerprints or revive revoked keys. Repeating provision does not extend expiry.
The legacy `-key` argument is supported; key files avoid putting secrets in shell arguments.
Pepper rotation needs a separate migration and is outside this change.

## Budget policy

```bash
go run ./cmd/usage-admin -action limits -project demo -tokens 100000 -usd 10
go run ./cmd/usage-admin -action status -project demo
go run ./cmd/usage-admin -action pending -project demo
```

Use integer token ceilings and plain decimal USD with at most nine fractional digits.
Budgets cover paid attempts, not only successful answers. Exhaustion returns 429 with UTC rollover
`Retry-After`; ledger errors return 503 before further paid work. A stream may report a later failure
through an SSE error event after headers were sent.

## Health and recovery

`/health` checks liveness. `/ready` checks the persistent ledger and both Search indexes with a 3s bound.
Readiness does not verify provider credentials, model availability, storage durability, or backup freshness.

Ledger uses AOF with `appendfsync always`; vector state uses `everysec`. Both disable eviction.
Redis acknowledgement depends on filesystem/storage behavior; it is not an off-host recovery guarantee.
[Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/).

[Ledger migration/reconciliation](LEDGER.md) · [Recovery drill](LEDGER-RECOVERY.md)
· [Threat model](THREAT-MODEL.md).
