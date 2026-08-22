package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/cache"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

// UserMovies 提供想看/看过的标记状态和人数（详情页按钮用）。
type UserMovies interface {
	IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error)
	CountByMovie(ctx context.Context, movieID, status string) (int, error)
}

// Fetcher 抓取影片主资料。
type Fetcher interface {
	Fetch(ctx context.Context, doubanID string, force bool) error
}

// ReviewFetcher 抓取豆瓣短评。
type ReviewFetcher interface {
	FetchReviews(ctx context.Context, doubanID string) error
}

// Suggester 提供搜索联想。
type Suggester interface {
	Suggest(ctx context.Context, keyword string) ([]Suggestion, error)
}

// PopularProvider 提供发现页的热门榜单。
type PopularProvider interface {
	Popular(ctx context.Context, movieType string) ([]PopularSubject, error)
}

// SimilarFinder 提供相似影片。
type SimilarFinder interface {
	FindSimilar(ctx context.Context, doubanID string, limit int) ([]Movie, error)
}

// SeriesFinder 提供同系列的各季（详情页季度导航）。
type SeriesFinder interface {
	FindSeriesSeasons(ctx context.Context, doubanID string) ([]SeriesSeason, error)
}

// BackdropSyncer 同步 TMDB 剧照。
type BackdropSyncer interface {
	SyncBackdrops(ctx context.Context, doubanID string) error
}

// VectorEnricher 生成语义向量。
type VectorEnricher interface {
	Enrich(ctx context.Context, doubanID string) error
}

// Review 是一条豆瓣短评，以 JSON 数组形式存在 media.reviews_json。
type Review struct {
	Title     string `json:"title"`
	Author    string `json:"author"`
	Link      string `json:"link"`
	Published string `json:"published"`
	Summary   string `json:"summary"`
}

// BackgroundRunner 在请求之外跑后台任务（没有 Worker 时的兜底路径）。
type BackgroundRunner interface {
	Run(task func(context.Context))
}

// acceptingBackgroundRunner 能在提交前判断后台队列是否还收得下任务。
type acceptingBackgroundRunner interface {
	TryRun(task func(context.Context)) bool
}

// ResourceLister 判断某部影片是否已有可播放资源。
type ResourceLister interface {
	HasPlayableResource(ctx context.Context, mediaID int) (bool, error)
}

// AirScheduleReader 提供某部作品尚未播出的剧集，用于详情页展示更新时间。
// 为 nil 时详情页安全降级为不展示该区块。
type AirScheduleReader interface {
	ListUpcomingUnits(ctx context.Context, mediaID, seasonNumber int, from time.Time, limit int) ([]mediaidentity.MediaUnit, error)
}

// Handler 提供影片详情页、发现页、图片代理和资料刷新接口。
// 各种能力都通过 Option 注入，缺失时对应区块自动降级为不展示。
type Handler struct {
	config       config.Config
	store        Store
	userMovies   UserMovies
	fetcher      Fetcher
	reviews      ReviewFetcher
	backdrops    BackdropSyncer
	vectors      VectorEnricher
	suggester    Suggester
	popular      PopularProvider
	trending     PopularProvider
	similar      SimilarFinder
	resources    ResourceLister
	airSchedule  AirScheduleReader
	runner       BackgroundRunner
	refreshQueue RefreshQueue
	httpClient   *http.Client
	crawling     sync.Map
	similarCache *cache.TTL[[]Movie]
	similarSF    singleflight.Group
}

// similarCacheTTL 是相似影片的缓存时长。
const similarCacheTTL = 5 * time.Minute

// similarCacheCapacity 是相似影片的缓存条数上限。
const similarCacheCapacity = 256

// HandlerOption 是 Handler 的可选装配项，下面这一组 WithXxx 都只是往结构体里塞一个依赖。
type HandlerOption func(*Handler)

// WithUserMovies 注入片单存储，用于在详情页显示「想看/看过」状态。
func WithUserMovies(store UserMovies) HandlerOption {
	return func(handler *Handler) { handler.userMovies = store }
}

// WithFetcher 注入元数据抓取器和后台执行器。
func WithFetcher(fetcher Fetcher, runner BackgroundRunner) HandlerOption {
	return func(handler *Handler) { handler.fetcher, handler.runner = fetcher, runner }
}

