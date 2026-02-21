## 压测（k6）

你现在的 compare 流程包含“微信支付闸门”。为了做吞吐/CPU 压测并触发 HPA，建议临时把费用设为 0（免费），避免卡在 `awaiting_payment`：

- `COMPARE_JOB_FEE_FEN=0`

### 1) 本地/外部压测

准备两份 Excel 文件路径，然后运行：

```bash
k6 run \
  -e BASE_URL="https://api.guangyang.online" \
  -e FILE1="/abs/path/a.xlsx" \
  -e FILE2="/abs/path/b.xlsx" \
  loadtest/k6_compare.js
```

### 2) 用 Docker 跑 k6（推荐）

```bash
docker run --rm -i \
  -v "$PWD:/work" \
  -w /work \
  grafana/k6 run \
  -e BASE_URL="https://api.guangyang.online" \
  -e FILE1="/work/a.xlsx" \
  -e FILE2="/work/b.xlsx" \
  loadtest/k6_compare.js
```

### 3) 观察指标

- `go-api`：`/metrics`（同 8080 端口）
- `compare-worker/payment-worker`：默认 `:9090/metrics`（集群内抓取）

关键 metrics（Prometheus）：
- `gy_http_requests_total`
- `gy_http_request_duration_seconds`
- `gy_worker_jobs_total`
- `gy_worker_job_duration_seconds`

## 在 ACK 里跑 k6（Job）

由于 `ConfigMap/Secret` 有大小限制（约 1MiB），较大的 xlsx 不能直接塞进去。
推荐做法：把 `k6_compare.js` 做成 ConfigMap，然后在 `k6 Job` 的 initContainer 里用 URL 下载 xlsx（比如 OSS 签名 URL）。

### 1) 创建脚本 ConfigMap

```bash
kubectl -n gy create configmap k6-script \
  --from-file=loadtest/k6_compare.js \
  --dry-run=client -o yaml | kubectl apply -f -
```

### 2) 配置并运行 Job

编辑 `k8s/40-k6-job.yaml`，把 `FILE1_URL/FILE2_URL` 填成可下载的 URL（推荐 OSS 签名 URL，expire 设久一点），然后：

```bash
kubectl -n gy delete job k6-compare --ignore-not-found
kubectl apply -f k8s/40-k6-job.yaml
kubectl -n gy logs -f job/k6-compare
```

