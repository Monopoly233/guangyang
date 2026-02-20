ARG XLSCONVERT_RUNTIME_BASE=ghcr.io/unoconv/unoserver-docker:0.4.4

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
# Use prebuilt image to avoid apt instability in CI.
# Includes LibreOffice + unoserver + unoconvert + lots of fonts.
FROM ${XLSCONVERT_RUNTIME_BASE}
WORKDIR /app
COPY --from=build /out/xlsconvert /app/xlsconvert

EXPOSE 8090
ENV PORT=8090

CMD ["/app/xlsconvert"]