// WithHTTPClient 注入图片代理用的 HTTP 客户端。
func WithHTTPClient(client *http.Client) HandlerOption {
	return func(handler *Handler) { handler.httpClient = client }
}

// WithBackgroundRunner 注入后台执行器。
func WithBackgroundRunner(runner BackgroundRunner) HandlerOption {
	return func(handler *Handler) { handler.runner = runner }
}

// WithRefreshQueue 注入元数据刷新队列。
func WithRefreshQueue(queue RefreshQueue) HandlerOption {
	return func(handler *Handler) { handler.refreshQueue = queue }
}

// WithReviewFetcher 注入短评抓取器。
func WithReviewFetcher(fetcher ReviewFetcher) HandlerOption {
	return func(handler *Handler) { handler.reviews = fetcher }
}

// WithBackdropSyncer 注入剧照同步器。
func WithBackdropSyncer(syncer BackdropSyncer) HandlerOption {
	return func(handler *Handler) { handler.backdrops = syncer }
}

// WithVectorEnricher 注入向量补全器。
func WithVectorEnricher(enricher VectorEnricher) HandlerOption {
	return func(handler *Handler) { handler.vectors = enricher }
}

// WithSuggester 注入搜索建议实现。
func WithSuggester(suggester Suggester) HandlerOption {
	return func(handler *Handler) { handler.suggester = suggester }
}

// WithPopularProvider 注入热门榜数据源。
func WithPopularProvider(provider PopularProvider) HandlerOption {
	return func(handler *Handler) { handler.popular = provider }
}

// WithSiteTrending 注入本站热播数据源。
func WithSiteTrending(provider PopularProvider) HandlerOption {
	return func(handler *Handler) { handler.trending = provider }
}

// WithSimilarFinder 注入相似影片查询。
func WithSimilarFinder(finder SimilarFinder) HandlerOption {
	return func(handler *Handler) { handler.similar = finder }
}

// WithResourceLister 注入资源可播性查询，用于判断详情页能不能播。
func WithResourceLister(lister ResourceLister) HandlerOption {
	return func(handler *Handler) { handler.resources = lister }
}

// WithAirScheduleReader 注入播出时间表读取。
func WithAirScheduleReader(reader AirScheduleReader) HandlerOption {
	return func(handler *Handler) { handler.airSchedule = reader }
}

// NewHandler 构造详情页 Handler，并给出站 HTTP Client 套上图片代理的安全拦截。
func NewHandler(cfg config.Config, store Store, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, store: store, httpClient: &http.Client{Timeout: 15 * time.Second},
		similarCache: cache.New[[]Movie](similarCacheCapacity, similarCacheTTL)}
	for _, option := range options {
		option(handler)
	}
	handler.httpClient = newImageProxyHTTPClient(handler.httpClient)
	return handler
}

// Register 注册路由：详情页、发现页、图片代理、短评/剧照片段和资料刷新接口。
func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/movie/:id", auth.Optional(handler.config.AppSecret), handler.movie)
	router.GET("/api/proxy/image/:url", handler.proxyImage)
	router.GET("/api/htmx/reviews", handler.reviewList)
	router.GET("/api/htmx/movie-backdrops", handler.backdropList)
	router.POST("/api/v2/media/:media_id/refresh", auth.Optional(handler.config.AppSecret), handler.refreshMediaV2)
	router.GET("/api/v2/media/suggest", handler.movieSuggest)
	router.GET("/discover", handler.discover)
	router.GET("/discover/:type", handler.discover)
}

