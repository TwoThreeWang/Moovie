#!/bin/bash

# 脚本名称: deploy.sh
# 功能: 拉取最新代码、构建并启动 Docker 容器，映射 .env 文件

# 设置颜色输出
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # 无颜色

echo -e "${YELLOW}开始执行部署流程...${NC}"

# 1. 检查 .env 文件是否存在
if [ ! -f .env ]; then
    echo -e "${RED}警告: 当前目录下未找到 .env 文件！${NC}"
    if [ -f .env.example ]; then
        echo -e "${YELLOW}检测到 .env.example，请以此为模板创建 .env 文件后再执行此脚本。${NC}"
    fi
    exit 1
fi

# 2. 拉取最新代码
echo -e "${GREEN}步骤 1: 正在拉取最新代码...${NC}"
git pull
if [ $? -ne 0 ]; then
    echo -e "${RED}代码拉取失败，请检查网络或 Git 配置。${NC}"
    exit 1
fi

# 3. 停止当前容器 (可选，但建议在某些更改下显式先停掉)
# echo -e "${GREEN}正在停止旧容器...${NC}"
# docker-compose down

# 4. 构建并启动容器
echo -e "${GREEN}步骤 2: 正在构建镜像并启动容器...${NC}"
docker compose up -d --build

if [ $? -eq 0 ]; then
    echo -e "${GREEN}部署成功！${NC}"
    echo -e "${YELLOW}当前运行中的容器状态:${NC}"
    docker compose ps
else
    echo -e "${RED}部署失败，请检查 docker-compose 日志。${NC}"
    exit 1
fi
