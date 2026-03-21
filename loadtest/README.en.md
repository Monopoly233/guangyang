## Load testing (k6)

Your compare flow includes a payment gate. For throughput/CPU load tests (and to trigger HPA), temporarily make compare free:

- `COMPARE_JOB_FEE_FEN=0`

### Metrics endpoints
- **go-api**: `GET /metrics` on port 8080
- **compare-worker / payment-worker**: a lightweight metrics server on `METRICS_ADDR` (default `:9090`)

Key Prometheus metrics:
- `gy_http_requests_total`
- `gy_http_request_duration_seconds`
- `gy_worker_jobs_total`
- `gy_worker_job_duration_seconds`

---

## Running k6 inside ACK (CronJob + manual Job)

Because `ConfigMap/Secret` objects have size limits (~1MiB), large XLSX files should not be embedded directly.
Recommended approach:
- Store `k6_compare.js` in a ConfigMap (`k6-script`)
- Download XLSX files in an initContainer (e.g. from OSS signed URLs) into an `emptyDir` volume

### 1) Create/update the script ConfigMap

```bash
kubectl -n gy create configmap k6-script \
  --from-file=loadtest/k6_compare.js \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 2) Apply the CronJob template

Edit `k8s/40-k6-job.yaml` and set `FILE1_URL` / `FILE2_URL` to downloadable URLs (OSS signed URLs with a long expiry is recommended).

```bash
kubectl apply -f k8s/40-k6-job.yaml
```

### 3) Trigger a one-off Job (recommended)

Jobs have immutable pod templates. Instead of `kubectl apply` on a Job, keep a CronJob as the template and create a one-off Job from it:

```bash
name="k6-compare-manual-$(date +%s)"
kubectl -n gy create job --from=cronjob/k6-compare "$name"
kubectl -n gy logs -f "job/$name" -c fetch-files
kubectl -n gy logs -f "job/$name" -c k6
```

