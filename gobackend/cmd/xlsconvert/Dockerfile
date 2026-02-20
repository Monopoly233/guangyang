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

RUN apt-get update && \
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

