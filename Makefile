.PHONY: dev build run migrate

# 开发模式（需要安装 air）
dev:
	air

# 编译
build:
	go build -o bin/moovie ./cmd/server

# 运行
run: build
	./bin/moovie

# 执行数据库迁移
migrate:
	psql "$(DATABASE_URL)" -f migrations/001_init.up.sql

# 回滚数据库
migrate-down:
	psql "$(DATABASE_URL)" -f migrations/001_init.down.sql

# Docker 启动
docker-up:
	docker-compose up -d

# Docker 停止
docker-down:
	docker-compose down

# 安装开发依赖
setup:
	go mod tidy
	go install github.com/air-verse/air@latest
