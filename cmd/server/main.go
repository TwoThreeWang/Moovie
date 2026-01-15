package main

import (
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/user/moovie/internal/config"
	"github.com/user/moovie/internal/handler"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
)

func main() {
	// 加载环境变量
	if err := godotenv.Load(); err != nil {
		log.Println("未找到 .env 文件，使用系统环境变量")
	}

	// 加载配置
	cfg := config.Load()

	// 初始化数据库
	db, err := repository.InitDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库连接失败: %v", err)
	}
	defer db.Close()

	// 初始化仓库
	repos := repository.NewRepositories(db)

	// 初始化缓存
	utils.InitCache()

	// 初始化 Gin
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 启用 gzip，默认压缩级别
	r.Use(gzip.Gzip(gzip.DefaultCompression))

	// 加载模板（使用 multitemplate 解决继承问题）
	r.HTMLRender = loadTemplates("./web/templates")

	// 静态文件
	r.Static("/static", "./web/static")

	// 中间件
	r.Use(middleware.Logger())
	r.Use(middleware.CORS())

	// 初始化 Handler
	h := handler.NewHandler(repos, cfg)

	// 注册路由
	registerRoutes(r, h)

	// 配置 HTTP 服务器
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:           ":" + port,
		Handler:        r,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// 在 goroutine 中启动服务器，这样我们就可以监听信号
	go func() {
		log.Printf("服务器启动于 http://localhost:%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("服务器启动失败: %v", err)
		}
	}()

	// 等待中断信号以优雅地关闭服务器
	quit := make(chan os.Signal, 1)
	// kill (no parameter) 默认发送 syscall.SIGTERM
	// kill -2 是 syscall.SIGINT
	// kill -9 是 syscall.SIGKILL，但它不能被捕获，所以不需要添加
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("正在关闭服务器...")

	// 5 秒超时上下文用于关闭过程
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatal("服务器强制关闭:", err)
	}

	log.Println("服务器已退出")
}

func registerRoutes(r *gin.Engine, h *handler.Handler) {
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
	dashboard.Use(middleware.RequireAuth(h.Config.JWTSecret))
	{
		dashboard.GET("", h.Dashboard)
		dashboard.GET("/favorites", h.Favorites)
		dashboard.GET("/history", h.History)
		dashboard.GET("/settings", h.Settings)
	}

	// ==================== htmx API ====================
	api := r.Group("/api")
	api.Use(middleware.OptionalAuth(h.Config.JWTSecret))
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
	admin.Use(middleware.RequireAuth(h.Config.JWTSecret))
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

// loadTemplates 使用 multitemplate 加载模板，解决模板继承问题
func loadTemplates(templatesDir string) multitemplate.Renderer {
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
