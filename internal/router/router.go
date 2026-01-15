package router

import (
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
	r.GET("/", h.Home)
	r.GET("/search", h.Search)
	r.GET("/movie/:id", h.Movie)
	r.GET("/play/:id", h.Play)
	r.GET("/player", h.Player)
	r.GET("/discover", h.Discover)
	r.GET("/rankings", h.Rankings)
	r.GET("/trends", h.Trends)
	r.GET("/feedback", h.FeedbackPage)
	r.GET("/about", h.About)
	r.GET("/dmca", h.DMCA)
	r.GET("/privacy", h.Privacy)
	r.GET("/terms", h.Terms)
	r.GET("/sitemap.xml", h.Sitemap)

	// ==================== 认证页面 ====================
	auth := r.Group("/auth")
	{
		auth.GET("/login", h.LoginPage)
		auth.POST("/login", h.Login)
		auth.GET("/register", h.RegisterPage)
		auth.POST("/register", h.Register)
		auth.POST("/logout", h.Logout)
	}

	// ==================== 用户中心（需要登录）====================
	dashboard := r.Group("/dashboard")
	dashboard.Use(middleware.RequireAuth(h.Config.AppSecret))
	{
		dashboard.GET("", h.Dashboard)
		dashboard.GET("/favorites", h.Favorites)
		dashboard.GET("/history", h.History)
		dashboard.GET("/settings", h.Settings)
		dashboard.POST("/settings/email", h.UpdateEmail)
		dashboard.POST("/settings/username", h.UpdateUsername)
		dashboard.POST("/settings/password", h.UpdatePassword)
	}

	// ==================== htmx API ====================
	api := r.Group("/api")
	api.Use(middleware.OptionalAuth(h.Config.AppSecret))
	{
		api.POST("/favorites/:id", h.AddFavorite)
		api.DELETE("/favorites/:id", h.RemoveFavorite)
		api.POST("/feedback", h.SubmitFeedback)
		api.POST("/history/sync", h.SyncHistory)
		api.GET("/movies/suggest", h.MovieSuggest)
		api.GET("/movies/check/:doubanId", h.CheckMovie)
		api.GET("/proxy/image", h.ProxyImage)

		// 资源网视频搜索 API
		api.GET("/vod/search", h.VodSearch)
		api.GET("/vod/detail", h.VodDetail)
	}

	// ==================== 管理后台 ====================
	admin := r.Group("/admin")
	admin.Use(middleware.RequireAuth(h.Config.AppSecret))
	admin.Use(middleware.RequireAdmin())
	{
		admin.GET("", h.AdminDashboard)
		admin.GET("/users", h.AdminUsers)
		admin.GET("/crawlers", h.AdminCrawlers)

		// 资源网管理
		admin.GET("/sites", h.AdminSites)
		admin.POST("/sites", h.AdminSiteCreate)
		admin.PUT("/sites/:id", h.AdminSiteUpdate)
		admin.DELETE("/sites/:id", h.AdminSiteDelete)

		// 搜索缓存管理
		admin.GET("/cache", h.AdminCache)
		admin.POST("/cache/clean", h.AdminCacheClean)
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
	}

	// 注册所有页面模板
	pages := []string{
		"home", "search", "movie", "play", "player",
		"discover", "rankings", "trends", "feedback",
		"about", "dmca", "privacy", "terms", "404",
		"login", "register",
		"dashboard", "favorites", "history", "settings",
		"admin_dashboard", "admin_users", "admin_crawlers", "admin_sites", "admin_cache",
	}

	for _, page := range pages {
		viewPath := templatesDir + "/pages/" + page + ".html"
		r.AddFromFilesFuncs(page+".html", funcMap, assemble(viewPath)...)
	}

	return r
}
