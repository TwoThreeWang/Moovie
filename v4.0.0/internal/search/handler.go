package search

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/cache"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

// maxUnifiedSearchLimit 是统一搜索单次返回的条数上限。
const maxUnifiedSearchLimit = 100

// Searcher 是资源搜索的最小接口，便于 Handler 测试时替换。
type Searcher interface {
	Search(ctx context.Context, keyword string, bypassFilter bool) (*Result, error)
}

// Handler 提供搜索页、统一搜索接口和热搜榜。结果和热搜都带进程内缓存。
type Handler struct {
	config     config.Config
	unified    UnifiedSearcher
	cache      *cache.TTL[UnifiedResult]
	refreshing sync.Map // key → struct{}，防止同一缓存键并发刷新
	logger     SearchLogStore
	runner     BackgroundRunner
	trends     *cache.TTL[[]TrendItem]
	now        func() time.Time
}

// HandlerOption 是 Handler 的可选装配项。
type HandlerOption func(*Handler)

// WithSearchLogger 注入搜索日志记录（异步写，不阻塞响应）。
func WithSearchLogger(logger SearchLogStore, runner BackgroundRunner) HandlerOption {
	return func(handler *Handler) {
		handler.logger = logger
		handler.runner = runner
	}
}

// WithUnifiedSearcher 注入统一搜索实现。
func WithUnifiedSearcher(searcher UnifiedSearcher) HandlerOption {
	return func(handler *Handler) { handler.unified = searcher }
}

// NewHandler 构造搜索 Handler，缓存容量和 TTL 取自配置。
func NewHandler(cfg config.Config, searcher Searcher, options ...HandlerOption) *Handler {
	cacheEntries := cfg.Search.CacheEntries
	if cacheEntries <= 0 {
		cacheEntries = 200
	}
	cacheTTL := cfg.Search.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 3 * time.Hour
	}
	handler := &Handler{config: cfg, unified: NewUnifiedSearchService(searcher),
		cache: cache.New[UnifiedResult](cacheEntries, cacheTTL), trends: cache.New[[]TrendItem](2, 10*time.Minute), now: time.Now}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Register 注册路由：搜索页、HTMX 片段、JSON 接口和热搜榜页。
func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/search", handler.searchPage)
	router.GET("/api/htmx/search", handler.unifiedSearchHTMX)
	router.GET("/api/v2/search", handler.unifiedSearchAPI)
	router.GET("/trends", handler.trendsPage)
}

// unifiedSearchAPI 返回 JSON 格式的统一搜索结果。
func (handler *Handler) unifiedSearchAPI(c *gin.Context) {
	result, ok := handler.runUnifiedSearch(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": result.Items, "unmatched": result.Unmatched,
		"filtered_count": result.FilteredCount, "duration_ms": result.DurationMS,
		"resource_duration_ms": result.ResourceDurationMS, "resource_unavailable": result.ResourceUnavailable,
		"catalog_duration_ms": result.CatalogDurationMS,
		"catalog_fallback":    result.CatalogFallback})
}

// unifiedSearchHTMX 返回 HTMX 片段并记录一次搜索日志。
func (handler *Handler) unifiedSearchHTMX(c *gin.Context) {
	result, ok := handler.runUnifiedSearch(c)
	if !ok {
		return
	}
	if len(result.Items) > 0 || len(result.Unmatched) > 0 {
		handler.recordSearch(c, strings.TrimSpace(c.Query("q")))
	}
	if doubanID := strings.TrimSpace(c.Query("douban_id")); doubanID != "" {
		items := make([]UnifiedItem, 0, len(result.Items))
		for _, item := range result.Items {
			if item.DoubanID != doubanID || item.ResourceCount > 0 {
				items = append(items, item)
			}
		}
		result.Items = items
	}
	c.HTML(http.StatusOK, "partials/unified_search_results.html", gin.H{"Result": result, "Keyword": c.Query("q")})
}

