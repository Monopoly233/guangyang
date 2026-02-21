## ACK (Kubernetes) deployment (resume version)

This directory deploys the service as **Go API + compare-worker + payment-worker + web + xlsconvert**, and includes HPA and a load-test template.

- `00-namespace.yaml`: namespace `gy`
- `15-go-serviceaccount.yaml`: ServiceAccount + RRSA token projection (when applicable)
- `20-go.yaml`: Go API (Service `go:8080`) + `compare-worker` + `payment-worker`
- `30-web.yaml`: frontend Nginx (Service `web:80`)
- `30-hpa.yaml`: HPA (CPU utilization)
- `40-albconfig-ingressclass.yaml`: ALB AlbConfig + IngressClass (`gy-alb`)
- `50-ingress.yaml`: ALB Ingress rules (`api/pay/www` → `go/web`)
- `40-k6-job.yaml`: k6 load test **CronJob** template (default `suspend: true`; trigger manually)

### Prerequisites

1) Images pushed to ACR (prefer commit tags; do not rely on `latest`):
- `.../guangyang/web:<tag>`
- `.../guangyang/go:<tag>`

2) ACK can pull ACR private images:
- If you have `managed-aliyun-acr-credential-helper`, you may not need extra secrets.
- Otherwise create an `imagePullSecret` and attach it to the namespace/service account.

### Apply manifests

```bash
kubectl apply -f k8s/
```

### Verify

```bash
kubectl -n gy get deploy,svc,pod
kubectl -n gy logs deploy/go --tail=50
```

Health check:

```bash
kubectl -n gy port-forward svc/go 18080:8080
curl -sS http://127.0.0.1:18080/healthz
```

### ALB Ingress notes

`k8s/40-albconfig-ingressclass.yaml` contains cluster-specific settings (e.g. an ALB instance ID). If you reuse these manifests in another cluster, update them accordingly.

### WeChat notify notes

To validate routing, you can issue a **GET** to `/wechatpay/notify` (it should return 405 and will not attempt signature verification).
For real WeChat callbacks (POST), you must configure:
- `WECHAT_API_V3_KEY`
- Platform verification material (either platform certificate PEM or platform public key)

The notify route should be reachable as:
- `https://<your-domain>/wechatpay/notify` → `svc/go:8080`

