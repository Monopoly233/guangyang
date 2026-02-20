FROM golang:1.24.3-alpine AS build

WORKDIR /src
ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct \
    GOSUMDB=sum.golang.google.cn

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/xlsconvert ./cmd/xlsconvert

# Runtime: LibreOffice for .xls conversion
FROM debian:bookworm-slim
WORKDIR /app
COPY --from=build /out/xlsconvert /app/xlsconvert

ENV DEBIAN_FRONTEND=noninteractive
# Use Aliyun Debian mirrors for faster/stabler installs in CN
RUN sed -i \
      -e 's@http://deb.debian.org/debian@http://mirrors.aliyun.com/debian@g' \
      -e 's@https://deb.debian.org/debian@http://mirrors.aliyun.com/debian@g' \
      -e 's@http://security.debian.org/debian-security@http://mirrors.aliyun.com/debian-security@g' \
      -e 's@https://security.debian.org/debian-security@http://mirrors.aliyun.com/debian-security@g' \
      /etc/apt/sources.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      python3 \
      python3-uno \
      unoconv \
      libreoffice-calc \
      libreoffice-core \
      libreoffice-common \
      fontconfig \
      fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*

EXPOSE 8090
ENV PORT=8090

CMD ["/app/xlsconvert"]

