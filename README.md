# Moovie

聚合电影搜索网站 - 基于 **Golang + Gin + htmx + PostgreSQL**

## 特性

- 🔍 多源聚合搜索
- 📺 在线播放（m3u8）
- ❤️ 收藏功能（htmx 无刷新）
- 📝 观影历史（localStorage + 服务端同步）
- 🎯 SEO 友好（服务端渲染）
- 🔐 JWT 认证（HttpOnly Cookie）

## 快速开始

### 环境要求

- Go 1.21+
- PostgreSQL 15+

### 本地开发

```bash
# 1. 克隆项目
git clone <repo-url>
cd moovie

# 2. 复制环境变量
cp .env.example .env
# 编辑 .env 修改数据库连接

# 3. 创建数据库并执行迁移
createdb moovie
psql moovie -f migrations/001_init.up.sql

# 4. 安装依赖
go mod tidy

# 5. 启动开发服务器
go run ./cmd/server
# 或使用 air 热重载
make dev
```

访问 http://localhost:8080

### Docker 部署

```bash
docker-compose up -d
```

## 项目结构

```
moovie/
├── cmd/server/         # 入口
├── internal/
│   ├── config/         # 配置
│   ├── middleware/     # 中间件（JWT、CORS、日志）
│   ├── handler/        # HTTP 处理器
│   ├── repository/     # 数据访问层
│   └── model/          # 数据模型
├── web/
│   ├── templates/      # Go HTML 模板
│   └── static/         # CSS、JS
├── migrations/         # 数据库迁移
└── docker-compose.yml
```

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端 | Gin |
| 数据库 | PostgreSQL |
| 前端交互 | htmx |
| 播放器 | hls.js |
| 认证 | JWT |

## 许可证

MIT
