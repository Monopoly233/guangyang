# Python FastAPI Dockerfile
FROM python:3.13-slim

WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1

COPY requirements.txt ./

# 使用清华大学镜像源 (TUNA) 替换官方源
RUN pip install --no-cache-dir -r requirements.txt \
    -i https://pypi.tuna.tsinghua.edu.cn/simple

COPY . .

EXPOSE 8000
ENV PORT=8000

 # Default 1 worker (safe under low CPU); override via UVICORN_WORKERS.
 CMD ["bash", "-lc", "uvicorn main:app --host 0.0.0.0 --port ${PORT} --workers ${UVICORN_WORKERS:-1}"]