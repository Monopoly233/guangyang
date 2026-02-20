# main.py
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from typing import Optional
import logging
import sys
import os

# 创建一个 FastAPI 实例
app = FastAPI()


def _cors_origins():
    """
    CORS_ALLOW_ORIGINS:
    - "*" 允许所有
    - 逗号分隔： "https://a.com,https://b.com"
    """
    v = os.getenv("CORS_ALLOW_ORIGINS", "http://localhost:5173,http://127.0.0.1:5173").strip()
    if v == "*":
        return ["*"]
    return [x.strip() for x in v.split(",") if x.strip()]


# 允许前端源访问（需在应用启动前添加中间件）
app.add_middleware(
    CORSMiddleware,
    allow_origins=_cors_origins(),
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# 日志配置
logger = logging.getLogger(__name__)
logger.setLevel(logging.DEBUG)
console_handler = logging.StreamHandler(sys.stdout)
console_handler.setLevel(logging.DEBUG)
formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
console_handler.setFormatter(formatter)
if not logger.handlers:
    logger.addHandler(console_handler)


from compare_api import compare_router
from feeguest import feeguest_router

# 根路径测试
@app.get("/")
def read_root():
    return {"Hello": "World"}


@app.get("/items/{item_id}")
def read_item(item_id: int, q: Optional[str] = None):
    return {"item_id": item_id, "q": q}


# 挂载 compare_api 子路由
app.include_router(compare_router)
app.include_router(feeguest_router)