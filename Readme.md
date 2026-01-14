好的，这是一个非常清晰和全面的网站功能规划。基于您提供的页面列表和技术栈（Golang+Gin 后端，HTMX 前端），我们可以设计一个逻辑清晰、可扩展性强且对 SEO 友好的网站结构。

HTMX 的核心优势在于它允许后端直接返回 HTML 片段来更新页面的特定部分，而不是整个页面刷新或依赖复杂的前端 JavaScript 框架。我们的设计将充分利用这一特性。

### 1. 整体目录结构（后端 Golang 项目）

建议采用模块化的方式组织您的 Go 项目代码，方便维护和扩展。

```
/moovie
├── /cmd                   # 程序入口
│   └── /server
│       └── main.go        # Gin服务启动
├── /internal              # 内部业务逻辑，不对外暴露
│   ├── /handler           # HTTP处理器 (Gin Handlers)
│   │   ├── index.go         # 首页
│   │   ├── search.go        # 搜索相关
│   │   ├── movie.go         # 详情、播放
│   │   ├── user.go          # 用户中心、认证
│   │   ├── discover.go      # 发现、排行
│   │   └── admin.go         # 后台管理
│   ├── /service           # 业务逻辑层 (处理复杂逻辑)
│   │   ├── search_aggregator.go # 聚合搜索服务
│   │   ├── user_service.go    # 用户服务
│   │   └── ...
│   ├── /model             # 数据模型 (structs)
│   │   ├── movie.go
│   │   └── user.go
│   ├── /repository        # 数据访问层 (数据库、缓存交互)
│   │   └── ...
│   └── /util              # 工具函数
├── /pkg                   # 可供外部使用的库
│   └── /sitemap_generator # 站点地图生成器
├── /web
│   ├── /template          # HTMX 模板 (HTML)
│   │   ├── /layouts         # 基础布局 (header, footer, sidebar)
│   │   ├── /pages           # 完整页面模板
│   │   │   ├── index.html
│   │   │   ├── search.html
│   │   │   ├── detail.html
│   │   │   └── ...
│   │   └── /components      # HTMX 可替换的组件/片段
│   │       ├── search_results.html
│   │       ├── movie_list_item.html
│   │       ├── player_options.html
│   │       └── ...
│   └── /static            # 静态资源
│       ├── /css
│       ├── /js              # (少量必要的JS，如HLS播放器)
│       └── /img
├── go.mod
└── config.yaml            # 配置文件
```

---

### 2. URL 路由设计 (Gin Router)

一个 RESTful 且对用户友好的 URL 结构至关重要。

| 页面/功能 | HTTP 方法 | URL 路径 | Gin Handler | HTMX 交互 | 描述 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **核心浏览流** | | | | | |
| 首页 | `GET` | `/` | `handler.Index` | 否 (全页加载) | 显示搜索框、历史记录等。 |
| 搜索 | `GET` | `/search` | `handler.Search` | 是 | 接收查询参数 `q`，返回包含结果的完整页面。 |
| └─ 动态筛选 | `GET` | `/search/filter` | `handler.SearchFilter` | 是 (hx-get) | 接收筛选参数，仅返回 `search_results.html` 片段。 |
| 分类发现 | `GET` | `/discover` | `handler.Discover` | 是 | 默认页面，可通过 AJAX 加载不同分类。 |
| 排行榜 | `GET` | `/rankings` | `handler.Rankings` | 是 | 显示不同类型的榜单。 |
| **内容呈现流** | | | | | |
| 电影详情 | `GET` | `/movie/{id}` | `handler.MovieDetail` | 否 (全页加载) | `{id}` 是电影的唯一标识。 |
| └─ 切换播放线路 | `GET` | `/movie/{id}/player` | `handler.GetPlayer` | 是 (hx-get) | 接收 `source` 参数，返回 `player_options.html` 片段。 |
| 综合播放页 | `GET` | `/play/{id}` | `handler.PlayerPage` | 是 | 包含选集列表的播放页。 |
| └─ 切换剧集 | `GET` | `/play/{id}/episode` | `handler.GetEpisode` | 是 (hx-get) | 接收 `ep` 参数，更新播放器内的视频源。 |
| HLS 播放器 | `GET` | `/player/hls` | `handler.HLSPlayer` | 否 | 极简页面，通过 `src` query 参数接收 m3u8 地址。 |
| **用户系统** | | | | | |
| 登录/注册页 | `GET` | `/auth` | `handler.AuthPage` | 否 | 显示登录表单。 |
| └─ 提交登录 | `POST` | `/auth/login` | `handler.Login` | 是 (hx-post) | 处理登录逻辑，成功后重定向。 |
| └─ 提交注册 | `POST` | `/auth/register` | `handler.Register` | 是 (hx-post) | 处理注册逻辑。 |
| 用户中心 | `GET` | `/dashboard` | `handler.Dashboard` | 是 | 显示用户中心，不同板块用 HTMX 加载。 |
| **互动与 SEO** | | | | | |
| 反馈页 | `GET` | `/feedback` | `handler.FeedbackPage` | 否 | |
| └─ 提交反馈 | `POST` | `/feedback` | `handler.SubmitFeedback` | 是 (hx-post) | |
| 关于页 | `GET` | `/about` | `handler.AboutPage` | 否 | |
| Sitemap | `GET` | `/sitemap.xml` | `handler.Sitemap` | 否 | 动态生成 XML 文件。 |

