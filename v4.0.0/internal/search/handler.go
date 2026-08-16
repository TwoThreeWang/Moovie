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
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

const maxUnifiedSearchLimit = 100

type Searcher interface {
	Search(ctx context.Context, keyword string, bypassFilter bool) (*Result, error)
}

type Handler struct {
	config  config.Config
	unified UnifiedSearcher
	cache   *Cache[UnifiedResult]
	logger  SearchLogStore
	runner  BackgroundRunner
	trends  *Cache[[]TrendItem]
	now     func() time.Time
}

type HandlerOption func(*Handler)

func WithSearchLogger(logger SearchLogStore, runner BackgroundRunner) HandlerOption {
	return func(handler *Handler) {
		handler.logger = logger
		handler.runner = runner
	}
}

func WithUnifiedSearcher(searcher UnifiedSearcher) HandlerOption {
	return func(handler *Handler) { handler.unified = searcher }
}

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
		cache: NewCache[UnifiedResult](cacheEntries, cacheTTL), trends: NewCache[[]TrendItem](2, 10*time.Minute), now: time.Now}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/search", handler.searchPage)
	router.GET("/api/htmx/search", handler.unifiedSearchHTMX)
	router.GET("/api/v2/search", handler.unifiedSearchAPI)
	router.GET("/trends", handler.trendsPage)
}

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

func (handler *Handler) unifiedSearchHTMX(c *gin.Context) {
	result, ok := handler.runUnifiedSearch(c)
	if !ok {
		return
	}
	if len(result.Items) > 0 || len(result.Unmatched) > 0 {
		handler.recordSearch(c, strings.TrimSpace(c.Query("q")))
	}
	items := make([]UnifiedItem, 0, len(result.Items))
	for _, item := range result.Items {
		if item.ResourceCount > 0 {
			items = append(items, item)
		}
	}
	result.Items = items
	c.HTML(http.StatusOK, "partials/unified_search_results.html", gin.H{"Result": result, "Keyword": c.Query("q")})
}

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
	if mediaType != "" && normalizeUnifiedMediaType(mediaType) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"code": "invalid_type", "message": "type 仅支持 movie 或 tv"})
		return UnifiedResult{}, false
	}
	excludeSourceKey, excludeVodID := excludedResource(c.Query("exclude"))
	query := UnifiedQuery{Keyword: keyword, Year: c.Query("year"), MediaType: mediaType,
		ExcludeSourceKey: excludeSourceKey, ExcludeVodID: excludeVodID,
		BypassFilter: c.Query("bypass") == "1", Limit: limit}
	cacheKey := unifiedSearchCacheKey(query)
	if cached, found := handler.cache.Get(cacheKey); found {
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
	handler.cache.Set(cacheKey, result)
	return result, true
}

func unifiedSearchCacheKey(query UnifiedQuery) string {
	return fmt.Sprintf("search:%s:%s:%s:%s:%s:%t:%d", strings.ToLower(query.Keyword), query.Year,
		normalizeUnifiedMediaType(query.MediaType), query.ExcludeSourceKey, query.ExcludeVodID,
		query.BypassFilter, query.Limit)
}

func excludedResource(value string) (string, string) {
	sourceKey, vodID, found := strings.Cut(strings.TrimSpace(value), ":")
	if !found || strings.TrimSpace(sourceKey) == "" || strings.TrimSpace(vodID) == "" {
		return "", ""
	}
	return strings.TrimSpace(sourceKey), strings.TrimSpace(vodID)
}

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

func (handler *Handler) trendsPage(c *gin.Context) {
	items24h := handler.trendItems(c.Request.Context(), "24h", 24, 20)
	itemsAll := handler.trendItems(c.Request.Context(), "all", 0, 50)
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
				if hours > 0 {
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
	return items
}

func hashIP(ip string) string {
	hash := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(hash[:8])
}
