# Configuration reference

Configuration is read from environment variables at startup (Constitution section 70, ADR-018). Loading is
**fail-fast**: every problem is listed at once and the process exits non-zero. Error messages never contain values.

- Local development: copy `.env.example` to `.env` (git-ignored). Real environment variables always win over the file.
- The `.env` file is read **only** when `APP_ENV` is unset or `local`/`test`, and it cannot select `staging` or `production` (SR-19).
- Secrets and provider credentials are **never** configuration keys; they are reached through the `SecretStore` port.
- An empty value is treated as unset. A key with no default is required.
- A test fails when a key is read by the loader but not listed here, or listed here but not read.

## Keys

| Key | Type | Default | Required | Description |
|-----|------|---------|----------|-------------|
| `APP_ENV` | enum: `local`, `test`, `staging`, `production` | none | yes | Deployment environment. `staging` and `production` are production-like: local-only features (mock provider, local secret store, `.env`) are refused. |
| `APP_VERSION` | string | `dev` | no | Version reported in logs and telemetry; normally set at build or deploy time. |
| `LOG_LEVEL` | enum: `debug`, `info`, `warn`, `error` | `info` | no | Minimum log level. |
| `LOG_FORMAT` | enum: `json`, `text` | `json` | no | Log encoding. `text` is allowed only in `local` and `test`. |
| `HTTP_ADDR` | `host:port` | `:8080` | no | Public API listener. |
| `HTTP_OPERATOR_ADDR` | `host:port` | `127.0.0.1:8081` | no | Operator listener (metrics, admin). Must differ from `HTTP_ADDR`. Loopback by default; inside a container bind `:8081` and do not publish the port (SR-21). |
| `HTTP_READ_HEADER_TIMEOUT` | duration | `5s` | no | Maximum time to read request headers. Must not exceed `HTTP_READ_TIMEOUT`. |
| `HTTP_READ_TIMEOUT` | duration | `15s` | no | Maximum time to read the whole request. |
| `HTTP_WRITE_TIMEOUT` | duration | `30s` | no | Maximum time to write the response. Keep above the search deadline (about 8 s). |
| `HTTP_IDLE_TIMEOUT` | duration | `60s` | no | Keep-alive idle timeout. |
| `HTTP_SHUTDOWN_TIMEOUT` | duration | `25s` | no | Grace period to finish in-flight requests on SIGINT/SIGTERM. |
| `HTTP_MAX_BODY_BYTES` | integer, 1024 to 67108864 | `1048576` | no | Maximum request body size. |

Durations use Go syntax (`500ms`, `5s`, `2m`) and must be greater than zero.

## Added by later E1 slices
PostgreSQL, Redis, authentication, rate limits and telemetry keys arrive with their slices (S3 to S5) and are added to this table in the same PR.

## Notes
- `HTTP_ADDR` and `HTTP_OPERATOR_ADDR` may both use port `0` (any free port, for tests and ephemeral runs); otherwise they must differ.
- The API logs one `request` record per call with the route template, never the raw path, query string, headers or body.