// runUnifiedSearch 校验参数、查缓存、执行统一搜索。返回值第二项为 false 表示已经写过错误响应。
func (handler *Handler) runUnifiedSearch(c *gin.Context) (UnifiedResult, bool) {
	keyword := strings.TrimSpace(c.Query("q"))
	if keyword == "" || len([]rune(keyword)) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_query", "message": "q 参数无效"})
		return UnifiedResult{}, false
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit < 1 || limit > maxUnifiedSearchLimit {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_limit", "message": "limit 必须在 1 到 100 之间"})
		return UnifiedResult{}, false
	}
	mediaType := strings.TrimSpace(c.Query("type"))
	if mediaType != "" && normalizeMediaType(mediaType) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_type", "message": "type 仅支持 movie 或 tv"})
		return UnifiedResult{}, false
	}
	excludeSourceKey, excludeVodID := excludedResource(c.Query("exclude"))
	query := UnifiedQuery{Keyword: keyword, Year: c.Query("year"), MediaType: mediaType,
		ExcludeSourceKey: excludeSourceKey, ExcludeVodID: excludeVodID,
		BypassFilter: c.Query("bypass") == "1", Limit: limit}
	cacheKey := unifiedSearchCacheKey(query)
	if cached, stale, found := handler.cache.GetStale(cacheKey); found {
		if stale {
			handler.refreshInBackground(cacheKey, query)
		}
		if refresher, ok := handler.unified.(UnifiedPlaybackRefresher); ok {
			if refreshed, refreshErr := refresher.RefreshPlayback(c.Request.Context(), cached, query); refreshErr == nil {
				cached = refreshed
			}
		}
		return cached, true
	}
	if handler.unified == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "search_unavailable", "message": "搜索服务暂时不可用"})
		return UnifiedResult{}, false
	}
	result, err := handler.unified.SearchUnified(c.Request.Context(), query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "unified_search_failed", "message": "统一搜索失败"})
		return UnifiedResult{}, false
	}
	if len(result.Items) > 0 || len(result.Unmatched) > 0 {
		handler.cache.Set(cacheKey, result)
	}
	return result, true
}

// unifiedSearchCacheKey 用全部查询条件拼缓存键，避免不同筛选条件互相串结果。
func unifiedSearchCacheKey(query UnifiedQuery) string {
	return fmt.Sprintf("search:%s:%s:%s:%s:%s:%t:%d", strings.ToLower(query.Keyword), query.Year,
		normalizeMediaType(query.MediaType), query.ExcludeSourceKey, query.ExcludeVodID,
		query.BypassFilter, query.Limit)
}

// excludedResource 解析 exclude=source:vodid 参数，用于“换个线路”时排除当前正在播放的资源。
func excludedResource(value string) (string, string) {
	sourceKey, vodID, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found || strings.TrimSpace(sourceKey) == "" || strings.TrimSpace(vodID) == "" {
		return "", ""
	}
	return strings.TrimSpace(sourceKey), strings.TrimSpace(vodID)
}

// searchPage 渲染搜索页；带 doubanId 时直接跳转到影片详情页。
func (handler *Handler) searchPage(c *gin.Context) {
	keyword := c.Query("kw")
	if keyword == "" {
		c.Redirect(http.StatusFound, "/")
		return
	}
	if doubanID := c.Query("doubanId"); doubanID != "" {
		target := "/movie/" + doubanID
		if keyword != "" {
			target += "?title=" + url.QueryEscape(keyword)
		}
		c.Redirect(http.StatusFound, target)
		return
	}
	view := platformweb.NewViewModel(c, handler.config, platformweb.Metadata{
		Title:       keyword + "在线观看 - " + keyword + "免费高清资源搜索 - " + handler.config.SiteName,
		Description: "Moovie影牛 为您找到关于“" + keyword + "”的相关资源。包含最新电影、电视剧在线观看线路，支持4K/高清多源码切换。",
		Canonical:   fmt.Sprintf("%s/search?kw=%s", handler.config.SiteURL, keyword),
	})
	view.Keyword = keyword
	view.Bypass = c.Query("bypass") == "1"
	c.HTML(http.StatusOK, "search.html", view)
}

