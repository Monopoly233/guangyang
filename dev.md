## 项目概览

这是一套面向生产的 **异步 Excel 比对导出** 服务，重点在于：可扩展的任务队列、对象存储、支付闸门、可观测性与自动扩缩容。

### 架构要点
- **Go API（stateless）**：接收上传，把输入文件写入 OSS，创建 job 元数据（Redis），投递 Redis Streams。
- **compare-worker（可水平扩容）**：消费 compare stream，从 OSS 拉取输入，必要时调用 `xlsconvert` 做 `.xls→.xlsx`，执行比对并导出结果，再上传 OSS。
- **payment-worker（独立扩容域）**：消费 paygate stream，负责创建微信 Native Pay 订单/推进 job 状态机（把支付逻辑与重计算解耦）。
- **存储与队列**：
  - Redis：job 状态一致性（幂等更新） + Streams 队列
  - OSS：输入/输出文件（结果用签名 URL 直下）
- **可靠性设计**：
  - Redis Streams consumer group + pending 自动认领
  - 分布式锁（SETNX+TTL）避免多 worker 重复计算
  - 失败策略：业务失败标记 job failed（不自动重试）
- **可观测性**：
  - JSON 结构化日志（slog）
  - Prometheus metrics（`/metrics` + worker 独立 metrics server）
  - OpenTelemetry tracing（OTLP，未配置 endpoint 时自动 no-op）
- **性能优化（不改变输出语义）**：
  - key->row streaming read、列索引 O(1)、diff 流式导出（StreamWriter）
  - normalize 本地去重、按需写出减少内存峰值

---

## 本地开发快速开始

### 依赖（按 Dockerfile 口径）
- **Go**：1.24+
- **Node.js**：20+

### 方式 A：本机直接运行（开发调试）

#### 1) 启动 Go（默认 8080）

```bash
cd gobackend
export PORT=8080
export CORS_ALLOW_ORIGIN=http://localhost:5173
go run .
```

可选环境变量：
- **`TMP_ROOT`**：对比任务的临时目录；默认 `./tmp`

#### 2) 启动前端（Vite，默认 5173）

```bash
cd frontend
npm ci
npm run dev
```

可选环境变量（Vite）：
- **`VITE_GO_API_BASE`**：默认 `http://localhost:8080`

### 方式 B：Docker Compose 一键启动（本地/联调）

```bash
docker compose up -d --build
```

默认端口（见 `docker-compose.yml`）：
- **前端 Nginx**：`http://localhost:8088`
- **Go**：由前端 Nginx 通过 `/api/` 反代

注意：**不要把真实生产密钥放进仓库**。可以参考 `env.prod.example`，把真实 `env.prod` 放在本地磁盘并被 `.gitignore` 忽略。

## 接口速查（便于联调）

### Go（计费 + 对比任务编排）
- **健康检查**：`GET /healthz`
- **余额**：`GET /profile`
- **计费**：
  - `POST /billing/pending`（JSON：`amount`、可选 `idempotencyKey`）
  - `POST /billing/deduct`（JSON：`idempotencyKey`、`amount`）
- **对比任务（带支付闸门）**：
  - `POST /compare/jobs`（multipart：`file1`、`file2`）→ 返回 `jobId`
  - `GET /compare/jobs/{jobId}` → 返回 `status`、`paid`；若等待支付则带 `amount`、`code_url`
  - `GET /compare/jobs/{jobId}/export` → 需已支付且任务 ready，否则返回 402/410 等
  - `POST /compare/jobs/{jobId}/cancel`
- **微信支付回调**：`POST /wechatpay/notify`（由微信侧回调，不建议手工调用）

## 生产环境路由约定（Docker + Nginx）

生产环境下前端不直连端口，而是走同域反代（见 `frontend/nginx.conf` 与 `frontend/src/api/paidApi.js`）：
- **`/api/`** → Go `:8080`（前端默认 `GO_API_BASE=/api`）
- **`/wechatpay/`** → Go（避免被 SPA 的 `try_files` 吞掉）

## 关键环境变量（生产/支付相关）

Go 服务除基础变量外，还支持微信支付相关配置（建议用 `.env` / `env.prod` / CI 变量注入，避免写死在 compose 文件里）：
- **基础**：`PORT`、`CORS_ALLOW_ORIGIN`、`TMP_ROOT`
- **微信支付**：`WECHAT_NOTIFY_URL`、`WECHAT_MCHID`、`WECHAT_APPID`、`WECHAT_PAY_APPID`、`WECHAT_CORP_ID`、`WECHAT_API_V3_KEY`、`WECHAT_PLATFORM_PUBLIC_KEY_ID`、`WECHAT_PLATFORM_PUBLIC_KEY`、`WECHAT_ALLOW_WW_APPID`、`WECHAT_MOCK`

证书/密钥文件约定（只列路径，不在文档里放明文密钥）：
- 本地 compose：`./wechatpay` 会挂载到容器 `/app/wechatpay`（只读）
- 生产 compose：`/opt/app/gy/wechatpay/cert` 会挂载到容器 `/app/wechatpay/cert`（只读）

## GitLab CI 自动部署说明

`.gitlab-ci.yml` 在 `main` 分支触发：
- build/push：构建并推送 `web/go` 镜像到 ACR
- deploy：`kubectl apply -f k8s/` + 固定使用本次 `IMAGE_TAG` 滚动部署