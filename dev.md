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