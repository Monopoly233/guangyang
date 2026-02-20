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
COPY --from=build /out/app /app/app

EXPOSE 8080
ENV PORT=8080

CMD ["/app/app"]

