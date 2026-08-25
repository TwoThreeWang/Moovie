# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
# 构建阶段只生成静态链接的 Web 和 Worker 二进制，不把 Go 工具链带入运行镜像。
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/moovie-web ./cmd/web
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/moovie-worker ./cmd/worker

# 运行阶段使用非 root 用户和最小依赖，降低镜像体积与容器权限。
FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S moovie \
    && adduser -S -G moovie moovie
WORKDIR /app
COPY --from=builder /out/moovie-web /out/moovie-worker ./
COPY --from=builder /src/web ./web

ENV PORT=5008 WEB_ROOT=/app/web
EXPOSE 5008
USER moovie
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget --quiet --spider http://127.0.0.1:5008/health || exit 1
ENTRYPOINT ["./moovie-web"]