// recordSearch 异步记录搜索关键词，IP 只存哈希前缀。
func (handler *Handler) recordSearch(c *gin.Context, keyword string) {
	if handler.logger == nil || handler.runner == nil {
		return
	}
	ipHash := hashIP(c.ClientIP())
	handler.runner.Run(func(ctx context.Context) {
		logContext, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		_ = handler.logger.Log(logContext, keyword, nil, ipHash)
	})
}

// trendsPage 渲染热搜榜页面，分 24 小时榜和总榜。
func (handler *Handler) trendsPage(c *gin.Context) {
	items24h := handler.trendItems(c.Request.Context(), "24h", 24, 20)
	itemsAll := handler.trendItems(c.Request.Context(), "all", 720, 50)
	c.HTML(http.StatusOK, "trends.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       "今日影视热搜榜 - 热门电影电视剧排行榜 - 实时更新 - " + handler.config.SiteName,
		Description: "想知道大家都在看什么？Moovie影牛实时汇总全网搜索热度，为您呈现今日最火电影、电视剧及综艺排行榜。发现好片，一键在线观看。",
		Keywords:    "电影排行榜, 热搜榜, 热门电影, 电视剧排名, 在线电影搜索, 实时影视热度, 搜索趋势,热门搜索,关键词排行,影视风向",
		Canonical:   platformweb.CanonicalURL(handler.config.SiteURL, "/trends"),
	}, gin.H{
		"Trending24h": items24h,
		"TrendingAll": itemsAll,
		"UpdateTime":  handler.now().Format("15:04"),
	}))
}

// trendItems 取热搜数据并打角标，结果缓存 10 分钟。
func (handler *Handler) trendItems(ctx context.Context, cacheKey string, hours, limit int) []TrendItem {
	if cached, found := handler.trends.Get(cacheKey); found {
		return cached
	}
	items := make([]TrendItem, 0)
	if handler.logger != nil {
		keywords, err := handler.logger.Trending(ctx, hours, limit)
		if err == nil {
			for _, keyword := range keywords {
				item := TrendItem{Keyword: keyword.Keyword, Count: keyword.Count}
				if hours <= 24 {
					if keyword.Count > 100 {
						item.Tag, item.TagClass = "热", "hot"
					} else if keyword.LastSearchedAt.After(handler.now().Add(-time.Hour)) {
						item.Tag, item.TagClass = "新", "new"
					}
				} else if keyword.Count > 4000 {
					item.Tag, item.TagClass = "爆", "bao"
				} else if keyword.Count > 2000 {
					item.Tag, item.TagClass = "热", "hot"
				}
				items = append(items, item)
			}
		}
	}
	handler.trends.Set(cacheKey, items)
	if cacheKey == "24h" && len(items) > 0 {
		handler.warmTrendingCache(items)
	}
	return items
}

// refreshInBackground 异步刷新一条过期的搜索缓存，同一 key 不会并发刷新。
func (handler *Handler) refreshInBackground(cacheKey string, query UnifiedQuery) {
	if handler.unified == nil {
		return
	}
	if _, loaded := handler.refreshing.LoadOrStore(cacheKey, struct{}{}); loaded {
		return
	}
	go func() {
		defer handler.refreshing.Delete(cacheKey)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if result, err := handler.unified.SearchUnified(ctx, query); err == nil {
			handler.cache.Set(cacheKey, result)
		}
	}()
}

// warmTrendingCache 把热搜关键词预热到搜索缓存里，缓存中已有的跳过。
func (handler *Handler) warmTrendingCache(items []TrendItem) {
	if handler.unified == nil {
		return
	}
	for _, item := range items {
		query := UnifiedQuery{Keyword: item.Keyword, Limit: 20}
		cacheKey := unifiedSearchCacheKey(query)
		if _, _, found := handler.cache.GetStale(cacheKey); found {
			continue
		}
		handler.refreshInBackground(cacheKey, query)
	}
}

// hashIP 对来源 IP 做单向哈希后只取前 8 字节，避免明文存储访客 IP。
func hashIP(ip string) string {
	hash := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(hash[:8])
}
