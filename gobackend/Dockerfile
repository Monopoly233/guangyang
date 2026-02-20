FROM registry.cn-hangzhou.aliyuncs.com/library/golang:1.22-alpine AS build

WORKDIR /src
COPY . .

RUN go env -w CGO_ENABLED=0 && \
    go build -o /out/app .

FROM registry.cn-hangzhou.aliyuncs.com/library/alpine:3.20
WORKDIR /app
COPY --from=build /out/app /app/app

EXPOSE 8080
ENV PORT=8080

CMD ["/app/app"]

