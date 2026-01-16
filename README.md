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
├── cmd/server/         # 应用程序入口点
├── internal/           # 内部包（不对外暴露）
│   ├── config/         # 配置管理
│   ├── handler/        # HTTP 请求处理器（API、Admin、通用）
│   ├── middleware/     # 中间件（JWT认证、CORS、日志）
│   ├── model/          # 数据模型定义
│   ├── repository/     # 数据访问层（数据库操作）
│   ├── router/         # 路由定义
│   ├── service/        # 业务逻辑层（爬虫、搜索服务）
│   └── utils/          # 工具包（缓存、HTTP客户端、响应格式）
├── web/                # Web前端资源
│   ├── static/         # 静态文件（CSS、JS、图片）
│   └── templates/      # Go HTML模板（Layouts、Pages、Partials）
├── migrations/         # 数据库迁移文件
├── Dockerfile          # Docker 镜像构建配置
├── docker-compose.yml  # Docker Compose 容器编排配置
└── Makefile            # 项目自动化命令
```

## 技术栈

| 组件 | 技术 |
|------|------|
| 后端框架 | Gin (Go Web Framework) |
| 数据库 | PostgreSQL |
| 前端交互 | htmx (动态Web交互) |
| 播放器 | hls.js (HLS视频播放) |
| 认证 | JWT (JSON Web Token) |
| 内存缓存 | gocache (本地内存缓存) |
| 反爬虫 | 自定义HTTP客户端，模拟浏览器行为 |

## 核心功能

### 后端功能
- **用户系统**: 注册、登录、JWT认证、用户管理
- **电影搜索**: 豆瓣电影搜索API集成，带缓存机制
- **收藏管理**: 用户收藏电影功能
- **观看历史**: 用户观看历史记录和同步
- **反馈系统**: 用户反馈提交功能
- **管理后台**: 用户管理、爬虫管理、数据统计

### 前端功能
- **搜索建议**: 实时搜索下拉联想，支持键盘导航
- **响应式设计**: 适配移动端和桌面端
- **SSR渲染**: 服务端渲染，SEO友好
- **动态交互**: 使用htmx实现无刷新页面更新

### 技术特点
- **统一API响应**: 所有JSON API使用统一格式返回
- **反爬虫机制**: HTTP客户端模拟真实浏览器行为
- **缓存优化**: 5分钟TTL的内存缓存，减少API调用
- **错误处理**: 完善的错误处理和用户提示

## 许可证

MIT
