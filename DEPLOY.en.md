## Cloud deployment (GitLab CI/CD)

### 1) Services and routes
- **web**: static frontend built from `frontend/` served by Nginx
  - `/`: frontend SPA
  - `/api/`: reverse proxy to Go (`go:8080`)
- **go**: `gobackend` (includes `/compare/jobs`, `/wechatpay/notify`, etc.)

### 2) Local one-command run (without CI)

```bash
cp .env.example .env
# Fill WECHAT_NOTIFY_URL (required for real pay) and provide WeChat cert/key files.
docker compose up -d --build
```

Open `http://localhost/`.

### 3) Production run (on a server)

Prereqs:
- Docker + Docker Compose v2

On the server, prepare a directory (e.g. `/opt/guagnyang`) with:
- `docker-compose.prod.yml`
- `.env` or `env.prod` (**create it on the server; do not commit real secrets**; use `env.prod.example` as a template)
- `wechatpay/` directory (certs/keys; keep it only on the server)

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

Configure GitLab CI/CD variables (Settings → CI/CD → Variables):
- **ACR_USERNAME / ACR_PASSWORD** (to push images)
- **KUBECONFIG_B64** (base64 kubeconfig for ACK access)

WeChat config (recommended as masked/protected vars):
- `WECHAT_NOTIFY_URL` (must be HTTPS, no query string)
- Optional: `WECHAT_MCHID`, `WECHAT_PAY_APPID`, `WECHAT_API_V3_KEY`, `WECHAT_PLATFORM_PUBLIC_KEY_ID`, `WECHAT_PLATFORM_PUBLIC_KEY`, `WECHAT_MOCK`

### 5) WeChat cert/key material

In ACK deployment, CI creates/updates:
- `gy/wechatpay-env` (small env vars)
- `gy/wechatpay-cert` (cert/key files) mounted into containers at `/app/wechatpay/cert`

Do **not** commit any real keys/certs to git.

