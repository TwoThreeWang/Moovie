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
	r.GET("/rankings", h.Rankings)     // 排行榜
	r.GET("/trends", h.Trends)         // 热门
	r.GET("/feedback", h.FeedbackPage) // 反馈页
	r.GET("/about", h.About)           // 关于页
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
		optional.GET("/movie/:id", h.Movie)               // 电影详情
		optional.GET("/play/:source_key/:vod_id", h.Play) // 视频播放页
		optional.GET("/discover", h.Discover)             // 发现页
		optional.GET("/foryou", h.ForYou)                 // 为你推荐
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
		dashboard.GET("/favorites", h.Favorites)               // 收藏
		dashboard.GET("/history", h.History)                   // 历史记录
		dashboard.GET("/settings", h.Settings)                 // 设置
		dashboard.POST("/settings/email", h.UpdateEmail)       // 更新邮箱
		dashboard.POST("/settings/username", h.UpdateUsername) // 更新用户名
		dashboard.POST("/settings/password", h.UpdatePassword) // 更新密码
	}

	// ==================== htmx API ====================
	api := r.Group("/api")
	api.Use(middleware.OptionalAuth(h.Config.AppSecret))
	{
		api.POST("/favorites/:id", h.AddFavorite)        // 添加收藏
		api.DELETE("/favorites/:id", h.RemoveFavorite)   // 移除收藏
		api.POST("/feedback", h.SubmitFeedback)          // 提交反馈
		api.DELETE("/history/:id", h.RemoveHistory)      // 删除历史记录
		api.POST("/history/sync", h.SyncHistory)         // 同步历史记录
		api.GET("/movies/suggest", h.MovieSuggest)       // 电影建议
		api.GET("/movies/check/:doubanId", h.CheckMovie) // 检查电影是否存在
		api.GET("/proxy/image", h.ProxyImage)            // 图片代理

		// 资源网视频搜索 API
		api.GET("/vod/search", h.VodSearch) // 视频搜索
		api.GET("/vod/detail", h.VodDetail) // 视频详情

		// htmx 专属 API
		api.GET("/htmx/search", h.SearchHTMX)              // 搜索结果片段
		api.GET("/htmx/similar", h.SimilarMoviesHTMX)      // 相似电影推荐
		api.GET("/htmx/foryou", h.ForYouHTMX)              // 为你推荐
		api.GET("/htmx/reviews", h.ReviewsHTMX)            // 豆瓣短评
		api.GET("/htmx/feedback-list", h.FeedbackListHTMX) // 反馈列表
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
	}

	// 注册所有页面模板
	pages := []string{
		"home", "search", "movie", "play", "player", "player_embed",
		"discover", "rankings", "trends", "foryou", "feedback",
		"about", "changelog", "dmca", "privacy", "terms", "404",
		"login", "register",
		"dashboard", "favorites", "history", "settings",
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
		r.AddFromFilesFuncs(name, funcMap, partial)
	}

	return r
}
