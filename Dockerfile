# 构建阶段
FROM golang:1.21-alpine AS builder

WORKDIR /app

# 安装依赖
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并编译
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o moovie ./cmd/server

# 运行阶段
FROM alpine:latest

WORKDIR /app

# 复制编译产物
COPY --from=builder /app/moovie .
COPY --from=builder /app/web ./web

# 暴露端口
EXPOSE 8080

# 启动命令
CMD ["./moovie"]
