# Moovie 影牛

> 搜全网，只为你想看的那一部。

Moovie 是一个使用 Go 构建的影视聚合与观影管理应用。它将多源资源搜索、HLS 播放、影视资料、个性化推荐、观影记录和社区互动放在同一个轻量的 Web 应用中。

- 在线示例：[moovie.c2v2.com](https://moovie.c2v2.com/)
- 默认端口：`5007`
- 当前架构：Go 模块化单体 + PostgreSQL + 服务端模板

![Moovie 架构概览](Moovie_架构概览.png)

> 架构图展示的是核心链路。当前项目还包含弹幕、豆瓣同步、月度报告、广场互动和采集健康度等能力。

## 功能

### 搜索与播放

- 并发检索多个苹果 CMS 兼容资源站，并将结果统一展示。
- 本地结果优先返回，后台异步刷新；冷门关键词首次搜索会同步拉取远端数据。
- 内置 HLS.js 播放器，支持电影、剧集、m3u8 测试工具和 IPTV。
- 自动记录播放进度，登录后可在多端同步观影历史。
- 提供 TVBox 配置及 VOD 接口。

### 发现与推荐

- 从豆瓣获取电影资料、评分、短评和热门内容。
- 从 TMDB 补充 IMDb 映射与电影剧照。
- 使用 Ollama 生成 768 维向量，并通过 pgvector 提供相似电影和个性化推荐。
- 支持想看、看过、评分、短评、搜索趋势和分类发现。

### 用户与社区

- JWT + HttpOnly Cookie 登录认证和滑动续期。
- 豆瓣账号绑定、全量同步、增量同步和任务状态展示。
- 公开观影主页、月度观影小记和分享页面。
- 广场动态、活跃榜、短评点赞与回复。
- 外部弹幕聚合和站内弹幕；站内弹幕发送需要登录。

### 管理与运维

- 管理资源网、用户、反馈、版权关键词和分类过滤规则。
- 记录采集站点成功、空返回、超时和错误状态。
- 连续失败自动熔断，冷却后放行探针恢复；全部站点熔断时自动兜底。
- 定时清理过期资源、搜索日志和采集统计。

## 技术栈

| 领域 | 技术 |
| --- | --- |
| 语言 | Go 1.25.4 |
| Web | Gin、服务端 HTML 模板、multitemplate |
| 数据库 | PostgreSQL 15+、GORM、pgvector、pg_trgm |
| 前端 | htmx、原生 CSS、原生 JavaScript、PWA |
| 播放器 | HLS.js、ArtPlayer 弹幕插件 |
| 推荐 | Ollama、768 维文本向量、pgvector HNSW |
| 外部数据 | 苹果 CMS API、豆瓣、TMDB、Cloudflare AI Gateway |
| 缓存与并发 | 进程内 LRU、singleflight、context 超时 |
| 部署 | Docker、Docker Compose |

## 工作方式

```text
浏览器 / TVBox
       │
       ▼
Gin 路由与 Handler
       │
       ├── Service：搜索、采集、推荐、同步、月报、健康度
       │
       ├── Repository：GORM 与定制 SQL
       │
       └── PostgreSQL：业务数据、搜索缓存数据、pgvector 向量

外部服务：资源站 API / 豆瓣 / TMDB / Ollama / AI Gateway / 弹幕服务
```

搜索采用“本地优先、远端刷新”的策略：数据库有结果时直接响应并异步刷新；没有结果时，在总超时内并发请求已启用的资源站。电影详情与推荐链路使用豆瓣/TMDB 元数据和 Ollama 向量，与资源站搜索相互独立。

## 快速开始

### 1. 准备环境

必需：

- Go `1.25.4`
- PostgreSQL `15+`
- PostgreSQL `vector` 扩展

按功能选配：

- `pg_trgm`：加速标题模糊搜索；创建失败不会阻止应用启动。
- Ollama + `quentinz/bge-base-zh-v1.5`：生成 768 维电影向量。电影资料补全和向量推荐依赖它。
- TMDB Token：补充剧照和部分元数据。
- Cloudflare AI Gateway：生成更高语义密度的向量输入文本；未配置时使用本地元数据拼接结果。
- 弹幕聚合服务：未配置 `DANMU_API_BASE` 时，弹幕功能自动关闭。

### 2. 获取代码

```bash
git clone https://github.com/TwoThreeWang/Moovie.git
cd Moovie
```

### 3. 创建配置

```bash
cp .env.example .env
```

至少修改以下配置：

```dotenv
APP_ENV=development
APP_SECRET=请替换为足够长的随机字符串

DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=你的数据库密码
DB_NAME=moovie
DB_SSLMODE=disable
DB_TIMEZONE=Asia/Shanghai
```

应用启动时会尝试创建 `vector`、`pg_trgm` 扩展，自动迁移数据表并创建索引，因此数据库用户需要相应权限。生产环境建议先在数据库侧安装并启用扩展。

### 4. 启动

```bash
go mod download
go run ./cmd/server
```

访问 [http://localhost:5007](http://localhost:5007)，或检查健康接口：

```bash
curl http://localhost:5007/health
```

程序使用相对路径加载 `web/templates` 和 `web/static`，请从项目根目录运行。

### 5. 配置管理员和资源站

注册首个账号后，可在数据库中将其设为管理员：

```sql
UPDATE users SET role = 'admin' WHERE email = 'your-email@example.com';
```

重新登录后进入 `/admin/sites`，添加支持苹果 CMS 接口格式的资源站。不要将数据库、管理后台或上游服务凭据暴露到公网。

## 环境变量

完整示例见 [`.env.example`](.env.example)。

### 应用与数据库

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | `5007` | HTTP 监听端口 |
| `APP_ENV` | `development` | 设为 `production` 时启用 Gin Release 模式 |
| `SITE_NAME` | `Moovie` | 站点名称 |
| `SITE_URL` | `http://localhost:5007` | 生成 canonical、分享链接等内容的公开地址 |
| `HTTP_WRITE_TIMEOUT_SECONDS` | `30` | HTTP 写超时，应覆盖整轮采集预算 |
| `DB_HOST` | `localhost` | PostgreSQL 主机名 |
| `DB_PORT` | `5432` | PostgreSQL 端口 |
| `DB_USER` | `postgres` | PostgreSQL 用户 |
| `DB_PASSWORD` | `postgres` | PostgreSQL 密码；生产环境必须修改 |
| `DB_NAME` | `moovie` | 数据库名 |
| `DB_SSLMODE` | `disable` | PostgreSQL SSL 模式 |
| `DB_TIMEZONE` | `Asia/Shanghai` | 数据库和应用时区 |
| `APP_SECRET` | 内置占位值 | JWT 与 Session 签名密钥；生产环境必须修改 |
| `JWT_EXPIRY_HOURS` | `72` | JWT 有效期；使用超过一半后自动续期 |

### 采集、推荐与弹幕

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `SEARCH_BREAKER_ENABLED` | `true` | 是否跳过处于熔断状态的搜索站点 |
| `DOUBAN_PROXY` | 空 | 豆瓣透明代理地址；设为 `None` 等同于关闭 |
| `TMDB_API_TOKEN` | 空 | TMDB API Token |
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama 地址 |
| `OLLAMA_MODEL` | `quentinz/bge-base-zh-v1.5` | Embedding 模型，输出必须为 768 维 |
| `CF_GATEWAY_URL` | 空 | Cloudflare AI Gateway 的 OpenAI 兼容入口 |
| `CF_API_TOKEN` | 空 | Cloudflare AI Gateway Token |
| `CF_AI_MODEL` | 项目默认模型 | AI Gateway 模型名 |
| `DANMU_API_BASE` | 空 | 弹幕服务基础地址，包含 Token 路径；留空关闭弹幕 |
| `DANMU_API_TOKEN` | `changeme` | Compose 中弹幕容器使用的 Token，生产环境必须修改 |

`.env.example` 中保留了 Gemini 配置，但当前电影语义生成链路没有启用 Gemini fallback；实际使用的是 Cloudflare AI Gateway 或本地元数据降级。

标准的 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY` 和 `NO_PROXY` 会被外部 HTTP 请求使用。

## Docker Compose 部署

当前 [`docker-compose.yml`](docker-compose.yml) 包含：

- `app`：Moovie 应用。
- `danmu-api`：弹幕聚合服务。

它不包含 PostgreSQL 和 WARP，并依赖已存在的外部网络 `postgres_default`。

### 1. 准备外部网络和 PostgreSQL

确保 PostgreSQL 容器已经连接到 `postgres_default`，并将 `.env` 中的 `DB_HOST` 设置为它在该网络中的 DNS 名称。

如果网络尚不存在，可创建：

```bash
docker network create postgres_default
```

PostgreSQL 镜像必须提供 pgvector 扩展，例如使用 pgvector 官方维护的 PostgreSQL 镜像。

### 2. 处理 WARP 配置

Compose 默认将代理地址写为 `cloudflare-warp:1080`，但没有创建这个服务：

- 已有 WARP 容器：将它连接到 `postgres_default`，并确保网络内可通过 `cloudflare-warp` 访问 SOCKS5 端口 `1080`。
- 不使用 WARP：注释 `app` 和 `danmu-api` 中的 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY` 配置。

否则豆瓣、TMDB、AI 和资源站等外部请求可能全部失败。

### 3. 配置弹幕服务

当前 Compose 会把 `DANMU_API_TOKEN` 传给 `danmu-api`，但不会自动为 `app` 拼接 `DANMU_API_BASE`。如需弹幕功能，请在 `.env` 中显式设置两者，并保持 Token 一致：

```dotenv
DANMU_API_TOKEN=请替换为随机Token
DANMU_API_BASE=http://danmu-api:9321/请替换为随机Token
```

不需要弹幕时将 `DANMU_API_BASE` 留空即可。

### 4. 构建并启动

```bash
docker compose up -d --build
docker compose ps
docker compose logs -f app
```

也可以使用仓库中的部署脚本。它会先执行 `git pull`，再重建容器：

```bash
./deploy.sh
```

## 采集健康度与熔断

系统按“站点 + 小时”记录调用结果，并在后台资源网列表展示近 24 小时的成功率、空返回率和平均耗时。数据默认保留 7 天。

一次调用会被分为：

- `ok`：正常返回至少一条结果。
- `empty`：请求和解析成功，但没有结果。
- `timeout`：请求超时。
- `error`：网络、HTTP 状态或解析错误。

连续 3 次超时或错误后，站点进入 5 分钟冷却期。冷却期间会放行单个探针；探针成功后立即恢复。当所有站点都处于熔断状态时，系统会兜底放行全部站点，避免直接返回空搜索结果。

熔断只应用于多站点搜索，不会拦截用户已经选定的播放详情请求。

若站点最近一小时满足告警条件，系统会在后台反馈页创建“系统告警”，同一站点 24 小时内最多一次。

## 项目结构

```text
Moovie/
├── cmd/server/                  # 应用入口与生命周期
├── internal/
│   ├── config/                  # 环境配置
│   ├── handler/                 # 页面、HTMX、API 与后台 Handler
│   ├── middleware/              # 认证、安全头、CORS 与日志
│   ├── model/                   # GORM 数据模型
│   ├── repository/              # 数据访问与 SQL
│   ├── router/                  # 路由和模板注册
│   ├── service/                 # 搜索、采集、推荐、同步与定时任务
│   └── utils/                   # 缓存、HTTP、AI、解析与异步任务
├── web/
│   ├── static/                  # CSS、JavaScript、PWA 与图片
│   └── templates/               # layouts、pages 与 HTMX partials
├── doc/                         # 调研与实现说明
├── Dockerfile
├── docker-compose.yml
├── Makefile
└── deploy.sh
```

## 开发与验证

```bash
# 格式化
gofmt -w $(find cmd internal -name '*.go')

# 测试
go test ./...

# 静态检查
go vet ./...

# 构建
go build ./...
```

常用 Make 目标：

```bash
make build
make run
make docker-up
make docker-down
```

## 数据与使用说明

- Moovie 不在仓库中内置影视资源，搜索与播放结果来自管理员配置的第三方站点。
- 第三方接口的可用性、内容和版权状态不受本项目控制。
- 请遵守所在地法律法规、上游服务条款和内容版权要求。
- 不要提交 `.env`、数据库备份、API Token 或用户数据。

## 许可证

当前仓库尚未包含独立的 `LICENSE` 文件。若要对外开源、再分发或用于商业场景，请先补充并确认许可证。

---

由 [TwoThreeWang](https://github.com/TwoThreeWang) 构建。