---

### 3. HTMX 应用策略

充分利用 HTMX 来提升用户体验，减少整页刷新。

*   **搜索与筛选**：
    *   在搜索结果页，侧边栏的筛选器（如年份、地区）的 `<a>` 标签或 `<input>` 标签上添加 `hx-get="/search/filter"` 和 `hx-target="#results-container"`。
    *   这样，用户每次点击筛选，只有结果列表区域会更新，而不是整个页面。后端 `handler.SearchFilter` 只需渲染并返回 `search_results.html` 这个 HTML 片段。

*   **无限滚动**：
    *   在搜索结果、排行榜等列表页的底部，放置一个带有 `hx-get="/search?page=2"` 和 `hx-trigger="revealed"` 的元素。
    *   当用户滚动到页面底部时，它会自动请求下一页数据，并将返回的 HTML 片段追加到现有列表中，实现无限滚动效果。

*   **用户中心**：
    *   用户中心的侧边栏（收藏夹、历史记录、设置）可以设计成 HTMX 驱动的选项卡。
    *   点击不同选项卡会 `hx-get` 不同的内容到主内容区，例如 `hx-get="/dashboard/favorites"`，`hx-target="#dashboard-content"`。

*   **播放页**：
    *   点击“切换线路”或“选集”时，使用 `hx-get` 请求新的播放信息，然后更新播放器容器 `hx-target="#player-wrapper"`。这避免了整个播放页的刷新，体验更流畅。

### 总结

这个结构将 Golang 的高性能和 HTMX 的前端简洁性完美结合：

1.  **后端驱动**：所有渲染逻辑都在后端完成，前端几乎没有业务逻辑，非常轻量。
2.  **用户体验**：通过 HTMX 实现局部更新，获得了接近单页应用 (SPA) 的流畅体验，但没有 SPA 的复杂性。
3.  **SEO 友好**：所有核心内容页面（详情、搜索等）都支持完整的服务器端渲染（SSR），对搜索引擎爬虫非常友好。`sitemap.xml` 的动态生成进一步加强了 SEO。
4.  **开发高效**：后端开发者只需编写 Handler 并渲染 HTML 模板即可，无需深入复杂的前端构建和状态管理。

您可以从搭建 `cmd/server` 和 `web/template/layouts` 开始，逐步实现每个路由和对应的 Handler 及模板。

---
接下来我们可以探讨以下几点：

*   您想先从哪个模块开始深入设计？（例如，聚合搜索的实现思路）
*   需要我为您生成一个基础的 `main.go` (Gin服务器) 的代码框架吗？
*   我们可以讨论一下数据库模型的具体字段设计。

### **Moovie 项目最终技术方案 (修正版)**

本文档基于我们最初设定的完整项目结构，并整合了后续所有关于认证、交互和数据库的实现细节。

**核心技术栈**:
*   **后端**: Golang + Gin Web 框架
*   **前端**: HTMX
*   **认证**: 无状态 JWT (JSON Web Tokens) in `HttpOnly` Cookie
*   **数据库**: PostgreSQL (作为未来持久化的目标)

---

### **1. 最终项目目录结构 (完整版)**

这个结构融合了模块化、可扩展性和职责分离的最佳实践。

```
/Moovie
├── cmd/server/main.go         # ✅ 程序主入口：初始化Gin、DB连接、模板、路由

├── internal/                  # ✅ 内部业务逻辑
│   ├── handler/               # ✅ HTTP处理器 (Gin Handlers) - 职责：解析请求、调用Service、返回响应
│   │   ├── index_handler.go     # 首页
│   │   ├── search_handler.go    # 搜索、筛选
│   │   ├── movie_handler.go     # 电影详情、播放页
│   │   ├── user_handler.go      # 【JWT认证逻辑放这里】登录、注册、登出、Dashboard
│   │   ├── discover_handler.go  # 分类发现、排行榜
│   │   └── middleware.go        # 【JWT认证中间件放这里】
│   │
│   ├── service/               # ✅ 业务逻辑层 - 职责：处理复杂业务规则，组合Repository操作
│   │   ├── user_service.go      # 用户相关的业务逻辑（如密码哈希、验证）
│   │   ├── search_aggregator.go # 聚合多个电影源的搜索结果
│   │   └── ...
│   │
│   ├── model/                 # ✅ 数据模型 (Structs) - 职责：定义应用中的数据结构
│   │   ├── user.go              # User struct
│   │   ├── movie.go             # Movie, Episode, Source等structs
│   │   └── jwt_claims.go        # 【JWT CustomClaims struct放这里】
│   │
│   ├── repository/            # ✅ 数据访问层 - 职责：与数据源（数据库、缓存、API）交互
│   │   ├── user_repository.go   # 用户数据的增删改查（未来对接PostgreSQL）
│   │   └── movie_repository.go  # 电影数据的增删改查
│   │
│   └── util/                  # ✅ 工具函数
│       └── password.go          # 密码哈希和验证的辅助函数
│
├── pkg/                       # ✅ 可供外部使用的库 (例如，一个通用的Sitemap生成器)
│   └── sitemap_generator/
│
├── web/                       # ✅ 所有前端相关文件
│   ├── templates/             # ✅ HTML模板
│   │   ├── layouts/           # 基础布局 (base.html)
│   │   ├── pages/             # 完整页面模板 (index.html, search.html, detail.html)
│   │   └── components/        # ✅ HTMX可替换的组件/片段 (search_results.html, player_options.html)
│   └── static/                # ✅ 静态资源 (CSS, JS)
│
├── go.mod                     # Go模块依赖文件
└── config.yaml                # (可选) 配置文件，用于存储数据库连接、JWT密钥等
```

