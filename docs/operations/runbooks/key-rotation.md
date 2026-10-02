# Runbook: rotate API keys and secrets

## Status
**State:** Ready
**Last verified:** 2026-10-02

Every procedure below was executed on the local Compose stack (`make stack`) on that date, with the original value restored
afterwards, and the outputs quoted here are the ones observed: key overlap and revocation (A), investigation queries (B),
pepper rotation (C), `fip_app` password rotation (D) and Redis password rotation (E).

Not covered, and not verified: provider credentials (no provider exists yet, E4), the PostgreSQL superuser password, and any
managed secret store (none is chosen; the local file store refuses to run outside `local` and `test`, SR-19).

## When to use
| Case | Use it when |
|------|-------------|
| **A. Routine API key rotation** | a client's key is approaching its expiry (keys issued by `keyctl` last 90 days unless `-ttl` says otherwise), or a client asks for a new key |
| **B. A key is compromised or suspected** | a key appeared somewhere it should not, a client reports a leak, or logs show use you cannot explain |
| **C. The pepper is compromised** | `secrets/api_key_pepper` leaked, or a database dump and the secrets directory may have been exposed together |
| **D. A database role password is compromised or due** | a `postgres_*_password` secret leaked, or on schedule |
| **E. The Redis password is compromised or due** | `secrets/redis_password` leaked, or on schedule |

A key is the **bearer credential** of one client; the pepper protects every stored key hash (ADR-027). If you are unsure which
case applies, start with B for the one key you suspect and escalate to C if the pepper may be involved.

## Impact
- **A:** none, when the overlap in step A.3 is respected.
- **B:** the client holding the revoked key is refused (`401`) from the next request; nothing else is affected.
- **C:** **every API key stops working**. All clients must receive new keys. Plan a window and tell the clients first.
- **D:** the API and worker restart (seconds, they report not ready meanwhile). Migrations and `keyctl` are on-demand and unaffected.
- **E:** Redis restarts, so rate-limit counters reset (acceptable, ADR-004); the API limits per instance until Redis is back (ADR-032).

## Before you start
- Run from the repository root in a POSIX shell with the stack up (`make stack`, `make dev` for D and E on a host-run API).
- `COMPOSE="docker compose -f deployments/local/docker-compose.yml"`; `keyctl` runs as `make keyctl ARGS="..."`.
- You need the files in `./secrets` (they are git-ignored and mode 0600). **Keep a copy of the current value outside the
  repository until the new one is confirmed working**; the steps that delete a secret tell you when.
- Tokens are printed **once** by `keyctl key issue`. Hand them to the client over a secure channel and do not paste them into tickets.
- Failed authentications are not written to the audit table (ADR-027, ADR-032); they are in the API log with `reason` and `key_prefix`.

## Steps

### A. Routine API key rotation (overlap, no downtime)
1. See what exists: `make keyctl ARGS="key list"` (prefix, client, role, status).
2. Issue the new key for the same client:
   `make keyctl ARGS="key issue -client NAME"` (add `-ttl 2160h` for the default 90 days, or `-ttl 0` for none).
   Output: `key issued prefix=XXXXXXXX expires=...` and `token=fip_...`. Save the token now.
3. Give the new token to the client and wait until the client confirms it works. **Both keys are valid in the meantime.**
4. Revoke the old key: `make keyctl ARGS="key revoke -prefix OLDPREFIX"`. Output: `key revoked prefix=OLDPREFIX`.

### B. A key is compromised or suspected
1. **Revoke it first, investigate after:** `make keyctl ARGS="key revoke -prefix PREFIX"`. It takes effect on the next request;
   nothing is cached.
2. Issue a replacement as in A.2 and deliver it securely.
3. Find out what the key did. Key lifecycle events and last use are in the database, read as the read-only role (it cannot see
   key material):
   ```bash
   $COMPOSE exec -T postgres sh -c 'export PGPASSWORD="$(cat /run/secrets/postgres_readonly_password)"; \
     psql -h localhost -U fip_readonly -d fip -At -F" | " \
       -c "SELECT prefix, created_at, expires_at, revoked_at, last_used_at FROM api_keys WHERE prefix = '"'"'PREFIX'"'"'" \
       -c "SELECT occurred_at, action, outcome, resource_id FROM audit_events ORDER BY occurred_at DESC LIMIT 20"'
   ```
   `last_used_at` is refreshed at most every five minutes (ADR-027): it tells you whether and roughly when the key was used,
   not every request. For failed attempts and for what a valid key did, search the API log:
   `$COMPOSE logs api | grep PREFIX` (failed attempts carry `"reason"` and `"key_prefix"`; every request carries `route`, `status`
   and `request_id`).
4. If the key was **valid and used by someone else**, treat it as a security incident (SEV1 if data may have been exposed,
   `reliability-and-observability.md` section 5): keep the logs, and open a post-mortem.

