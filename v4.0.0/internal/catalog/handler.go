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
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
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

// ResourceLister 返回某部影片的统一播放摘要。
type ResourceLister interface {
	PlaybackSummary(ctx context.Context, mediaID int) (search.PlaybackSummary, error)
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
}

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
	handler := &Handler{config: cfg, store: store, httpClient: &http.Client{Timeout: 15 * time.Second}}
	for _, option := range options {
		option(handler)
	}
	handler.httpClient = newImageProxyHTTPClient(handler.httpClient)
	return handler
}

// Register 注册路由：详情页、发现页、图片代理、短评/剧照片段和资料刷新接口。
func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/movie/:id", optional, handler.movie)
	router.GET("/api/htmx/movie-actions", optional, handler.movieActions)
	router.GET("/api/htmx/movie-ready", handler.movieReady)
	router.GET("/api/htmx/movie-playback", handler.moviePlayback)
	router.GET("/api/htmx/movie-series", handler.movieSeries)
	router.GET("/api/proxy/image/:url", handler.proxyImage)
	router.GET("/api/htmx/reviews", handler.reviewList)
	router.GET("/api/htmx/movie-backdrops", handler.backdropList)
	router.POST("/api/v2/media/:media_id/refresh", optional, handler.refreshMediaV2)
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
	if movie.EmbeddingContent == "" {
		handler.queueEmbedding(c.Request.Context(), doubanID)
	}

	keywords := []string{movie.Title}
	if movie.Year != "" {
		keywords = append(keywords, movie.Year)
	}
	if movie.Genres != "" {
		keywords = append(keywords, strings.Split(movie.Genres, ",")...)
	}
	keywords = append(keywords, "在线观看", "免费下载", "高清资源", "Moovie", "影牛")
	var directors []Director
	if json.Unmarshal([]byte(movie.Directors), &directors) != nil {
		directors = []Director{}
	}

	c.HTML(http.StatusOK, "movie.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       "《" + movie.Title + "》 (" + movie.Year + ") - 剧情介绍/演职员表 - " + handler.config.SiteName,
		Description: truncateDescription(movie.Summary, 150), Keywords: strings.Join(keywords, ","),
		Cover: proxyImageURL(movie.Poster), Canonical: fmt.Sprintf("%s/movie/%s", handler.config.SiteURL, movie.DoubanID),
	}, gin.H{
		"Movie": movie, "DirectorList": directors, "SearchTitle": searchTitle,
	}))
}

// movieReady 供 fetching 页轮询：数据就绪时通过 HX-Redirect 跳转到详情页。
func (handler *Handler) movieReady(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	movie, err := handler.store.FindByDoubanID(c.Request.Context(), doubanID)
	if err == nil && movie != nil && movie.Title != "" {
		c.Header("HX-Redirect", "/movie/"+doubanID)
	}
	c.Status(http.StatusOK)
}

// movieActions 返回播放按钮、想看/看过按钮和社交统计（htmx 延迟加载）。
func (handler *Handler) movieActions(c *gin.Context) {
	doubanID := c.Query("douban_id")
	mediaID, _ := strconv.Atoi(c.Query("media_id"))

	var (
		playback                            search.PlaybackSummary
		isWish, isWatched                   bool
		watchedByCount, wishByCount         int
	)
	playback.MediaID = mediaID

	var wg sync.WaitGroup
	if handler.resources != nil && mediaID > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, err := handler.resources.PlaybackSummary(c.Request.Context(), mediaID); err == nil {
				playback = s
			}
		}()
	}
	userID := auth.UserID(c)
	if handler.userMovies != nil {
		if userID > 0 {
			wg.Add(2)
			go func() { defer wg.Done(); isWish, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, doubanID, "wish") }()
			go func() { defer wg.Done(); isWatched, _ = handler.userMovies.IsMarked(c.Request.Context(), userID, doubanID, "watched") }()
		}
		wg.Add(2)
		go func() { defer wg.Done(); watchedByCount, _ = handler.userMovies.CountByMovie(c.Request.Context(), doubanID, "watched") }()
		go func() { defer wg.Done(); wishByCount, _ = handler.userMovies.CountByMovie(c.Request.Context(), doubanID, "wish") }()
	}
	wg.Wait()

	c.HTML(http.StatusOK, "partials/movie_actions.html", gin.H{
		"Playback": playback, "DoubanID": doubanID,
		"IsWish": isWish, "IsWatched": isWatched,
		"WatchedByCount": watchedByCount, "WishByCount": wishByCount,
		"Title": c.Query("title"), "Poster": c.Query("poster"), "Year": c.Query("year"),
	})
}

// moviePlayback 返回在线资源区块（htmx 延迟加载）。
func (handler *Handler) moviePlayback(c *gin.Context) {
	mediaID, _ := strconv.Atoi(c.Query("media_id"))
	playback := search.PlaybackSummary{MediaID: mediaID, State: search.PlaybackNone}
	if handler.resources != nil && mediaID > 0 {
		if s, err := handler.resources.PlaybackSummary(c.Request.Context(), mediaID); err == nil {
			playback = s
		}
	}
	searchTitle := c.Query("search_title")
	if searchTitle == "" {
		searchTitle = c.Query("title")
	}
	c.HTML(http.StatusOK, "partials/movie_playback.html", gin.H{
		"Playback": playback, "DoubanID": c.Query("douban_id"),
		"Title": c.Query("title"), "SearchTitle": searchTitle, "Year": c.Query("year"),
	})
}

// movieSeries 返回系列季度导航和播出日程（htmx 延迟加载）。
func (handler *Handler) movieSeries(c *gin.Context) {
	doubanID := c.Query("douban_id")
	movie, err := handler.store.FindByDoubanID(c.Request.Context(), doubanID)
	if err != nil || movie == nil {
		c.String(http.StatusOK, "")
		return
	}
	var (
		seriesSeasons []SeriesSeason
		airSchedule   mediaidentity.AirScheduleView
	)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); seriesSeasons = handler.findSeriesSeasons(c.Request.Context(), doubanID) }()
	go func() { defer wg.Done(); airSchedule = handler.airScheduleView(c.Request.Context(), movie) }()
	wg.Wait()

	c.HTML(http.StatusOK, "partials/movie_series.html", gin.H{
		"SeriesSeasons": seriesSeasons, "AirSchedule": airSchedule,
	})
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