---

### **2. 核心功能实现细节 (填充到新结构中)**

#### **2.1. 用户认证系统 (JWT)**

*   **JWT Claims 定义**: 放在 `internal/model/jwt_claims.go`。
*   **JWT 密钥**: 从 `config.yaml` 读取，或作为环境变量。
*   **登录/注册/登出 Handler**: 放在 `internal/handler/user_handler.go`。
    *   `Login` 函数会调用 `internal/service/user_service.go` 中的 `ValidateUser` 函数。
    *   `ValidateUser` 函数再调用 `internal/repository/user_repository.go` 的 `GetUserByUsername` 和 `util/password.go` 的 `CheckPasswordHash`。
    *   验证成功后，在 `user_handler.go` 中生成 JWT 并设置 `HttpOnly` Cookie。
*   **认证中间件**: 放在 `internal/handler/middleware.go` 中，命名为 `AuthRequired`。它会保护所有需要登录的路由。

#### **2.2. 数据库集成 (PostgreSQL)**

*   **用户模型**: `internal/model/user.go` 中定义的 `User` struct。
*   **数据操作**: 所有对用户表的 `SELECT`, `INSERT` 等操作，都封装在 `internal/repository/user_repository.go` 中。
*   **密码处理**:
    *   哈希密码的逻辑放在 `internal/util/password.go` 中，提供 `HashPassword` 和 `CheckPasswordHash` 两个函数。
    *   在 `internal/service/user_service.go` 中创建用户时，调用 `HashPassword`。
    *   在验证用户时，调用 `CheckPasswordHash`。

#### **2.3. 前端交互 (HTMX)**

*   **完整页面渲染**: `handler` 调用 `c.HTML()` 时，渲染 `web/templates/pages/` 中的模板，这些模板通常会嵌入 `web/templates/layouts/base.html`。
*   **HTMX 片段渲染**:
    *   **场景**: 用户在搜索结果页点击“按年份筛选”。
    *   **流程**:
        1.  前端 `<a>` 标签触发 `hx-get="/search/filter?year=2023"`。
        2.  `search_handler.go` 中的 `FilterSearchResults` 函数被调用。
        3.  该函数调用 `service` 和 `repository` 获取筛选后的电影列表。
        4.  **关键**: 它不渲染完整页面，而是只渲染 `web/templates/components/search_results.html` 这个 HTML 片段，并将结果直接作为响应体返回。
        5.  HTMX 接收到这个 HTML 片段，并将其替换到页面上 `hx-target` 指定的容器中。

---

### **3. 完整页面功能规划 (回顾)**

这个规划与我们最初的设想保持一致，现在可以清晰地看到每个页面功能由哪个 `handler` 文件负责。

| 类别 | 页面/功能 | 负责人 (Handler) |
| :--- | :--- | :--- |
| **核心浏览** | 首页 (Index) | `index_handler.go` |
| | 搜索结果页 (Search) | `search_handler.go` |
| | 分类/发现页 (Discover) | `discover_handler.go` |
| | 排行榜 (Rankings) | `discover_handler.go` |
| **内容呈现** | 电影详情页 (Detail) | `movie_handler.go` |
| | 综合播放页 (Player) | `movie_handler.go` |
| **用户系统** | 登录/注册/中心 (Auth/Dashboard) | `user_handler.go` |
| **SEO & 其他**| Sitemap.xml | (可放在 `index_handler.go` 或新建一个 `seo_handler.go`) |
| | 后台管理 (Admin) | (新建一个 `admin_handler.go`) |

---

这次的方案，是我对之前所有讨论内容的最终、最完整的整合。它采用了我们最初设计的健壮结构，并正确地将后续所有技术细节填充到了对应的模块中。

再次为我之前的疏忽和不一致向您道歉。希望这份修正后的完整方案，能够真正成为您项目的坚实基础。