// discover 渲染发现页；HTMX 请求只返回卡片网格，整页请求会带上各分类专属的 SEO 文案。
func (handler *Handler) discover(c *gin.Context) {
	movieType := c.Param("type")
	if strings.TrimSpace(movieType) == "" {
		movieType = c.Query("type")
	}
	movieType = normalizeDiscoverType(movieType)
	if c.GetHeader("HX-Request") != "" {
		subjects := []PopularSubject{}
		provider := handler.popular
		if movieType == "trending" {
			provider = handler.trending
		}
		if provider != nil {
			var err error
			subjects, err = provider.Popular(c.Request.Context(), movieType)
			if err != nil {
				requestmeta.Logger(c.Request.Context()).Warn("discover snapshot unavailable", "media_type", movieType, "error", err)
				c.Status(http.StatusServiceUnavailable)
				return
			}
		}
		c.HTML(http.StatusOK, "partials/discover_grid.html", gin.H{"Subjects": subjects, "CurrentType": movieType})
		return
	}
	title := "2026豆瓣高分电影推荐 - 热门在线电影发现"
	description := "发现最新上映及豆瓣高分电影，涵盖动作、科幻、悬疑等多种题材，实时同步全网热度。"
	keywords := "热门电影,最新电视剧,高分佳作,Moovie影牛发现"
	switch movieType {
	case "tv":
		title = "2026近期热门电视剧排行榜 - 好剧推荐在线看"
		description = "为您整理近期最火的电视剧、国产剧、美剧及韩剧，支持全网资源搜索与在线播放。"
		keywords = "热门电视剧,最新电视剧,高分佳作,Moovie影牛发现"
	case "show":
		title = "2026豆瓣高分综艺推荐 - 热门在线综艺发现"
		description = "发现最新、最热的综艺，满足你的综艺需求。"
		keywords = "热门综艺,最新综艺,高分佳作,Moovie影牛发现"
	case "cartoon":
		title = "2026热门动漫新番推荐 - 豆瓣高分动画榜单"
		description = "发现本季最强新番及经典高分动漫，支持多线路高清搜索。"
		keywords = "热门动漫,最新动漫,高分佳作,Moovie影牛发现"
	case "trending":
		title = "本站热播排行榜 - 最近7天站内最多人观看"
		description = "最近一周站内观众正在看的热门影视作品，基于真实播放数据实时排行。"
		keywords = "本站热播,热门影视,在线观看排行,Moovie影牛"
	}
	c.HTML(http.StatusOK, "discover.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: title + " - " + handler.config.SiteName, Description: description, Keywords: keywords,
		Canonical: platformweb.CanonicalURL(handler.config.SiteURL, "/discover/"+movieType),
	}, gin.H{"CurrentType": movieType}))
}

// normalizeDiscoverType 把类型参数限制在 movie/tv/show/cartoon/trending 五种。
func normalizeDiscoverType(movieType string) string {
	switch strings.ToLower(strings.TrimSpace(movieType)) {
	case "tv":
		return "tv"
	case "show":
		return "show"
	case "cartoon":
		return "cartoon"
	case "trending":
		return "trending"
	default:
		return "movie"
	}
}

// movieSuggest 返回搜索联想的 JSON。
func (handler *Handler) movieSuggest(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("q"))
	if keyword == "" {
		catalogAPIError(c, http.StatusBadRequest, "搜索关键词不能为空")
		return
	}
	if handler.suggester == nil {
		catalogAPIError(c, http.StatusInternalServerError, "搜索建议服务暂时不可用")
		return
	}
	results, err := handler.suggester.Suggest(c.Request.Context(), keyword)
	if err != nil {
		catalogAPIError(c, http.StatusInternalServerError, "搜索建议服务暂时不可用")
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "success", "data": results, "success": true})
}

// refreshMediaV2 是手动触发资料刷新的接口，立即返回任务 ID，不等抓取完成。
func (handler *Handler) refreshMediaV2(c *gin.Context) {
	mediaID, err := strconv.Atoi(c.Param("media_id"))
	if err != nil || mediaID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的影视ID"})
		return
	}
	queue, ok := handler.refreshQueue.(MediaRefreshQueue)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "电影资料刷新队列暂时不可用"})
		return
	}
	jobID, err := queue.EnqueueMediaRefresh(c.Request.Context(), mediaID, "manual", auth.UserID(c))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "电影资料刷新入队失败"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "已加入刷新队列", "job_id": jobID, "media_id": mediaID})
}

