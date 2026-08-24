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

// 三类并发额度的名字，用于日志和指标。
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

// newOverloadController 按配置创建三个并发额度。
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

// acquireSlot 先尝试非阻塞占位，占不到再带超时排队。
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

// tryAcquireSlot 只做一次非阻塞尝试。
func tryAcquireSlot(slots chan struct{}) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// releaseSlot 归还槽位。
func releaseSlot(slots chan struct{}) { <-slots }

// requestResourceClass 判断请求属于哪一类资源（图片代理 / 重请求 / 普通），返回对应的信号量。
func requestResourceClass(path string, controller *overloadController) (string, chan struct{}) {
	if strings.HasPrefix(path, "/api/proxy/image/") {
		return overloadImage, controller.image
	}
	if isHeavyRequestPath(path) {
		return overloadHeavy, controller.heavy
	}
	return "", nil
}

// isHeavyRequestPath 列出会打上游接口或查大表的路径。
// 注意：这是一份硬编码的路径前缀清单，新增这类接口时需要同步维护，否则限流分类会漏。
func isHeavyRequestPath(path string) bool {
	if path == "/sitemap.xml" || path == "/discover" || path == "/trends" ||
		strings.HasPrefix(path, "/movie/") || strings.HasPrefix(path, "/discover/") {
		return true
	}
	for _, prefix := range []string{
		"/play/", "/watch/", "/api/watch/resolve", "/api/tvbox.json", "/api/vod",
		"/api/v2/media/", "/api/v2/media-units/", "/api/htmx/foryou", "/api/htmx/similar",
		"/api/htmx/search", "/api/v2/search", "/api/htmx/movie/",
		"/api/htmx/reviews", "/api/htmx/movie-backdrops", "/api/danmaku", "/similar/",
		"/recommendations/", "/api/v2/admin/metrics",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// isProbePath 判断是否为健康检查路径，这类请求不限流也不记日志。
func isProbePath(path string) bool { return path == "/health" || path == "/ready" }

// reject 返回 503 并带上 Retry-After，HTML 请求返回文案、接口请求返回 JSON。
func (controller *overloadController) reject(c *gin.Context, class string) {
	total := controller.rejected.Add(1)
	c.Header("Retry-After", "1")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Moovie-Overload", class)
	controller.logRejection(class, total)
	if c.GetHeader("HX-Request") == "true" {
		c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", []byte(
			`<div class="empty-state" style="padding:2rem"><p>服务繁忙，请稍后重试</p></div>`))
		c.Abort()
		return
	}
	if strings.Contains(c.GetHeader("Accept"), "text/html") {
		c.Data(http.StatusServiceUnavailable, "text/html; charset=utf-8", []byte(overloadHTML))
		c.Abort()
		return
	}
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
		"code": "server_busy", "message": "服务繁忙，请稍后重试", "retry_after_seconds": 1,
	})
}

// logRejection 每秒最多写一条过载日志，避免拒绝风暴时日志反过来加重负载。
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

const overloadHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>服务繁忙</title><style>
:root{--bg:#fff;--text:#1a1a2e;--text-s:#64748b;--primary:#6366f1}
@media(prefers-color-scheme:dark){:root{--bg:#0f0f1a;--text:#e2e8f0;--text-s:#94a3b8;--primary:#818cf8}}
*{margin:0;box-sizing:border-box}body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;
background:var(--bg);color:var(--text);display:flex;align-items:center;justify-content:center;min-height:100vh;text-align:center;padding:40px 20px}
.dots{display:flex;gap:6px;justify-content:center}
.dots span{width:8px;height:8px;border-radius:50%;background:var(--primary);animation:pulse 1.4s ease-in-out infinite}
.dots span:nth-child(2){animation-delay:.2s}.dots span:nth-child(3){animation-delay:.4s}
@keyframes pulse{0%,80%,100%{opacity:.2;transform:scale(.8)}40%{opacity:1;transform:scale(1)}}
h2{font-size:1.25rem;font-weight:600;margin:20px 0 8px}p{font-size:.9rem;color:var(--text-s)}
a{display:inline-block;margin-top:20px;padding:8px 20px;font-size:.9rem;color:#fff;background:var(--primary);border-radius:8px;text-decoration:none}
</style></head><body><div>
<div class="dots"><span></span><span></span><span></span></div>
<h2>服务繁忙</h2><p>当前访问量较大，请稍后重试</p>
<a href="javascript:location.reload()">刷新重试</a>
</div></body></html>`

// requestTimeout 给每个请求的 context 加统一超时，防止慢上游把连接一直占住。
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

// requestBodyLimit 限制请求体大小，先看 Content-Length 快速拒绝，再用 MaxBytesReader 兜底。
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

// staticCacheHeaders 给 /static/ 下的资源加长缓存。
func staticCacheHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/static/") {
			c.Header("Cache-Control", "public, max-age=604800, stale-while-revalidate=86400")
		}
		c.Next()
	}
}

// normalizedHTTPConfig 给未配置的项补默认值，并保证分类上限不超过全局上限。
// 测试里可以直接传零值 HTTPConfig 而不必逐项填写。
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
