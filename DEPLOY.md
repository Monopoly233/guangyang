## 云端 Docker 自动部署（GitLab）

### 1) 目录与服务
- **web**：`frontend` 构建出的静态站点 + Nginx
  - `/`：前端 SPA
  - `/api/`：反代到 Go（`go:8080`）
  - `/py/`：反代到 Python（`py:8000`）
- **go**：`gobackend`（包含 `/compare/jobs`、`/wechatpay/notify` 等）
- **py**：`python/FastAPI`（Excel 对比导出等）

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
- `.env`（环境变量文件；也可以继续用本仓库的 `env.prod`，启动时用 `--env-file` 指定）
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

