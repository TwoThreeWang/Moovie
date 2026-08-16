package httpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/gin-gonic/gin"
)

func TestHeavyRequestLimitShedsBurstAndKeepsHealthAvailable(t *testing.T) {
	cfg := overloadTestConfig()
	started := make(chan struct{})
	release := make(chan struct{})
	var active atomic.Int32
	server := New(cfg, nil, func(router *gin.Engine) {
		router.GET("/movie/:id", func(c *gin.Context) {
			if active.Add(1) == 1 {
				close(started)
			}
			defer active.Add(-1)
			<-release
			c.String(http.StatusOK, "ok")
		})
	})

	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		server.Handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/movie/1", nil))
	}()
	<-started

	busy := httptest.NewRecorder()
	busyRequest := httptest.NewRequest(http.MethodGet, "/movie/2", nil)
	busyRequest.Header.Set("Accept", "application/json")
	server.Handler.ServeHTTP(busy, busyRequest)
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("X-Moovie-Overload") != overloadHeavy || busy.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy response = status:%d headers:%v body:%s", busy.Code, busy.Header(), busy.Body.String())
	}
	if !strings.Contains(busy.Body.String(), "server_busy") {
		t.Fatalf("busy body = %q", busy.Body.String())
	}

	health := httptest.NewRecorder()
	server.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status while saturated = %d", health.Code)
	}
	close(release)
	<-firstDone
	if first.Code != http.StatusOK || active.Load() != 0 {
		t.Fatalf("first response/active = %d/%d", first.Code, active.Load())
	}
}

func TestGlobalRequestLimitShedsNonHeavyWork(t *testing.T) {
	cfg := overloadTestConfig()
	cfg.HTTP.MaxInFlight = 1
	started := make(chan struct{})
	release := make(chan struct{})
	server := New(cfg, nil, func(router *gin.Engine) {
		router.GET("/slow", func(c *gin.Context) {
			close(started)
			<-release
			c.Status(http.StatusNoContent)
		})
	})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))
	}()
	<-started
	busy := httptest.NewRecorder()
	server.Handler.ServeHTTP(busy, httptest.NewRequest(http.MethodGet, "/about", nil))
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("X-Moovie-Overload") != overloadGlobal {
		t.Fatalf("global busy response = status:%d overload:%q", busy.Code, busy.Header().Get("X-Moovie-Overload"))
	}
	close(release)
	<-firstDone
}

func TestImmediateSlotCanBeTakenAfterQueueDeadline(t *testing.T) {
	slots := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if acquireSlot(ctx, slots) {
		t.Fatal("queueing acquire unexpectedly ignored canceled context")
	}
	if !tryAcquireSlot(slots) {
		t.Fatal("free immediate slot was not acquired")
	}
	releaseSlot(slots)
}

func TestHeavyWaiterDoesNotConsumeLastGlobalSlot(t *testing.T) {
	cfg := overloadTestConfig()
	cfg.HTTP.MaxInFlight = 2
	started := make(chan struct{})
	release := make(chan struct{})
	server := New(cfg, nil, func(router *gin.Engine) {
		router.GET("/movie/:id", func(c *gin.Context) {
			if c.Param("id") == "1" {
				close(started)
				<-release
			}
			c.Status(http.StatusNoContent)
		})
		router.GET("/light", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	})
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/movie/1", nil))
	}()
	<-started
	waiterDone := make(chan struct{})
	go func() {
		defer close(waiterDone)
		server.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/movie/2", nil))
	}()
	time.Sleep(5 * time.Millisecond)
	light := httptest.NewRecorder()
	server.Handler.ServeHTTP(light, httptest.NewRequest(http.MethodGet, "/light", nil))
	if light.Code != http.StatusNoContent {
		t.Fatalf("light request status while heavy class saturated = %d", light.Code)
	}
	close(release)
	<-firstDone
	<-waiterDone
}

