#!/bin/sh
# 拉取最新代码并重建 Web 与 Worker。
set -eu

cd "$(dirname "$0")"

if [ ! -f .env ]; then
    echo "未找到 .env，请先根据 .env.example 创建生产配置。" >&2
    exit 1
fi

echo "正在拉取最新代码..."
git pull --ff-only

echo "正在重建并启动新系统..."
docker compose up -d --build --force-recreate
docker compose ps