// reviewList 返回短评片段。只有本地短评为空时才排抓取任务；
// 已经采集过的短评不做定时更新。没有任务队列就退回进程内后台抓取。
func (handler *Handler) reviewList(c *gin.Context) {
	doubanID := strings.TrimSpace(c.Query("douban_id"))
	if doubanID == "" {
		c.String(http.StatusBadRequest, "豆瓣 ID 不能为空")
		return
	}
	movie, err := handler.store.FindByDoubanID(c.Request.Context(), doubanID)
	if err == nil && movie != nil && movie.ReviewsJSON != "" {
		var reviews []Review
		if json.Unmarshal([]byte(movie.ReviewsJSON), &reviews) == nil {
			c.HTML(http.StatusOK, "partials/reviews.html", gin.H{"Reviews": reviews})
			return
		}
	}
	if queued, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderReviews, RefreshReasonMissingReviews); queued {
		if queueErr != nil {
			c.HTML(http.StatusServiceUnavailable, "partials/reviews.html", gin.H{"Error": "短评采集任务入队失败，请稍后重试"})
			return
		}
		c.HTML(http.StatusOK, "partials/reviews.html", gin.H{"Reviews": nil, "Message": "短评采集任务已加入 Worker 队列，请稍后刷新"})
		return
	}
	handler.queueReviewRefresh(doubanID)
	c.HTML(http.StatusOK, "partials/reviews.html", gin.H{"Reviews": nil, "Message": "正在从豆瓣采集精彩短评..."})
}

// backdropList 返回剧照片段，逻辑与 reviewList 相同：缺数据就排任务并提示稍后刷新。
func (handler *Handler) backdropList(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" {
		c.String(http.StatusOK, "")
		return
	}
	movie, err := handler.store.FindByDoubanID(c.Request.Context(), doubanID)
	if err != nil || movie == nil {
		c.String(http.StatusOK, "")
		return
	}
	if movie.Backdrops == "" && handler.backdrops != nil && (handler.refreshQueue != nil || handler.runner != nil) {
		if checker, ok := handler.store.(TMDBRefreshChecker); ok {
			needed, checkErr := checker.NeedsTMDBRefresh(c.Request.Context(), doubanID)
			if checkErr != nil {
				c.String(http.StatusServiceUnavailable, `<div class="reviews-error">剧照采集状态读取失败，请稍后重试</div>`)
				return
			}
			if !needed {
				c.HTML(http.StatusOK, "partials/movie_backdrops.html", gin.H{"Backdrops": []string{}})
				return
			}
		}
		if queued, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderTMDB, RefreshReasonMissingBackdrops); queued {
			if queueErr != nil {
				c.String(http.StatusServiceUnavailable, `<div class="reviews-error">剧照采集任务入队失败，请稍后重试</div>`)
				return
			}
			c.String(http.StatusOK, `<div class="reviews-empty">剧照采集任务已加入 Worker 队列，请稍后刷新</div>`)
			return
		}
		key := "backdrops:" + doubanID
		if _, loaded := handler.crawling.LoadOrStore(key, struct{}{}); !loaded {
			if !handler.runBackground(func(ctx context.Context) {
				defer handler.crawling.Delete(key)
				_ = handler.backdrops.SyncBackdrops(ctx, doubanID)
			}) {
				handler.crawling.Delete(key)
				c.String(http.StatusServiceUnavailable, `<div class="reviews-empty">服务繁忙，请稍后重试</div>`)
				return
			}
		}
		c.String(http.StatusOK, `<div class="reviews-empty">正在后台采集精彩剧照...</div>`)
		return
	}
	backdrops := []string{}
	if movie.Backdrops != "" {
		backdrops = strings.Split(movie.Backdrops, ",")
	}
	c.HTML(http.StatusOK, "partials/movie_backdrops.html", gin.H{"Backdrops": backdrops})
}

// queueReviewRefresh 是没有任务队列时的兜底：进程内抓一次，用 crawling 这张表防止重复。
func (handler *Handler) queueReviewRefresh(doubanID string) {
	if handler.reviews == nil || handler.runner == nil {
		return
	}
	key := "reviews:" + doubanID
	if _, loaded := handler.crawling.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if !handler.runBackground(func(ctx context.Context) {
		defer handler.crawling.Delete(key)
		_ = handler.reviews.FetchReviews(ctx, doubanID)
	}) {
		handler.crawling.Delete(key)
	}
}

