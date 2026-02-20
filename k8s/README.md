## ACK 部署（最小可跑通版）

本目录把 `docker-compose.prod.yml` 的 3 个服务（web/go/python）平移成 Kubernetes 清单：

- `00-namespace.yaml`：namespace `gy`
- `10-python.yaml`：FastAPI（Service: `python:8000`）
- `20-go.yaml`：Go API（Service: `go:8080`）
- `30-web.yaml`：Nginx 静态站点（Service: `web:80`）

### 前置条件

1. 你已将镜像 push 到 ACR（至少有 `:latest`）：

- `guangyang-registry.cn-heyuan.cr.aliyuncs.com/guangyang/web:latest`
- `guangyang-registry.cn-heyuan.cr.aliyuncs.com/guangyang/go:latest`
- `guangyang-registry.cn-heyuan.cr.aliyuncs.com/guangyang/python:latest`

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
kubectl -n gy logs deploy/python --tail=50
```

Go 健康检查：

```bash
kubectl -n gy port-forward svc/go 18080:8080
curl -sS http://127.0.0.1:18080/healthz
```

### 注意（微信回调）

为了先跑通 ALB 路由，本版本不强依赖微信支付配置：
- 你可以先用 **GET** 请求访问 `/wechatpay/notify` 验证路由（会返回 405，不会触发验签/解密配置读取）。
- 真正接入微信回调（POST）需要配置 `WECHAT_API_V3_KEY` + 平台验签材料（平台公钥或平台证书）。

