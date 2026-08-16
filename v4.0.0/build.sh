#!/bin/sh
# 拉取父仓库最新代码并重建新系统 Web 与 Worker。
set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

if [ ! -f "$script_dir/.env" ]; then
    echo "未找到 new/.env，请先根据 new/.env.example 创建生产配置。" >&2
    exit 1
fi

echo "正在拉取最新代码..."
git -C "$repo_root" pull --ff-only

echo "正在重建并启动新系统..."
cd "$script_dir"
docker compose up -d --build --force-recreate
docker compose ps