// movie 渲染影片详情页。本地没有资料时先渲染一个「正在抓取」的过渡页，
// 资料不完整时顺手排一个补全任务；相似推荐、季度导航、更新时间表任一环节失败都只是少一个区块。
func (handler *Handler) movie(c *gin.Context) {
	doubanID, searchTitle := c.Param("id"), c.Query("title")
	movie, err := handler.store.FindByDoubanID(c.Request.Context(), doubanID)
	if movie != nil && movie.Title == "" {
		_ = handler.store.DeleteByDoubanID(c.Request.Context(), doubanID)
		movie = nil
	}
	if err != nil || movie == nil {
		_, queueErr := handler.fetchMissing(c.Request.Context(), doubanID)
		if queueErr != nil {
			requestmeta.Logger(c.Request.Context()).Warn("queue missing metadata", "douban_id", doubanID, "error", queueErr)
		}
		c.HTML(http.StatusOK, "fetching.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: searchTitle}, gin.H{
			"Title": searchTitle, "DoubanID": doubanID,
		}))
		return
	}
	if needsMetadataRefresh(movie, time.Now()) {
		if _, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderDouban, RefreshReasonPartialMetadata); queueErr != nil {
			requestmeta.Logger(c.Request.Context()).Warn("queue partial metadata", "douban_id", doubanID, "error", queueErr)
		}
	}

	userID := auth.UserID(c)
	isWish, isWatched := false, false
	watchedByCount, wishByCount := 0, 0
	if handler.userMovies != nil {
		if userID > 0 {
			isWish, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, movie.DoubanID, "wish")
			isWatched, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, movie.DoubanID, "watched")
		}
		watchedByCount, _ = handler.userMovies.CountByMovie(c.Request.Context(), movie.DoubanID, "watched")
		wishByCount, _ = handler.userMovies.CountByMovie(c.Request.Context(), movie.DoubanID, "wish")
	}

	keywords := []string{movie.Title}
	if movie.Year != "" {
		keywords = append(keywords, movie.Year)
	}
	if movie.Genres != "" {
		keywords = append(keywords, strings.Split(movie.Genres, ",")...)
	}
	keywords = append(keywords, "在线观看", "免费下载", "高清资源", "Moovie", "影牛")
	description := truncateDescription(movie.Summary, 150)
	var directors []Director
	if json.Unmarshal([]byte(movie.Directors), &directors) != nil {
		directors = []Director{}
	}
	seriesSeasons := handler.findSeriesSeasons(c.Request.Context(), doubanID)
	similarMovies := excludeSeriesMovies(
		handler.findSimilar(c.Request.Context(), doubanID, 6+len(seriesSeasons)), seriesSeasons,
		mediaidentity.TitleBase(movie.Title, movie.OriginalTitle), 6)
	if movie.EmbeddingContent == "" {
		handler.queueEmbedding(c.Request.Context(), doubanID)
	}
	airSchedule := handler.airScheduleView(c.Request.Context(), movie)
	hasPlayableResource := false
	if handler.resources != nil {
		if playable, resourceErr := handler.resources.HasPlayableResource(c.Request.Context(), movie.ID); resourceErr == nil {
			hasPlayableResource = playable
		}
	}

	c.HTML(http.StatusOK, "movie.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       "《" + movie.Title + "》 (" + movie.Year + ") - 剧情介绍/演职员表 - " + handler.config.SiteName,
		Description: description, Keywords: strings.Join(keywords, ","),
		Cover: proxyImageURL(movie.Poster), Canonical: fmt.Sprintf("%s/movie/%s", handler.config.SiteURL, movie.DoubanID),
	}, gin.H{
		"Movie": movie, "IsWish": isWish, "IsWatched": isWatched,
		"WatchedByCount": watchedByCount, "WishByCount": wishByCount,
		"DirectorList": directors, "SearchTitle": searchTitle, "SimilarMovies": similarMovies,
		"SeriesSeasons": seriesSeasons,
		"AirSchedule":   airSchedule, "HasPlayableResource": hasPlayableResource,
	}))
}

