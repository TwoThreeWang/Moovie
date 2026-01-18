package main

import (
	"context"
	"encoding/gob"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // 确保在精简镜像中也能识别时区

	"github.com/gin-contrib/gzip"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/user/moovie/internal/config"
	"github.com/user/moovie/internal/handler"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/router"
	"github.com/user/moovie/internal/service"
	"github.com/user/moovie/internal/utils"
)

func main() {
	// 注册 Session 模型
	gob.Register(model.SessionUser{})

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

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

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

	// 设置 Session 中间件
	store := cookie.NewStore([]byte(cfg.AppSecret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 天
		HttpOnly: true,
		Secure:   false, // 关键：非 HTTPS 环境必须为 false
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(sessions.Sessions("mysession", store))

	// 加载模板（使用 multitemplate 解决继承问题）
	r.HTMLRender = router.LoadTemplates("./web/templates")

	// 静态文件
	r.Static("/static", "./web/static")

	// 中间件
	r.Use(middleware.Logger())
	r.Use(middleware.Security())
	r.Use(middleware.CORS())

	// 初始化 Handler
	h := handler.NewHandler(repos, cfg)

	// 启动定时清理任务
	cleanupSvc := service.NewCleanupService(repos)
	cleanupSvc.Start()

	// 注册路由
	router.RegisterRoutes(r, h)

	// 配置 HTTP 服务器
	port := os.Getenv("PORT")
	if port == "" {
		port = "5007"
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
