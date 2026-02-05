# main.py
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from typing import Optional
import logging
import sys
import os
import subprocess
import traceback

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


def ensure_dependencies_installed():
    """启动时根据 requirements.txt 安装依赖（无交互模式）。"""
    try:
        req_path = os.path.join(os.path.dirname(__file__), 'requirements.txt')
        if not os.path.exists(req_path):
            logger.warning("requirements.txt 未找到，跳过安装。")
            return
        logger.info("开始安装依赖（若已安装将被跳过）……")
        subprocess.run([sys.executable, '-m', 'pip', 'install', '-r', req_path, '--quiet'], check=False)
        logger.info("依赖检查完成。")
    except Exception:
        logger.warning(f"安装依赖时出现非致命错误：{traceback.format_exc()}")


@app.on_event("startup")
def _on_startup():
    ensure_dependencies_installed()


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