// findSeriesSeasons 取同系列各季，少于两季就不展示导航。
func (handler *Handler) findSeriesSeasons(ctx context.Context, doubanID string) []SeriesSeason {
	finder, ok := handler.store.(SeriesFinder)
	if !ok || doubanID == "" {
		return nil
	}
	seasons, err := finder.FindSeriesSeasons(ctx, doubanID)
	if err != nil {
		requestmeta.Logger(ctx).Warn("load series seasons failed", "douban_id", doubanID, "error", err)
		return nil
	}
	if len(seasons) < 2 {
		return nil
	}
	return seasons
}

// excludeSeriesMovies 把同系列的其他季从「相似推荐」里剔除，避免推荐区全是自己。
func excludeSeriesMovies(movies []Movie, seasons []SeriesSeason, seriesTitle string, limit int) []Movie {
	if limit <= 0 {
		return []Movie{}
	}
	excluded := make(map[string]struct{}, len(seasons))
	for _, season := range seasons {
		excluded[season.DoubanID] = struct{}{}
	}
	filtered := make([]Movie, 0, min(limit, len(movies)))
	for _, movie := range movies {
		if _, sameSeries := excluded[movie.DoubanID]; sameSeries {
			continue
		}
		// 未完成 TMDB 绑定的季度也不该混入推荐；这里只过滤，不据此建立系列关系。
		if len(seasons) > 0 && mediaidentity.TitleBase(movie.Title, movie.OriginalTitle) == seriesTitle {
			continue
		}
		filtered = append(filtered, movie)
		if len(filtered) == limit {
			break
		}
	}
	return filtered
}

// needsMetadataRefresh 判断资料是否需要补全：状态是 partial 或完整度低于 70 分，且已过下次刷新时间。
func needsMetadataRefresh(movie *Movie, now time.Time) bool {
	if movie == nil || movie.DoubanID == "" || (movie.MetadataStatus != "partial" && movie.CompletenessScore >= 70) {
		return false
	}
	return movie.NextRefreshAt == nil || !movie.NextRefreshAt.After(now)
}

// airScheduleUpcomingLimit 限制详情页一次展示的未播出集数。
// 追剧只关心最近几集；预告到半年后的集次既不准确，也会把选集区挤走。
const airScheduleUpcomingLimit = 8

// airScheduleView 组装"下一集何时更新"区块。
// 任何一步缺数据都返回零值视图，由模板决定整块不渲染——
// 这里刻意不返回 error：更新时间是详情页的增量信息，不该让它的失败影响主内容。
func (handler *Handler) airScheduleView(ctx context.Context, movie *Movie) mediaidentity.AirScheduleView {
	if handler.airSchedule == nil || movie == nil || movie.ID <= 0 {
		return mediaidentity.AirScheduleView{}
	}
	if mediaidentity.SeriesEnded(movie.SeriesStatus) {
		return mediaidentity.AirScheduleView{}
	}
	location := mediaidentity.AiringLocation(handler.config.Database.TimeZone)
	now := time.Now()
	seasonNumber := mediaidentity.TitleSeasonNumber(movie.Title, movie.OriginalTitle)
	units, err := handler.airSchedule.ListUpcomingUnits(ctx, movie.ID, seasonNumber,
		mediaidentity.AiringDay(now, location), airScheduleUpcomingLimit)
	if err != nil {
		requestmeta.Logger(ctx).Warn("load air schedule failed", "media_id", movie.ID, "error", err)
		return mediaidentity.AirScheduleView{}
	}
	return mediaidentity.BuildAirScheduleView(movie.SeriesStatus, units, now, location)
}

// findSimilar 让同一影片第一次请求后的向量查询离开热点路径，
// 并合并并发请求，防止热门详情页在冷缓存突发时击穿向量索引。
func (handler *Handler) findSimilar(ctx context.Context, doubanID string, limit int) []Movie {
	if handler.similar == nil || doubanID == "" || limit == 0 {
		return []Movie{}
	}
	key := fmt.Sprintf("%s:%d", doubanID, limit)
	if movies, ok := handler.cachedSimilar(key); ok {
		return movies
	}
	value, err, _ := handler.similarSF.Do(key, func() (any, error) {
		if movies, ok := handler.cachedSimilar(key); ok {
			return movies, nil
		}
		movies, err := handler.similar.FindSimilar(ctx, doubanID, limit)
		if err != nil {
			return nil, err
		}
		copyMovies := compactSimilarMovies(movies)
		handler.similarCache.Set(key, copyMovies)
		return copyMovies, nil
	})
	if err != nil || value == nil {
		return []Movie{}
	}
	return append([]Movie(nil), value.([]Movie)...)
}

