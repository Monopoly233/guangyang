## ACK 部署（最小可跑通版）

本目录把 `docker-compose.prod.yml` 的服务（web/go）平移成 Kubernetes 清单：

- `00-namespace.yaml`：namespace `gy`
- `20-go.yaml`：Go API（Service: `go:8080`）
- `30-web.yaml`：Nginx 静态站点（Service: `web:80`）
- `40-albconfig-ingressclass.yaml`：复用现成 ALB 的 AlbConfig + IngressClass（名字：`gy-alb`）
- `50-ingress.yaml`：ALB Ingress 规则（`api/pay/www` → `go/web`）

### 前置条件

1. 你已将镜像 push 到 ACR（至少有 `:latest`）：

- `guangyang-registry.cn-heyuan.cr.aliyuncs.com/guangyang/web:latest`
- `guangyang-registry.cn-heyuan.cr.aliyuncs.com/guangyang/go:latest`

2. ACK 集群能拉取 ACR 私有镜像：
   - 如果你集群安装了 `managed-aliyun-acr-credential-helper` 并配置 OK，通常无需额外 secret。
   - 否则需要创建 `imagePullSecret` 并在 Deployment 里引用（后续再加）。

### 应用方式（任选其一）

#### A) ACK 控制台 Workbench 直接 apply

在 ACK 集群页面使用 Workbench/控制台执行：

```bash
kubectl apply -f k8s/
```

#### B) 在你能访问集群的机器上 kubectl apply

```bash
kubectl apply -f k8s/
```

### 验证

```bash
kubectl -n gy get deploy,svc,pod
kubectl -n gy logs deploy/go --tail=50
```

Go 健康检查：

```bash
kubectl -n gy port-forward svc/go 18080:8080
curl -sS http://127.0.0.1:18080/healthz
```

### ALB Ingress

本仓库提供了可直接 apply 的 ALB 入口配置：

- IngressClass：`gy-alb`
- Ingress：`gy/gy-ingress`

注意：
- `k8s/40-albconfig-ingressclass.yaml` 中写死了 ALB 实例 ID：`alb-r8l72kghnnh97b0epj`（如你更换 ALB，需要同步修改）
- Ingress 已配置同时监听 80/443。你若使用 Cloudflare Full(strict)，请确保 ALB 已配置 443 监听与 Origin 证书。

### 注意（微信回调）

为了先跑通 ALB 路由，本版本不强依赖微信支付配置：
- 你可以先用 **GET** 请求访问 `/wechatpay/notify` 验证路由（会返回 405，不会触发验签/解密配置读取）。
- 真正接入微信回调（POST）需要配置 `WECHAT_API_V3_KEY` + 平台验签材料（平台公钥或平台证书）。

如需在 ACK 上做真实支付联调（例如把 compare 费用设为 0.01 元）：

1. 按模板创建 Secret（不要把真实密钥提交到 git）：
   - `k8s/_templates/16-wechatpay-secrets.template.yaml`（`wechatpay-env` + `wechatpay-cert`，注意不要直接 apply 到集群）
2. 确保 `gy/go` Deployment 已挂载 `/app/wechatpay/cert` 且能读到：
   - `merchant_key.pem`、`merchant_cert.pem`
   - 平台验签材料二选一：
     - `platform_cert.pem`（放入 `wechatpay-cert`），或
     - 配置 `WECHAT_PLATFORM_PUBLIC_KEY`（以及对应的 `WECHAT_PLATFORM_PUBLIC_KEY_ID`）
3. 确保回调域名路由到 Go：
   - `https://pay.guangyang.online/wechatpay/notify` → Ingress 已配置到 `svc/go:8080`
   

