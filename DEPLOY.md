## 云端 Docker 自动部署（GitLab）

### 1) 目录与服务
- **web**：`frontend` 构建出的静态站点 + Nginx
  - `/`：前端 SPA
  - `/api/`：反代到 Go（`go:8080`）
- **go**：`gobackend`（包含 `/compare/jobs`、`/wechatpay/notify` 等）

### 2) 本地一键启动（不走 CI）

```bash
cp .env.example .env
# 填好 WECHAT_NOTIFY_URL（必须）、以及微信证书/私钥 pem
docker compose up -d --build
```

浏览器打开 `http://localhost/`。

### 3) 生产启动（服务器上）

服务器需要安装：
- Docker + Docker Compose v2

在服务器上准备一个目录（例如 `/opt/guagnyang`），放：
- `docker-compose.prod.yml`
- `.env` 或 `env.prod`（环境变量文件：**请自行在服务器上创建，不要提交到 git**；可参考仓库中的 `env.prod.example`）
- `wechatpay/`（证书与密钥目录，建议只放在服务器，不要提交到仓库）

然后执行：

```bash
cd /opt/guagnyang
# 如果你的文件叫 .env（默认行为），直接：
docker compose -f docker-compose.prod.yml up -d
#
# 如果你的文件叫 env.prod（或其他名字），用：
# docker compose --env-file env.prod -f docker-compose.prod.yml up -d
```

### 4) GitLab CI 自动部署

CI 文件是根目录的 `.gitlab-ci.yml`，默认只对 `main` 分支触发。

在 GitLab 项目里配置 CI/CD 变量（Settings → CI/CD → Variables）：
- **DEPLOY_HOST**：服务器 IP 或域名
- **DEPLOY_USER**：服务器用户名
- **DEPLOY_PATH**：部署目录（如 `/opt/guagnyang`）
- **DEPLOY_SSH_KEY**：用于部署的私钥（建议创建专用 deploy key）

以及微信配置（建议用 masked/protected 变量）：
- **WECHAT_NOTIFY_URL**：`https://你的域名/wechatpay/notify`（必须，HTTPS 且不可带 query）
  - Nginx 已单独反代 `/wechatpay/` 到 Go，避免被前端 SPA 路由吞掉
  - 也可用 `https://你的域名/api/wechatpay/notify`（通过 `/api/` 反代，路径会在转发时去掉 `/api` 前缀）
- （可选）**WECHAT_MCHID**：不填则会从 `wechatpay/cert/<mchid>_YYYYMMDD_cert.zip` 推断
 - （可选）**WECHAT_PAY_APPID**：微信支付请求体里的 `appid`（常见为 wx...）。不填则会回退读 **WECHAT_APPID**
 - （可选）**WECHAT_APPID**：兼容旧配置；如果你这里填的是企业微信 CorpID（ww...），建议额外设置 **WECHAT_PAY_APPID**
 - （可选）**WECHAT_CORP_ID**：企业微信 CorpID（ww...），目前仅用于配置留档/未来扩展，不参与微信支付 v3 下单请求
- （可选）**WECHAT_API_V3_KEY**：不填则会从 `wechatpay/apikey/apikey.txt` 推断（生产建议用变量注入）
 - （可选）平台验签也支持“平台公钥”模式（微信商户平台可下载）：
   - **WECHAT_PLATFORM_PUBLIC_KEY_ID**：形如 `PUB_KEY_ID_...`
   - **WECHAT_PLATFORM_PUBLIC_KEY**：平台 RSA 公钥 PEM 内容（建议用 CI 变量/secret 注入）

### 5) 证书/密钥文件约定（Go 端）
Go 端会从挂载的 `wechatpay/` 目录读取：
- `wechatpay/cert/merchant_key.pem`
- `wechatpay/cert/merchant_cert.pem`
- `wechatpay/cert/platform_cert.pem`

注意：你目前仓库里是 `cert.zip`，需要在服务器上解压/导出成上述 pem 文件名。

---

## ACK / Kubernetes 部署：GitLab Variables 注入微信支付文件（推荐）

你现在的 ACK 部署会在 `deploy_ack` 阶段：

- 创建/更新 `gy/wechatpay-env`（短文本环境变量）
- 创建/更新 `gy/wechatpay-cert`（证书/密钥文件），并挂载到 Go 容器 `/app/wechatpay/cert`

### GitLab Variables 怎么“塞文件”

- **Type=File 不是上传文件**：它是把你粘贴到 Value 的内容在 Job 运行时写成临时文件，变量值是该文件路径。
- 你如果习惯先把文件做 **base64** 再粘贴到 File 变量，也可以；流水线会尝试自动 `base64 -d` 回原文件（并在注入前做 BEGIN/END 校验，失败会给出明确错误）。

### 建议配置的变量

#### A) 短文本（Type=Variable）

- `WECHAT_NOTIFY_URL`
- `WECHAT_MCHID`
- `WECHAT_PAY_APPID`
- `WECHAT_API_V3_KEY`
- `WECHAT_PLATFORM_PUBLIC_KEY_ID`（可选）
- `WECHAT_MOCK`（可选）

#### B) 文件（Type=File）

- `WECHAT_PUB_KEY_PEM_FILE`：平台公钥 `pub_key.pem`（完整 PEM，含 BEGIN/END）
- `WECHAT_PLATFORM_CERT_PEM_FILE`：平台证书 `platform_cert.pem`（可选，证书模式）
- `WECHAT_CERT_TXT_FILE`：`cert.txt`（可选，仅排查/兼容）

#### C) 商户证书 zip（二选一）

推荐用 base64 变量（Type=Variable）：

- `WECHAT_MERCHANT_CERT_ZIP_NAME`：`<mchid>_YYYYMMDD_cert.zip`
- `WECHAT_MERCHANT_CERT_ZIP_B64`：zip 的 base64（单行）

也支持你把 zip 的 base64 粘贴进 File 变量（Type=File）：

- `WECHAT_MERCHANT_CERT_ZIP_FILE`
- `WECHAT_MERCHANT_CERT_ZIP_NAME`