// cachedSimilar 读相似推荐缓存。返回的是副本，防止调用方改到缓存里的数据。
func (handler *Handler) cachedSimilar(key string) ([]Movie, bool) {
	movies, ok := handler.similarCache.Get(key)
	if !ok {
		return nil, false
	}
	return append([]Movie(nil), movies...), true
}

// 相似卡片只渲染身份、标题、海报、评分和年份。
// 只缓存这些字段可避免为每部访问过的影片长期保留简介、演员 JSON 和向量。
func compactSimilarMovies(movies []Movie) []Movie {
	compact := make([]Movie, 0, len(movies))
	for _, movie := range movies {
		compact = append(compact, Movie{
			ID: movie.ID, DoubanID: movie.DoubanID, Title: movie.Title, Year: movie.Year,
			Poster: movie.Poster, Rating: movie.Rating, Genres: movie.Genres,
		})
	}
	return compact
}

// queueEmbedding 为缺向量的影片排一个生成任务。
func (handler *Handler) queueEmbedding(ctx context.Context, doubanID string) {
	if queued, err := handler.enqueueRefresh(ctx, doubanID, RefreshProviderEmbedding, RefreshReasonMissingEmbedding); queued {
		if err != nil {
			requestmeta.Logger(ctx).Warn("queue missing embedding", "douban_id", doubanID, "error", err)
		}
		return
	}
	if handler.vectors == nil || handler.runner == nil {
		return
	}
	key := "embedding:" + doubanID
	if _, loaded := handler.crawling.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	if !handler.runBackground(func(ctx context.Context) {
		defer handler.crawling.Delete(key)
		_ = handler.vectors.Enrich(ctx, doubanID)
	}) {
		handler.crawling.Delete(key)
	}
}

// fetchMissing 为本地没有资料的影片排一个抓取任务；第一个返回值表示是否走了任务队列。
func (handler *Handler) fetchMissing(ctx context.Context, doubanID string) (bool, error) {
	if queued, err := handler.enqueueRefresh(ctx, doubanID, RefreshProviderDouban, RefreshReasonMissingMetadata); queued {
		return true, err
	}
	if handler.fetcher == nil || handler.runner == nil || doubanID == "" {
		return false, nil
	}
	if _, loaded := handler.crawling.LoadOrStore(doubanID, struct{}{}); loaded {
		return false, nil
	}
	if !handler.runBackground(func(ctx context.Context) {
		defer handler.crawling.Delete(doubanID)
		_ = handler.fetcher.Fetch(ctx, doubanID, false)
	}) {
		handler.crawling.Delete(doubanID)
	}
	return false, nil
}

// enqueueRefresh 统一入队。第一个返回值表示「有队列可用」，第二个才是入队是否出错。
func (handler *Handler) enqueueRefresh(ctx context.Context, doubanID, provider, reason string) (bool, error) {
	if handler.refreshQueue == nil {
		return false, nil
	}
	_, err := handler.refreshQueue.EnqueueRefresh(ctx, doubanID, provider, reason, 0)
	return true, err
}

// runBackground 提交后台任务，执行器支持拒绝时返回 false（此时页面会提示稍后重试）。
func (handler *Handler) runBackground(task func(context.Context)) bool {
	if accepting, ok := handler.runner.(acceptingBackgroundRunner); ok {
		return accepting.TryRun(task)
	}
	handler.runner.Run(task)
	return true
}

// truncateDescription 按字符数截断简介（SEO 描述用），按 rune 计数以免截断汉字。
func truncateDescription(summary string, limit int) string {
	runes := []rune(strings.TrimSpace(summary))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

// proxyImageURL 把外站图片地址改写成本站图片代理地址（豆瓣图片有防盗链）。
func proxyImageURL(value string) string {
	if value == "" || strings.HasPrefix(value, "/api/proxy/image") {
		return value
	}
	return "/api/proxy/image/r76RqSIVvUryzx" + base64.RawURLEncoding.EncodeToString([]byte(value))
}
