## Project overview

This project is a production-oriented **asynchronous Excel diff/export** service. The default deployment is now single-host Docker Compose: local Redis, local shared disk, local workers, and local `xlsconvert`. OSS/ACK are kept only as historical or optional expansion paths.

### Architecture highlights
- **Go API**: accepts uploads, stores input files under shared local `TMP_ROOT` (or OSS when explicitly enabled), persists job metadata in Redis, and enqueues job IDs into Redis Streams.
- **compare-worker**: consumes the compare stream, reads inputs from the shared local directory (or OSS when explicitly enabled), converts `.xls→.xlsx` via `xlsconvert` when needed, and generates the diff/export.
- **payment-worker (separate scaling domain)**: consumes the paygate stream and drives the payment state machine (WeChat Native Pay order creation / status transitions) to keep payment logic decoupled from heavy compute.
- **Storage & queue**
  - Redis: Compose-managed local Redis for consistent job state (idempotent updates) + Streams queue
  - Local shared disk: default input/output storage; downloads are served by Go, not redirected to OSS signed URLs
- **Reliability**
  - Redis Streams consumer groups + pending auto-claim
  - Distributed lock (SETNX + TTL) to prevent duplicate computation across worker replicas
  - Failure policy: mark job `failed` on terminal errors (no automatic retries)
- **Observability**
  - Structured JSON logs (`slog`)
  - Prometheus metrics (`/metrics` on API; workers expose a lightweight metrics server)
  - OpenTelemetry tracing (OTLP; no-op when no endpoint is configured)
- **Performance (without changing output semantics)**
  - Streaming XLSX read (key→row), O(1) column alignment, streaming XLSX export (StreamWriter)
  - Job-local normalize dedup, write-on-demand to reduce peak memory/GC

---

## Local development quickstart

### Prerequisites (aligned with Dockerfiles)
- **Go**: 1.24+
- **Node.js**: 20+

### Option A: run locally (dev/debug)

#### 1) Start Go (default 8080)

```bash
cd gobackend
export PORT=8080
export CORS_ALLOW_ORIGIN=http://localhost:5173
go run .
```

Optional env:
- `TMP_ROOT`: temp directory for compare jobs (default `./tmp`)

#### 2) Start frontend (Vite, default 5173)

```bash
cd frontend
npm ci
npm run dev
```

Optional (Vite):
- `VITE_GO_API_BASE` (default `http://localhost:8080`)

### Option B: Docker Compose (local integration)

```bash
docker compose up -d --build
```

Default ports (see `docker-compose.yml`):
- Frontend Nginx: `http://localhost:8088`
- Go API: reverse-proxied by Nginx under `/api/`

Important: **do not commit real production secrets**. Use `env.prod.example` as a template; keep the real `env.prod` only on disk and it is ignored by `.gitignore`.

---

## API reference (for integration)

### Go (billing stub + compare orchestration)
- Health: `GET /healthz`
- Profile: `GET /profile`
- Billing:
  - `POST /billing/pending` (JSON: `amount`, optional `idempotencyKey`)
  - `POST /billing/deduct` (JSON: `idempotencyKey`, `amount`)
- Compare jobs (pay-gated):
  - `POST /compare/jobs` (multipart: `file1`, `file2`) → returns `jobId`
  - `GET /compare/jobs/{jobId}` → returns `status`, `paid`; includes `amount`, `code_url` if awaiting payment
  - `GET /compare/jobs/{jobId}/export` → requires `ready` and paid; otherwise returns 402/410
  - `POST /compare/jobs/{jobId}/cancel`
- WeChat notify: `POST /wechatpay/notify` (called by WeChat; not meant for manual calls)

---

## Production routing (Docker + Nginx)

In production the frontend uses same-origin reverse proxy (see `frontend/nginx.conf` and `frontend/src/api/paidApi.js`):
- `/api/` → Go `:8080`
- `/wechatpay/` → Go (to avoid being swallowed by SPA routing rules)

---

## CI/CD (GitLab single host)

`.gitlab-ci.yml` runs on the `main` branch:
- build: builds local `web/go` images on this GitLab server
- deploy: runs `docker compose -f docker-compose.prod.yml up -d --remove-orphans` on this GitLab server

