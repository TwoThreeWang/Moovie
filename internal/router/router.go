package router

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"

	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/handler"
	"github.com/user/moovie/internal/middleware"
)

// RegisterRoutes 注册所有路由
func RegisterRoutes(r *gin.Engine, h *handler.Handler) {
	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// ==================== 公开页面 ====================
	r.GET("/", h.Home)                 // 首页
	r.GET("/search", h.Search)         // 搜索页面
	r.GET("/player", h.Player)         // M3U8 播放器
	r.GET("/trends", h.Trends)         // 搜索趋势
	r.GET("/feedback", h.FeedbackPage) // 反馈页
	r.GET("/about", h.About)           // 关于页
	r.GET("/advertise", h.Advertise)   // 广告合作
	r.GET("/changelog", h.Changelog)   // 更新记录页
	r.GET("/dmca", h.DMCA)             // DMCA页
	r.GET("/privacy", h.Privacy)       // 隐私政策
	r.GET("/terms", h.Terms)           // 使用条款
	r.GET("/sitemap.xml", h.Sitemap)   // 网站地图
	r.GET("/robots.txt", h.Robots)     // Robots.txt

	// 需要识别登录状态但又不强制登录的页面
	optional := r.Group("/")
	optional.Use(middleware.OptionalAuth(h.Config.AppSecret))
	{
		optional.GET("/movie/:id", h.Movie)                        // 电影详情
		optional.GET("/play/:source_key/:vod_id", h.Play)          // 视频播放页
		optional.GET("/discover", h.Discover)                      // 默认发现页
		optional.GET("/discover/:type", h.Discover)                // 发现页
		optional.GET("/foryou", h.ForYou)                          // 为你推荐
		optional.GET("/recommend", h.ForYou)                       // 为你推荐
		optional.GET("/similar/:douban_id", h.RecommendationsPage) // 相似电影推荐
	}

	// ==================== 认证页面 ====================
	auth := r.Group("/auth")
	{
		auth.GET("/login", h.LoginPage)       // 登录页
		auth.POST("/login", h.Login)          // 登录
		auth.GET("/register", h.RegisterPage) // 注册页
		auth.POST("/register", h.Register)    // 注册
		auth.GET("/logout", h.Logout)         // 退出登陆
	}

	// ==================== 用户中心（需要登录）====================
	dashboard := r.Group("/dashboard")
	dashboard.Use(middleware.RequireAuth(h.Config.AppSecret))
	{
		dashboard.GET("", h.Dashboard)                         // 用户中心首页
		dashboard.GET("/settings", h.Settings)                 // 设置
		dashboard.POST("/settings/email", h.UpdateEmail)       // 更新邮箱
		dashboard.POST("/settings/username", h.UpdateUsername) // 更新用户名
		dashboard.POST("/settings/password", h.UpdatePassword) // 更新密码
	}

	// ==================== htmx API ====================
	api := r.Group("/api")
	api.Use(middleware.OptionalAuth(h.Config.AppSecret))
	{
		api.POST("/user-movies/:id/wish", h.MarkWish)                                   // 标记想看
		api.POST("/user-movies/:id/watched", h.MarkWatched)                             // 标记看过
		api.DELETE("/user-movies/:id", h.RemoveUserMovie)                               // 取消标记
		api.PATCH("/user-movies/:id", h.UpdateUserMovie)                                // 更新评分与短评
		api.POST("/feedback", h.SubmitFeedback)                                         // 提交反馈
		api.DELETE("/history/:id", h.RemoveHistory)                                     // 删除历史记录
		api.POST("/history/sync", h.SyncHistory)                                        // 同步历史记录
		api.GET("/movies/suggest", h.MovieSuggest)                                      // 电影建议
		api.GET("/proxy/image", h.ProxyImage)                                           // 图片代理
		api.GET("/htmx/similar-with-reason/:douban_id", h.SimilarMoviesWithReasonsHTMX) // 相似电影推荐（带原因）

		// htmx 专属 API
		api.GET("/htmx/search", h.SearchHTMX)                                    // 搜索结果片段
		api.GET("/htmx/similar", h.SimilarMoviesHTMX)                            // 相似电影推荐
		api.GET("/htmx/foryou", h.ForYouHTMX)                                    // 为你推荐
		api.GET("/htmx/reviews", h.ReviewsHTMX)                                  // 豆瓣短评
		api.GET("/htmx/movie-comments", h.MovieCommentsHTMX)                     // 用户评论片段
		api.GET("/htmx/user-movie/edit", h.UserMovieEditFormHTMX)                // 编辑表单片段
		api.GET("/htmx/user-movie/mark-watched", h.UserMovieMarkWatchedFormHTMX) // 标记已看过前的表单
		api.GET("/htmx/user-movie/buttons", h.UserMovieButtonsHTMX)              // 操作按钮片段
		api.GET("/htmx/feedback-list", h.FeedbackListHTMX)                       // 反馈列表
		api.GET("/htmx/dashboard/wish", h.DashboardWishHTMX)                     // 仪表盘想看
		api.GET("/htmx/dashboard/watched", h.DashboardWatchedHTMX)               // 仪表盘已看过
		api.GET("/htmx/dashboard/history", h.DashboardHistoryHTMX)               // 仪表盘历史
		api.POST("/report/load-speed", h.ReportLoadSpeed)                        // 上报加载速度
		api.GET("/stats/load-speed", h.GetLoadStats)                             // 获取加载统计
	}

	// ==================== 管理后台 ====================
	admin := r.Group("/admin")
	admin.Use(middleware.RequireAuth(h.Config.AppSecret))
	admin.Use(middleware.RequireAdmin())
	{
		admin.GET("", h.AdminDashboard)         // 管理后台首页
		admin.GET("/users", h.AdminUsers)       // 用户管理
		admin.GET("/feedback", h.AdminFeedback) // 反馈管理

		// 用户管理 API
		admin.PUT("/users/:id/role", h.AdminUserUpdateRole) // 更新用户角色
		admin.DELETE("/users/:id", h.AdminUserDelete)       // 删除用户

		// 资源网管理
		admin.GET("/sites", h.AdminSites)             // 资源网管理
		admin.POST("/sites", h.AdminSiteCreate)       // 创建资源网
		admin.PUT("/sites/:id", h.AdminSiteUpdate)    // 更新资源网
		admin.DELETE("/sites/:id", h.AdminSiteDelete) // 删除资源网
		admin.GET("/sites/:id/test", h.AdminSiteTest) // 测试资源网

		// 反馈管理 API
		admin.PUT("/feedback/:id/status", h.AdminFeedbackStatus) // 更新反馈状态
		admin.PUT("/feedback/:id/reply", h.AdminFeedbackReply)   // 回复反馈

		// 搜索数据管理
		admin.GET("/data", h.AdminData)             // 搜索数据管理
		admin.POST("/data/clean", h.AdminDataClean) // 清理非活跃数据

		// 版权限制管理
		admin.GET("/copyright", h.AdminCopyright)              // 版权限制页面
		admin.POST("/copyright", h.AdminCopyrightCreate)       // 添加版权关键词
		admin.PUT("/copyright/:id", h.AdminCopyrightUpdate)    // 更新版权关键词
		admin.DELETE("/copyright/:id", h.AdminCopyrightDelete) // 删除版权关键词

		// 分类过滤管理
		admin.GET("/category", h.AdminCategory)              // 分类过滤页面
		admin.POST("/category", h.AdminCategoryCreate)       // 添加分类关键词
		admin.DELETE("/category/:id", h.AdminCategoryDelete) // 删除分类关键词
	}

	// ==================== 404 处理 ====================
	r.NoRoute(func(c *gin.Context) {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title":    "页面未找到 - Moovie影牛",
			"SiteName": h.Config.SiteName,
			"Path":     c.Request.URL.Path,
		}))
	})
}

