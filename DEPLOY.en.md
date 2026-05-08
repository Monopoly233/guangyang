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
- `${WECHATPAY_DIR:-./wechatpay}`: WeChatPay certs/keys and apikey files (`cert/` and `apikey/`)

GitLab CI sets `GY_DATA_DIR` to `$CI_PROJECT_DIR/runtime` by default, so the runner does not need write access to `/opt`. If you grant that permission later, override `GY_DATA_DIR` to `/opt/app/gy` in CI/CD variables.

The frontend binds to `127.0.0.1:8088` by default to avoid conflicts with GitLab/Nginx already using port `80`. Public traffic should enter through host Nginx/Cloudflare on 80/443, then proxy to `127.0.0.1:8088`.

A host Nginx template is provided:

```bash
sudo cp deploy/nginx-guangyang.conf /etc/nginx/conf.d/guangyang.conf
sudo nginx -t
sudo systemctl reload nginx
```

If the server already has a `guangyang.online` server block, change its upstream/proxy target to `http://127.0.0.1:8088`; do not leave it pointing at the old ACK/ALB or a missing local port.

For large uploads, host Nginx `client_max_body_size` must also be high enough. The template uses `512m`, and Go defaults to `COMPARE_MAX_UPLOAD_MB=512`; keep both values in sync if you raise the limit.

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

For single-host deployment, put cert/key files on the server under `${WECHATPAY_DIR:-./wechatpay}/cert`, mounted into containers at `/app/wechatpay/cert`. If you use file-based API v3 key fallback, put it under `${WECHATPAY_DIR:-./wechatpay}/apikey/apikey.txt`.

Do **not** commit any real keys/certs to git.

