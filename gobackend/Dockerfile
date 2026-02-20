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
RUN go build -o /out/app .

FROM alpine:3.20
WORKDIR /app

# Install unoconvert client (no LibreOffice needed) to talk to xlsconvert/unoserver.
# Use a venv to avoid system-python packaging constraints.
ENV PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
RUN apk add --no-cache ca-certificates python3 py3-pip && \
    python3 -m venv /opt/unoserver-client && \
    /opt/unoserver-client/bin/pip install --no-cache-dir --upgrade pip && \
    /opt/unoserver-client/bin/pip install --no-cache-dir unoserver
ENV PATH="/opt/unoserver-client/bin:${PATH}"

COPY --from=build /out/app /app/app

EXPOSE 8080
ENV PORT=8080

CMD ["/app/app"]

