# 🎬 Moovie

> **搜全网，只为你想看的那一部。**

Moovie 是一款基于 **Golang** 开发的聚合影视搜索工具。它通过整合多源搜索、智能推荐和极致的响应式设计，为你提供一个干净、高效的观影入口。

---

## 🚀 特性亮点

- 🔍 **多源聚合搜索**：一键横跨多个影视资源站点，告别碎片化搜索。
- 🧠 **智能推荐 (AI Powered)**：基于 `pgvector` 矢量相似度算法，根据你的观影历史精准推荐。
- 📱 **PWA 支持**：原生级别的移动端体验，可添加到主屏幕，支持离线图标。
- 📺 **流畅播放**：内置 HLS.js 播放器，支持 m3u8 高清流媒体播放。
- 📝 **观影记忆**：自动同步 LocalStorage 到云端，多端进度无缝覆盖。
- ⚡ **极致速度**：基于 Gin + htmx，实现无刷新页面更新，响应迅速。
- 🔐 **安全可靠**：JWT + HttpOnly Cookie 认证，保障隐私安全。

---

## 🛠️ 技术栈

| 领域 | 技术方案 |
| :--- | :--- |
| **语言** | Go (Golang) 1.21+ |
| **Web 框架** | [Gin](https://github.com/gin-gonic/gin) |
| **数据库** | [PostgreSQL](https://www.postgresql.org/) + [pgvector](https://github.com/pgvector/pgvector) |
| **前端交互** | [htmx](https://htmx.org/) (High Power Tools for HTML) |
| **前端样式** | Vanilla CSS (Glassmorphism Design) |
| **播放器** | [hls.js](https://github.com/video-dev/hls.js/) |
| **缓存** | gocache (Local Memory Cache) |

---

## 📦 快速开始

### 环境依赖

- **Go**: v1.21 或更高版本
- **PostgreSQL**: v15+ (需安装 `pgvector` 扩展)

### 本地运行

1. **克隆仓储**
   ```bash
   git clone https://github.com/TwoThreeWang/Moovie.git
   cd Moovie
   ```

2. **环境配置**
   ```bash
   cp .env.example .env
   # 请在 .env 中配置你的数据库连接信息
   ```

3. **安装依赖并启动**
   ```bash
   go mod tidy
   go run ./cmd/server
   ```
   🚀 访问 [http://localhost:5007](http://localhost:5007)

### Docker 快捷部署

```bash
docker-compose up -d
```

---

## 📂 目录结构

```text
moovie/
├── cmd/server/         # 🚀 程序入口点
├── internal/           # 核心业务逻辑
│   ├── handler/        # 🎮 HTTP 处理器 (API/Admin/View)
│   ├── model/          # 📦 数据模型与 Schema
│   ├── repository/     # 🗄️ 数据库操作 (DAL)
│   ├── service/        # ⚙️ 业务逻辑 (搜索/爬虫/推荐)
│   └── middleware/     # 🛡️ JWT/CORS/Auth 中间件
├── web/                # 前端资源
│   ├── static/         # 🎨 CSS/JS/Assets
│   └── templates/      # 🧬 Go HTML 模板 (Layouts/Partials)
└── ...
```

---

## 🛡️ 许可证

本项目采用 **MIT License**。详情请参阅 [LICENSE](LICENSE) 文件。

---

**由 [TwoThreeWang](https://github.com/TwoThreeWang) 倾情打造。**
