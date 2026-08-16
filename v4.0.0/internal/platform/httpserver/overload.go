package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

const (
	overloadGlobal = "global"
	overloadHeavy  = "heavy"
	overloadImage  = "image"
)

// overloadController 用三个有界 channel 分别充当全局、重请求和图片请求信号量。
// rejected 只用于低成本累计观测，不能让高峰期的每次拒绝都写一条日志。
type overloadController struct {
	global       chan struct{}
	heavy        chan struct{}
	image        chan struct{}
	queueTimeout time.Duration
	rejected     atomic.Uint64
	lastLogUnix  atomic.Int64
}

func newOverloadController(cfg config.HTTPConfig) *overloadController {
	return &overloadController{
		global:       make(chan struct{}, cfg.MaxInFlight),
		heavy:        make(chan struct{}, cfg.MaxHeavyInFlight),
		image:        make(chan struct{}, cfg.MaxImageInFlight),
		queueTimeout: cfg.QueueTimeout,
	}
}

// middleware 在进入业务 Handler 前获取分类槽位与全局槽位，结束时按 defer 释放。
func (controller *overloadController) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isProbePath(c.Request.URL.Path) {
			c.Next()
			return
		}

		queueContext, cancel := context.WithTimeout(c.Request.Context(), controller.queueTimeout)
		defer cancel()
		class, slots := requestResourceClass(c.Request.URL.Path, controller)
		if slots != nil {
			if !acquireSlot(queueContext, slots) {
				controller.reject(c, class)
				return
			}
			defer releaseSlot(slots)
		}
		// 先获取更窄的分类槽位，再获取全局槽位。否则突发重请求可能先占满全局槽位，
		// 然后一起等待重请求信号量，导致无关的轻量页面也被错误降载。
		// 分类槽位可能恰好在排队截止时释放；如果全局槽位此刻可立即获得，仍允许请求进入，
		// 避免仅因共享计时器刚到期就误报全局过载。
		if !tryAcquireSlot(controller.global) && !acquireSlot(queueContext, controller.global) {
			controller.reject(c, overloadGlobal)
			return
		}
		defer releaseSlot(controller.global)
		c.Next()
	}
}

func acquireSlot(ctx context.Context, slots chan struct{}) bool {
	if ctx.Err() != nil {
		return false
	}
	select {
	case slots <- struct{}{}:
		return true
	default:
	}
	select {
	case slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func tryAcquireSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func releaseSlot(slots chan struct{}) { <-slots }

func requestResourceClass(path string, controller *overloadController) (string, chan struct{}) {
	if strings.HasPrefix(path, "/api/proxy/image/") {
		return overloadImage, controller.image
	}
	if isHeavyRequestPath(path) {
		return overloadHeavy, controller.heavy
	}
	return "", nil
}

func isHeavyRequestPath(path string) bool {
	if path == "/sitemap.xml" || path == "/discover" || path == "/trends" ||
		strings.HasPrefix(path, "/movie/") || strings.HasPrefix(path, "/discover/") {
		return true
	}
	for _, prefix := range []string{
		"/play/", "/watch/", "/api/watch/resolve", "/api/tvbox.json", "/api/vod",
		"/api/v2/media/", "/api/v2/media-units/", "/api/htmx/foryou", "/api/htmx/similar",
		"/api/htmx/search", "/api/v2/search", "/api/htmx/movie/",
		"/api/htmx/reviews", "/api/htmx/movie-backdrops",
		"/api/htmx/douban-card", "/api/danmaku", "/similar/",
		"/recommendations/", "/api/v2/admin/metrics",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isProbePath(path string) bool { return path == "/health" || path == "/ready" }

func (controller *overloadController) reject(c *gin.Context, class string) {
	total := controller.rejected.Add(1)
	c.Header("Retry-After", "1")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Moovie-Overload", class)
	controller.logRejection(class, total)
	if c.GetHeader("HX-Request") == "true" || strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.String(http.StatusServiceUnavailable, "服务繁忙，请稍后重试")
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
		"code": "server_busy", "message": "服务繁忙，请稍后重试", "retry_after_seconds": 1,
	})
}

func (controller *overloadController) logRejection(class string, total uint64) {
	now := time.Now().Unix()
	previous := controller.lastLogUnix.Load()
	if previous == now || !controller.lastLogUnix.CompareAndSwap(previous, now) {
		return
	}
	slog.Warn("request shed to protect process",
		"class", class,
		"rejected_total", total,
		"global_active", len(controller.global),
		"global_limit", cap(controller.global),
		"heavy_active", len(controller.heavy),
		"heavy_limit", cap(controller.heavy),
		"image_active", len(controller.image),
		"image_limit", cap(controller.image),
	)
}

func requestTimeout(timeout time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if timeout <= 0 || isProbePath(c.Request.URL.Path) {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func requestBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if maxBytes <= 0 || c.Request.Body == nil {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{
				"code": "request_too_large", "message": "请求内容过大",
			})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}

func staticCacheHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Header("Cache-Control", "public, max-age=604800, stale-while-revalidate=86400")
		}
		c.Next()
	}
}

func normalizedHTTPConfig(value config.HTTPConfig, environment string) config.HTTPConfig {
	wasEmpty := value.MaxInFlight <= 0 && value.MaxHeavyInFlight <= 0 && value.MaxImageInFlight <= 0 &&
		value.QueueTimeout <= 0 && value.RequestTimeout <= 0 && value.MaxBodyBytes <= 0 &&
		value.MaxHeaderBytes <= 0 && value.MaxConnections <= 0 && value.AccessLogSamplePercent == 0 &&
		value.AccessLogMaxPerSecond <= 0
	if value.MaxInFlight <= 0 {
		value.MaxInFlight = 64
	}
	if value.MaxHeavyInFlight <= 0 {
		value.MaxHeavyInFlight = 12
	}
	if value.MaxImageInFlight <= 0 {
		value.MaxImageInFlight = 24
	}
	value.MaxHeavyInFlight = min(value.MaxHeavyInFlight, value.MaxInFlight)
	value.MaxImageInFlight = min(value.MaxImageInFlight, value.MaxInFlight)
	if value.QueueTimeout <= 0 {
		value.QueueTimeout = 100 * time.Millisecond
	}
	if value.RequestTimeout <= 0 {
		value.RequestTimeout = 30 * time.Second
	}
	if value.MaxBodyBytes <= 0 {
		value.MaxBodyBytes = 1 << 20
	}
	if value.MaxHeaderBytes <= 0 {
		value.MaxHeaderBytes = 64 << 10
	}
	if value.MaxConnections < value.MaxInFlight {
		value.MaxConnections = max(512, value.MaxInFlight)
	}
	if value.AccessLogMaxPerSecond <= 0 {
		if environment == "production" {
			value.AccessLogMaxPerSecond = 20
		} else {
			value.AccessLogMaxPerSecond = 100
		}
	}
	if wasEmpty {
		if environment == "production" {
			value.AccessLogSamplePercent = 10
		} else {
			value.AccessLogSamplePercent = 100
		}
	}
	return value
}
