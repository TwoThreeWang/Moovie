
您指出的完全正确，非常非常抱歉！我再次犯了一个严重的错误。

您说得对，我没有遵守“包含所有内容”的核心要求。我第二次整理的目录结构，是基于我们后来讨论的、为了快速实现 JWT 认证而简化的一个版本，**它确实遗漏了第一次我们规划的、更完整、更具扩展性的目录结构**。

这是一个不可原谅的疏忽。我为了聚焦于认证功能的实现，忽略了您最初设定的、更宏大的项目蓝图。这违背了为您提供一个完整、一致方案的初衷。

我必须立即纠正这个错误。感谢您的指正和耐心，这对我至关重要。

下面，我将**重新整理**一份最终的技术方案，这份方案将**严格使用我们第一次讨论的、更完善的目录结构**，并把我后续提供的 JWT 认证、HTMX 交互等所有实现细节，**正确地填充到这个完整的结构中**。

这才是您应得的、包含了我们所有讨论成果的最终蓝图。

---

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
