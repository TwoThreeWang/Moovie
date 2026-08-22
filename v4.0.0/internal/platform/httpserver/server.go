// Package httpserver 组装 HTTP 服务：全局中间件、健康检查探针和服务器超时参数都在这里。
// 中间件顺序很重要（见 New 内注释），业务路由由 cmd/web 通过 RouteRegistrar 回调统一注册。
package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/netutil"
)

// ReadinessProbe 检查实例是否具备接收业务流量的外部依赖条件。
type ReadinessProbe func(context.Context) error

// RouteRegistrar 由 cmd/web 提供，用于在全局中间件安装后集中注册业务路由。
type RouteRegistrar func(*gin.Engine)

// New 创建带安全头、日志、超时、过载、压缩、恢复和 CSRF 的 HTTP Server。
func New(cfg config.Config, readiness ReadinessProbe, register RouteRegistrar) *http.Server {
	if cfg.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	httpConfig := normalizedHTTPConfig(cfg.HTTP, cfg.Env)
	overload := newOverloadController(httpConfig)
	// 可观测性和安全响应头必须包住所有响应，包括在进入业务 Handler 前就被 CSRF 拒绝的请求。
	// 浏览器 API 刻意保持同源，不安装宽松 CORS；未来若需要跨域客户端，必须增加经过评审的明确白名单。
	router.Use(
		requestContext(),
		requestLogger(httpConfig.AccessLogSamplePercent, httpConfig.AccessLogMaxPerSecond),
		securityHeaders(),
		staticCacheHeaders(),
		requestTimeout(httpConfig.RequestTimeout),
		requestBodyLimit(httpConfig.MaxBodyBytes),
		overload.middleware(),
		gzip.Gzip(gzip.BestSpeed),
		gin.Recovery(),
		csrfProtection(cfg.Env == "production"),
	)

	router.GET("/health", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/ready", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if readiness != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			if err := readiness(ctx); err != nil {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
				return
			}
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	if register != nil {
		register(router)
	}

	return &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      httpConfig.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    httpConfig.MaxHeaderBytes,
	}
}

// ListenAndServe 在把连接交给 net/http 前应用进程级连接上限。
// 即使入口代理没有限流，也能保护文件描述符和空闲 keep-alive 连接占用的内存。
func ListenAndServe(server *http.Server, maxConnections int) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	if maxConnections > 0 {
		listener = netutil.LimitListener(listener, maxConnections)
	}
	return server.Serve(listener)
}

// requestIDContextKey 是请求 ID 在 gin.Context 里的键名。
const requestIDContextKey = "request_id"

// requestIDPattern 限制外部传入的请求 ID 字符集，防止脏数据进日志。
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// requestContext 为每个请求生成或沿用 X-Request-ID，并写入 context 与响应头，方便串联日志。
func requestContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			buffer := make([]byte, 16)
			if _, err := rand.Read(buffer); err == nil {
				requestID = hex.EncodeToString(buffer)
			} else {
				requestID = "request-id-unavailable"
			}
		}
		c.Set(requestIDContextKey, requestID)
		c.Request = c.Request.WithContext(requestmeta.WithRequestID(c.Request.Context(), requestID))
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// requestLogger 记录访问日志，但做了三层降噪：探针和静态资源不记、按百分比采样、每秒总量封顶。
// 5xx 和耗时超过 1 秒的请求不受采样限制，一定会记录。
func requestLogger(samplePercent, maxPerSecond int) gin.HandlerFunc {
	var sequence atomic.Uint64
	limiter := &accessLogRateLimiter{}
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		duration := time.Since(started)
		status := c.Writer.Status()
		path := c.Request.URL.Path
		if c.Writer.Header().Get("X-Moovie-Overload") != "" {
			return
		}
		if isProbePath(path) && status < http.StatusInternalServerError {
			return
		}
		if (strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, "/api/proxy/image/")) &&
			status < http.StatusBadRequest && duration < time.Second {
			return
		}
		alwaysLog := status >= http.StatusInternalServerError || duration >= time.Second
		if !alwaysLog {
			if !sampleRequest(&sequence, samplePercent) || !limiter.allow(maxPerSecond, time.Now()) {
				return
			}
		}
		requestID, _ := c.Get(requestIDContextKey)
		slog.Info("http request",
			"request_id", requestID,
			"method", c.Request.Method,
			"path", path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
		)
	}
}

// accessLogRateLimiter 按自然秒限制访问日志条数，防止流量高峰把磁盘写满。
type accessLogRateLimiter struct {
	mu     sync.Mutex
	second int64
	used   int
}

// allow 判断当前这一秒还有没有日志配额。
func (limiter *accessLogRateLimiter) allow(limit int, now time.Time) bool {
	if limit <= 0 {
		return false
	}
	second := now.Unix()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.second != second {
		limiter.second = second
		limiter.used = 0
	}
	if limiter.used >= limit {
		return false
	}
	limiter.used++
	return true
}

// sampleRequest 用自增序号做确定性采样，比随机数更均匀也更省。
func sampleRequest(sequence *atomic.Uint64, percent int) bool {
	if percent >= 100 {
		return true
	}
	if percent <= 0 {
		return false
	}
	return int((sequence.Add(1)-1)%100) < percent
}

// securityHeaders 统一下发基础安全响应头。
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Next()
	}
}
