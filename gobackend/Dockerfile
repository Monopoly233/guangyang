FROM golang:1.24.3-alpine AS build

WORKDIR /src

# CI/Runners in CN may not reach proxy.golang.org reliably.
ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/go-api . && \
    go build -o /out/compare-worker ./cmd/compare-worker && \
    go build -o /out/payment-worker ./cmd/payment-worker

FROM alpine:3.20
WORKDIR /app

# Install unoconvert client (no LibreOffice needed) to talk to xlsconvert/unoserver.
# Use a venv to avoid system-python packaging constraints.
ENV PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
RUN apk add --no-cache ca-certificates python3 py3-pip && \
    python3 -m venv /opt/unoserver-client && \
    /opt/unoserver-client/bin/pip install --no-cache-dir --upgrade pip && \
    # 重要：客户端/服务端必须版本一致，否则 XML-RPC 参数签名会不匹配（你集群里的 xlsconvert 当前是 3.4）
    /opt/unoserver-client/bin/pip install --no-cache-dir "unoserver==3.4"
ENV PATH="/opt/unoserver-client/bin:${PATH}"

COPY --from=build /out/go-api /app/go-api
COPY --from=build /out/compare-worker /app/compare-worker
COPY --from=build /out/payment-worker /app/payment-worker

EXPOSE 8080
ENV PORT=8080

CMD ["/app/go-api"]