func TestRequestBodyAndStaticCacheBoundaries(t *testing.T) {
	cfg := overloadTestConfig()
	cfg.HTTP.MaxBodyBytes = 8
	server := New(cfg, nil, func(router *gin.Engine) {
		router.POST("/body", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		router.GET("/static/test.js", func(c *gin.Context) { c.String(http.StatusOK, "asset") })
	})

	large := httptest.NewRecorder()
	server.Handler.ServeHTTP(large, httptest.NewRequest(http.MethodPost, "/body", strings.NewReader("123456789")))
	if large.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body status = %d", large.Code)
	}
	asset := httptest.NewRecorder()
	server.Handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/static/test.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Cache-Control"), "max-age=604800") {
		t.Fatalf("static response = status:%d cache:%q", asset.Code, asset.Header().Get("Cache-Control"))
	}
}

func TestAccessLogSamplerHonorsExactPercentage(t *testing.T) {
	var sequence atomic.Uint64
	allowed := 0
	for index := 0; index < 200; index++ {
		if sampleRequest(&sequence, 10) {
			allowed++
		}
	}
	if allowed != 20 || sampleRequest(&sequence, 0) || !sampleRequest(&sequence, 100) {
		t.Fatalf("sample results = allowed:%d sequence:%d", allowed, sequence.Load())
	}
}

func TestAccessLogRateLimiterCapsSuccessfulLogsPerSecond(t *testing.T) {
	limiter := &accessLogRateLimiter{}
	now := time.Unix(100, 0)
	if !limiter.allow(2, now) || !limiter.allow(2, now) || limiter.allow(2, now) {
		t.Fatal("rate limiter did not enforce the per-second budget")
	}
	if !limiter.allow(2, now.Add(time.Second)) || limiter.allow(0, now.Add(2*time.Second)) {
		t.Fatal("rate limiter did not reset or disable correctly")
	}
}

func TestHeavyRequestPathCoversResourceAmplifyingRoutes(t *testing.T) {
	for _, path := range []string{
		"/movie/1292052", "/discover", "/discover/tv", "/trends", "/play/source/1", "/watch/1292052",
		"/api/watch/resolve", "/api/tvbox.json", "/api/vod", "/api/v2/media/1/resources",
		"/api/v2/media-units/1/playback-candidates", "/api/htmx/foryou", "/api/htmx/similar",
		"/api/htmx/reviews", "/api/htmx/movie-backdrops", "/api/v2/media/suggest",
		"/api/htmx/douban-card", "/api/danmaku", "/sitemap.xml",
	} {
		if !isHeavyRequestPath(path) {
			t.Errorf("expected heavy path: %s", path)
		}
	}
	for _, path := range []string{"/", "/health", "/ready", "/static/app.js", "/api/v2/history/sync"} {
		if isHeavyRequestPath(path) {
			t.Errorf("expected ordinary path: %s", path)
		}
	}
}

func TestNormalizedHTTPConfigFillsPartialValuesWithoutOverridingDisabledAccessLogs(t *testing.T) {
	cfg := normalizedHTTPConfig(config.HTTPConfig{MaxInFlight: 8}, "production")
	if cfg.MaxHeavyInFlight != 8 || cfg.MaxImageInFlight != 8 || cfg.MaxConnections < cfg.MaxInFlight {
		t.Fatalf("normalized partial limits = %+v", cfg)
	}
	if cfg.AccessLogSamplePercent != 0 {
		t.Fatalf("explicit disabled access logs changed to %d", cfg.AccessLogSamplePercent)
	}
	empty := normalizedHTTPConfig(config.HTTPConfig{}, "production")
	if empty.AccessLogSamplePercent != 10 || empty.AccessLogMaxPerSecond != 20 || empty.MaxInFlight != 64 {
		t.Fatalf("normalized empty production config = %+v", empty)
	}
}

func overloadTestConfig() config.Config {
	return config.Config{
		Env: "test", Port: "5008", SiteName: "Moovie影牛", SiteURL: "http://localhost:5008",
		HTTP: config.HTTPConfig{
			MaxInFlight: 4, MaxHeavyInFlight: 1, MaxImageInFlight: 2,
			QueueTimeout: 20 * time.Millisecond, RequestTimeout: time.Second,
			MaxBodyBytes: 1 << 20, MaxHeaderBytes: 64 << 10, MaxConnections: 32,
			AccessLogSamplePercent: 100, AccessLogMaxPerSecond: 100,
		},
	}
}