// LoadTemplates 使用 multitemplate 加载模板，解决模板继承问题
func LoadTemplates(templatesDir string) multitemplate.Renderer {
	r := multitemplate.NewRenderer()

	// 获取布局和局部模板
	layouts, err := filepath.Glob(templatesDir + "/layouts/*.html")
	if err != nil {
		panic(err)
	}

	partials, err := filepath.Glob(templatesDir + "/partials/*.html")
	if err != nil {
		panic(err)
	}

	// 组装模板文件列表
	assemble := func(view string) []string {
		files := make([]string, 0)
		files = append(files, layouts...)
		files = append(files, partials...)
		files = append(files, view)
		return files
	}

	// 模板函数
	funcMap := template.FuncMap{
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"default": func(defaultValue, value interface{}) interface{} {
			switch v := value.(type) {
			case string:
				if v == "" {
					return defaultValue
				}
			case int:
				if v == 0 {
					return defaultValue
				}
			case nil:
				return defaultValue
			}
			return value
		},
		"js": func(s string) template.JS {
			return template.JS(s)
		},
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"jsonUnmarshal": func(s string) []interface{} {
			var res []interface{}
			_ = json.Unmarshal([]byte(s), &res)
			return res
		},
		"firstChar": func(s string) string {
			if s == "" {
				return ""
			}
			r := []rune(s)
			return string(r[0:1])
		},
		"seq": func(start, end int) []int {
			res := make([]int, 0, end-start+1)
			for i := start; i <= end; i++ {
				res = append(res, i)
			}
			return res
		},
		"divf": func(a, b interface{}) float64 {
			var aFloat, bFloat float64
			switch v := a.(type) {
			case int:
				aFloat = float64(v)
			case float64:
				aFloat = v
			default:
				aFloat = 0
			}
			switch v := b.(type) {
			case int:
				bFloat = float64(v)
			case float64:
				bFloat = v
			default:
				bFloat = 1
			}
			if bFloat == 0 {
				return 0
			}
			return aFloat / bFloat
		},
	}

	// 注册所有页面模板
	pages := []string{
		"home", "search", "movie", "play", "player", "player_embed",
		"discover", "trends", "foryou", "feedback",
		"about", "advertise", "changelog", "dmca", "privacy", "terms", "404",
		"login", "register", "recommendations",
		"dashboard", "settings",
		"admin_dashboard", "admin_users", "admin_sites", "admin_cache", "admin_feedback", "admin_copyright", "admin_category",
	}

	for _, page := range pages {
		viewPath := templatesDir + "/pages/" + page + ".html"
		if page == "player_embed" {
			r.AddFromFilesFuncs(page+".html", funcMap, viewPath)
		} else {
			r.AddFromFilesFuncs(page+".html", funcMap, assemble(viewPath)...)
		}
	}

	// 注册局部模板（用于 htmx）
	for _, partial := range partials {
		name := "partials/" + filepath.Base(partial)
		// 构建文件列表：当前 partial 放第一位（作为主入口），其他 partials 随后（作为依赖）
		files := make([]string, 0, len(partials))
		files = append(files, partial)
		for _, p := range partials {
			if p != partial {
				files = append(files, p)
			}
		}
		r.AddFromFilesFuncs(name, funcMap, files...)
	}

	return r
}
