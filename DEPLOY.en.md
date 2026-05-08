## Cloud deployment (GitLab CI/CD)

### 1) Services and routes
- **web**: static frontend built from `frontend/` served by Nginx
  - `/`: frontend SPA
  - `/api/`: reverse proxy to Go (`go:8080`)
- **go**: `gobackend` (includes `/compare/jobs`, `/wechatpay/notify`, etc.)
- **redis**: local Compose-managed Redis for job state and streams
- **compare-worker / payment-worker**: local workers consuming local Redis Streams
- **xlsconvert**: local `unoserver` for `.xls` to `.xlsx` conversion

### 2) Local one-command run (without CI)

```bash
cp env.prod.example env.prod
# Fill WECHAT_NOTIFY_URL (required for real pay) and provide WeChat cert/key files.
docker compose --env-file env.prod -f docker-compose.prod.yml up -d --build
```

Open `http://localhost/`.

### 3) Production run (on a server)

Prereqs:
- Docker + Docker Compose v2

On the server, prepare a directory (e.g. `/opt/guagnyang`) with:
- `docker-compose.prod.yml`
- `.env` or `env.prod` (**create it on the server; do not commit real secrets**; use `env.prod.example` as a template)
- `wechatpay/` directory (certs/keys; keep it only on the server)

Runtime data is kept on the same server:
- `${GY_DATA_DIR:-./runtime}/tmp`: uploaded files and export results; downloads no longer redirect to OSS
- `${GY_DATA_DIR:-./runtime}/redis`: Redis AOF data
- `${GY_DATA_DIR:-./runtime}/wechatpay/cert`: WeChatPay certs/keys

GitLab CI sets `GY_DATA_DIR` to `$CI_PROJECT_DIR/runtime` by default, so the runner does not need write access to `/opt`. If you grant that permission later, override `GY_DATA_DIR` to `/opt/app/gy` in CI/CD variables.

Start:

```bash
cd /opt/guagnyang
# If your env file is named .env:
docker compose -f docker-compose.prod.yml up -d
#
# If your env file is named env.prod:
# docker compose --env-file env.prod -f docker-compose.prod.yml up -d
```

### 4) GitLab CI automated deployment

The pipeline is defined in `.gitlab-ci.yml` and triggers on the `main` branch.

The runner should be a shell runner on the same GitLab server and must be able to run `docker`.

The pipeline no longer pushes to ACR and no longer runs `kubectl`. It executes locally:
- `docker compose -f docker-compose.prod.yml build`
- `docker compose -f docker-compose.prod.yml up -d --remove-orphans`

WeChat config (recommended as masked/protected vars):
- `WECHAT_NOTIFY_URL` (must be HTTPS, no query string)
- Optional: `WECHAT_MCHID`, `WECHAT_PAY_APPID`, `WECHAT_API_V3_KEY`, `WECHAT_PLATFORM_PUBLIC_KEY_ID`, `WECHAT_PLATFORM_PUBLIC_KEY`, `WECHAT_MOCK`

### 5) WeChat cert/key material

For single-host deployment, put cert/key files on the server under `${GY_DATA_DIR:-./runtime}/wechatpay/cert`, mounted into containers at `/app/wechatpay/cert`.

Do **not** commit any real keys/certs to git.

