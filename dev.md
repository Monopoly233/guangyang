## 开发完成度（截至 2026 年 1 月 31 日）

### 已完成
- **后端（Go）**：实现一个计费/余额的内存版 stub 服务（`gobackend/main.go`）
  - **健康检查**：`GET /healthz` 返回 `{"status":"ok"}`
  - **用户信息/余额**：`GET /profile` 返回 demo user 与余额（余额为初始值减去累计扣费，保底不小于 0）
  - **创建待扣费记录**：`POST /billing/pending`
    - 支持传入/自动生成 `idempotencyKey`
    - 同一 `idempotencyKey` 幂等返回
  - **扣费**：`POST /billing/deduct`
    - 按 `idempotencyKey` 幂等扣费（已扣过则直接返回）
  - **CORS**：默认允许 `http://localhost:5173`（可通过 `CORS_ALLOW_ORIGIN` 配置）
  - **端口**：默认 `8080`（可通过 `PORT` 配置）

- **前端（React）**：实现基础页面切换（`frontend/src/App.tsx`）
  - **Hash 路由**：通过 `window.location.hash` 在 `HomePage` 与 `ComparePage` 间切换（`#compare`）

- **后端（Python / FastAPI）**：实现 Excel 对比与费用预估接口（`python/FastAPI/`）
  - **基础路由**：`GET /`、`GET /items/{item_id}`（示例接口）
  - **Excel 对比**：`POST /compare/`
    - 上传两份 Excel，自动猜测主键列并输出：减少项/增加项/差异项
  - **导出对比结果**：`POST /compare/export`
    - 将减少/增加/差异分别写入 xlsx 工作表并下载
  - **费用预估**：`POST /feeguest/estimate`
    - 支持按文件体积（MB）或按行数计费，包含最低收费逻辑
  - **依赖**：`python/FastAPI/requirements.txt`（fastapi、uvicorn、pandas、openpyxl、python-multipart、xlrd 等）
  - **CORS**：允许 `http://localhost:5173` / `http://127.0.0.1:5173`

---

## 本地开发快速开始

### 依赖（按 Dockerfile 口径）
- **Go**：1.22+
- **Node.js**：20+
- **Python**：3.13+

### 方式 A：本机直接运行（开发调试）

#### 1) 启动 Python（FastAPI，默认 8000）

```bash
cd python/FastAPI
python -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
export PORT=8000
uvicorn main:app --reload --host 0.0.0.0 --port ${PORT}
```

可选环境变量：
- **`CORS_ALLOW_ORIGINS`**：`"*"` 或逗号分隔的白名单；默认 `http://localhost:5173,http://127.0.0.1:5173`

#### 2) 启动 Go（默认 8080）

```bash
cd gobackend
export PORT=8080
export PY_API_BASE=http://localhost:8000
export CORS_ALLOW_ORIGIN=http://localhost:5173
go run .
```

可选环境变量：
- **`TMP_ROOT`**：对比任务的临时目录；默认 `./tmp`

#### 3) 启动前端（Vite，默认 5173）

```bash
cd frontend
npm ci
npm run dev
```

可选环境变量（Vite）：
- **`VITE_GO_API_BASE`**：默认 `http://localhost:8080`
- **`VITE_PY_API_BASE`**：默认 `http://localhost:8000`

### 方式 B：Docker Compose 一键启动（本地/联调）

```bash
docker compose up -d --build
```

默认端口（见 `docker-compose.yml`）：
- **前端 Nginx**：`http://localhost:8088`
- **Go / Python**：在 compose 网络内互通（Go 通过 `PY_API_BASE=http://py:8000` 调用 Python）

如果需要注入生产环境变量（注意包含敏感信息，谨慎保管）：

```bash
docker compose --env-file env.prod up -d --build
```

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

### Python（Excel 对比 + 导出 + 费用预估）
- `POST /compare/`（multipart：`file1`、`file2`）→ JSON：减少/增加/差异
- `POST /compare/export`（multipart：`file1`、`file2`）→ xlsx 文件流
- `POST /feeguest/estimate`（multipart：`files` 可多文件；query：`pricing_mode=mb|rows` 等）→ 费用估算 JSON

## 生产环境路由约定（Docker + Nginx）

生产环境下前端不直连端口，而是走同域反代（见 `frontend/nginx.conf` 与 `frontend/src/api/paidApi.js`）：
- **`/api/`** → Go `:8080`（前端默认 `GO_API_BASE=/api`）
- **`/py/`** → Python `:8000`（前端默认 `PY_API_BASE=/py`）
- **`/wechatpay/`** → Go（避免被 SPA 的 `try_files` 吞掉）

## 关键环境变量（生产/支付相关）

Go 服务除基础变量外，还支持微信支付相关配置（建议用 `.env` / `env.prod` / CI 变量注入，避免写死在 compose 文件里）：
- **基础**：`PORT`、`CORS_ALLOW_ORIGIN`、`PY_API_BASE`、`TMP_ROOT`
- **微信支付**：`WECHAT_NOTIFY_URL`、`WECHAT_MCHID`、`WECHAT_APPID`、`WECHAT_PAY_APPID`、`WECHAT_CORP_ID`、`WECHAT_API_V3_KEY`、`WECHAT_PLATFORM_PUBLIC_KEY_ID`、`WECHAT_PLATFORM_PUBLIC_KEY`、`WECHAT_ALLOW_WW_APPID`、`WECHAT_MOCK`

证书/密钥文件约定（只列路径，不在文档里放明文密钥）：
- 本地 compose：`./wechatpay` 会挂载到容器 `/app/wechatpay`（只读）
- 生产 compose：`/opt/app/gy/wechatpay/cert` 会挂载到容器 `/app/wechatpay/cert`（只读）

## GitLab CI 自动部署说明

`.gitlab-ci.yml` 目前在 `main` 分支触发 `deploy`，核心命令为：
- `docker compose -p gy up -d --build --remove-orphans`
- `docker image prune -f`

说明：
- `-p gy` 固定 compose 项目名，便于和机器上其它 compose 项目隔离
- 部署机需要已安装 Docker 与 docker compose，并且 runner 对 docker 有权限