## Project overview

This project is a production-oriented **asynchronous Excel diff/export** service. The focus is on a scalable job pipeline, object storage, payment gating, observability, and autoscaling.

### Architecture highlights
- **Go API (stateless)**: accepts uploads, stores input files in OSS, persists job metadata in Redis, and enqueues job IDs into Redis Streams.
- **compare-worker (horizontally scalable)**: consumes the compare stream, downloads inputs from OSS, converts `.xls→.xlsx` via `xlsconvert` when needed, generates the diff/export, and uploads results back to OSS.
- **payment-worker (separate scaling domain)**: consumes the paygate stream and drives the payment state machine (WeChat Native Pay order creation / status transitions) to keep payment logic decoupled from heavy compute.
- **Storage & queue**
  - Redis: consistent job state (idempotent updates) + Streams queue
  - OSS: input/output files (exports are downloaded via signed URLs)
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

## CI/CD (GitLab → ACK)

`.gitlab-ci.yml` runs on the `main` branch:
- build/push: builds and pushes `web/go` images to ACR
- deploy: `kubectl apply -f k8s/` and rolls out deployments using the current `IMAGE_TAG`