### C. Rotate the pepper (every key stops working)
1. Record the keys that exist so each client can be re-issued: `make keyctl ARGS="key list"` (client and prefix for each).
2. **Keep a copy of the current pepper** outside the repository: `cp secrets/api_key_pepper /safe/place/api_key_pepper.old`.
3. Create a new pepper: `rm secrets/api_key_pepper && make local-secrets` (output: `created secrets\api_key_pepper`; it never
   overwrites an existing file, which is why the old one is removed first).
4. Restart what reads it so it picks up the new value: `$COMPOSE --profile app up -d --force-recreate --no-deps --wait api`.
   For an API run on the host, restart that process.
5. Issue a new key for every client: `make keyctl ARGS="key issue -client NAME"` and deliver the tokens.
6. Revoke every old key, otherwise it stays `active` in the database although it can no longer authenticate:
   `make keyctl ARGS="key revoke -prefix PREFIX"` for each prefix from step 1.

### D. Rotate a database role password (`fip_app` as the example)
The roles and their secrets: `fip_app` / `postgres_password` (API, worker), `fip_migrator` / `postgres_migrator_password`
(migrations), `fip_admin` / `postgres_admin_password` (`keyctl`), `fip_readonly` / `postgres_readonly_password`.
1. Keep a copy of the current secret file outside the repository.
2. Create the new secret: `rm secrets/postgres_password && make local-secrets` (48 hexadecimal characters, safe to pass as is).
3. Apply it to the role, reading the value from the file and sending it on standard input so it is not on a command line:
   ```bash
   printf "ALTER ROLE fip_app PASSWORD '%s';\n" "$(cat secrets/postgres_password)" | \
     $COMPOSE exec -T postgres sh -c 'export PGPASSWORD="$(cat /run/secrets/postgres_superuser_password)"; \
       psql -h localhost -U postgres -d postgres -v ON_ERROR_STOP=1'
   ```
   Output: `ALTER ROLE`.
4. **Immediately** recreate the containers that use the role, so they read the new secret:
   `$COMPOSE --profile app up -d --force-recreate --no-deps --wait api worker`. Between steps 3 and 4 the running containers
   keep their open connections but cannot open new ones, so do not pause between them.
   For `fip_migrator`, `fip_admin` and `fip_readonly` nothing needs recreating: they are used on demand and read the secret each time.

### E. Rotate the Redis password
1. Keep a copy of `secrets/redis_password` outside the repository.
2. `rm secrets/redis_password && make local-secrets`.
3. Recreate Redis and the API together (Redis reads the secret at start, the API reads it at start):
   `$COMPOSE --profile app up -d --force-recreate --no-deps --wait redis api`.

## Verification
- **A:** `curl -s -o /dev/null -w "%{http_code}\n" -H "Authorization: Bearer NEWTOKEN" http://127.0.0.1:8080/v1/whoami` prints `200`
  with the new token and `401` with the old one. Observed: new `200`, old `200` during the overlap and `401` after revocation.
- **B:** the revoked token prints `401`, and `make keyctl ARGS="key list"` shows `status=revoked`. The API log has
  `"msg":"authentication failed"` with `"reason":"revoked"` and the key's prefix.
- **C:** an old token prints `401` and a token issued after step 5 prints `200`. Observed: the old key fails with
  `"reason":"unknown_or_wrong"` (not `revoked`), which is expected until step 6 revokes it.
- **D:** `curl -s http://127.0.0.1:8080/readyz` shows `{"checks":{"postgres":"ok","redis":"ok"},"status":"ready"}`, and the
  **old** password is refused:
  `printf 'SELECT 1;' | $COMPOSE exec -T postgres sh -c "PGPASSWORD='OLD' psql -h localhost -U fip_app -d fip -At"` ends with
  `FATAL:  password authentication failed for user "fip_app"`.
- **E:** `readyz` shows `"redis":"ok"`, and `$COMPOSE exec -T redis sh -c 'redis-cli --no-auth-warning -a OLD ping'` prints
  `AUTH failed: WRONGPASS invalid username-password pair or user is disabled.`

## If it goes wrong
- **C or D or E, the service does not become ready:** put the saved copy back (`cp /safe/place/... secrets/...`), and for D reset
  the role to that value with the `ALTER ROLE` command of step D.3, then recreate the containers again. This is the same
  sequence that was used to restore the original value during verification.
- **A client lost its token:** it cannot be recovered (only the hash is stored). Issue a new key and revoke the lost one.
- **You cannot tell whether a leaked key was used:** assume it was; revoke it (B.1) and follow B.4.
- **The superuser password or a secret not listed here may be exposed:** this runbook does not cover it; treat it as SEV1 and escalate.

## Follow-up
- Record the change (who, when, which case and why) wherever the change log is kept; key issuing and revocation are already in
  `audit_events`.
- After C, delete the old pepper copy once every client has a working key.
- Compromise (B or C): run a blameless post-mortem within five business days and decide whether the key lifetime or the rotation
  schedule should change.
- When a managed secret store replaces the local files, rewrite D and E for it and re-verify (ADR-018, ADR-031).
