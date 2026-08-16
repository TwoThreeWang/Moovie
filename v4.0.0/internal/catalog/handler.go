package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

type UserMovies interface {
	IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error)
	CountByMovie(ctx context.Context, movieID, status string) (int, error)
}

type Fetcher interface {
	Fetch(ctx context.Context, doubanID string, force bool) error
}

type ReviewFetcher interface {
	FetchReviews(ctx context.Context, doubanID string) error
}

type Suggester interface {
	Suggest(ctx context.Context, keyword string) ([]Suggestion, error)
}

type externalSuggester interface {
	SuggestExternal(ctx context.Context, keyword string) ([]Suggestion, error)
}

type PopularProvider interface {
	Popular(ctx context.Context, movieType string) ([]PopularSubject, error)
}

type SimilarFinder interface {
	FindSimilar(ctx context.Context, doubanID string, limit int) ([]Movie, error)
}

type SeriesFinder interface {
	FindSeriesSeasons(ctx context.Context, doubanID string) ([]SeriesSeason, error)
}

type BackdropSyncer interface {
	SyncBackdrops(ctx context.Context, doubanID string) error
}

type VectorEnricher interface {
	Enrich(ctx context.Context, doubanID string) error
}

type Review struct {
	Title     string `json:"title"`
	Author    string `json:"author"`
	Link      string `json:"link"`
	Published string `json:"published"`
	Summary   string `json:"summary"`
}

type BackgroundRunner interface {
	Run(task func(context.Context))
}

type acceptingBackgroundRunner interface {
	TryRun(task func(context.Context)) bool
}

// LinkedResource 保存豆瓣卡片中渲染搜索结果样式资源卡所需的 vod_item 信息。
type LinkedResource struct {
	SourceKey   string
	VodID       string
	VodName     string
	VodPic      string
	VodYear     string
	VodArea     string
	TypeName    string
	VodActor    string
	VodRemarks  string
	VodDoubanID string
	AvgSpeedMs  int
	SampleCount int
	FailedCount int
}

func (r LinkedResource) SuccessRate() float64 {
	if r.SampleCount <= 0 {
		return 0
	}
	successes := r.SampleCount - r.FailedCount
	if successes < 0 {
		successes = 0
	}
	return float64(successes) / float64(r.SampleCount) * 100
}

type ResourceLister interface {
	ListResourcesByDoubanID(ctx context.Context, doubanID string) ([]LinkedResource, error)
	HasPlayableResource(ctx context.Context, mediaID int) (bool, error)
}

type DoubanMatch struct {
	DoubanID      string
	Title         string
	OriginalTitle string
	Year          string
	Poster        string
	Rating        float64
	IsLocal       bool
}

// AirScheduleReader 提供某部作品尚未播出的剧集，用于详情页展示更新时间。
// 为 nil 时详情页安全降级为不展示该区块。
type AirScheduleReader interface {
	ListUpcomingUnits(ctx context.Context, mediaID, seasonNumber int, from time.Time, limit int) ([]mediaidentity.MediaUnit, error)
}

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
	similar      SimilarFinder
	resources    ResourceLister
	airSchedule  AirScheduleReader
	runner       BackgroundRunner
	refreshQueue RefreshQueue
	httpClient   *http.Client
	crawling     sync.Map
	similarMu    sync.Mutex
	similarCache map[string]similarCacheEntry
	similarSF    singleflight.Group
}

const similarCacheTTL = 5 * time.Minute
const similarCacheCapacity = 256

type similarCacheEntry struct {
	movies    []Movie
	expiresAt time.Time
}

type HandlerOption func(*Handler)

func WithUserMovies(store UserMovies) HandlerOption {
	return func(handler *Handler) { handler.userMovies = store }
}

func WithFetcher(fetcher Fetcher, runner BackgroundRunner) HandlerOption {
	return func(handler *Handler) { handler.fetcher, handler.runner = fetcher, runner }
}

func WithHTTPClient(client *http.Client) HandlerOption {
	return func(handler *Handler) { handler.httpClient = client }
}

func WithBackgroundRunner(runner BackgroundRunner) HandlerOption {
	return func(handler *Handler) { handler.runner = runner }
}

func WithRefreshQueue(queue RefreshQueue) HandlerOption {
	return func(handler *Handler) { handler.refreshQueue = queue }
}

func WithReviewFetcher(fetcher ReviewFetcher) HandlerOption {
	return func(handler *Handler) { handler.reviews = fetcher }
}

func WithBackdropSyncer(syncer BackdropSyncer) HandlerOption {
	return func(handler *Handler) { handler.backdrops = syncer }
}

func WithVectorEnricher(enricher VectorEnricher) HandlerOption {
	return func(handler *Handler) { handler.vectors = enricher }
}

func WithSuggester(suggester Suggester) HandlerOption {
	return func(handler *Handler) { handler.suggester = suggester }
}

func WithPopularProvider(provider PopularProvider) HandlerOption {
	return func(handler *Handler) { handler.popular = provider }
}

func WithSimilarFinder(finder SimilarFinder) HandlerOption {
	return func(handler *Handler) { handler.similar = finder }
}

func WithResourceLister(lister ResourceLister) HandlerOption {
	return func(handler *Handler) { handler.resources = lister }
}

func WithAirScheduleReader(reader AirScheduleReader) HandlerOption {
	return func(handler *Handler) { handler.airSchedule = reader }
}

func NewHandler(cfg config.Config, store Store, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, store: store, httpClient: &http.Client{Timeout: 15 * time.Second}}
	for _, option := range options {
		option(handler)
	}
	handler.httpClient = newImageProxyHTTPClient(handler.httpClient)
	return handler
}

func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/movie/:id", auth.Optional(handler.config.AppSecret), handler.movie)
	router.GET("/api/proxy/image/:url", handler.proxyImage)
	router.GET("/api/htmx/reviews", handler.reviewList)
	router.GET("/api/htmx/movie-backdrops", handler.backdropList)
	router.POST("/api/v2/media/:media_id/refresh", auth.Optional(handler.config.AppSecret), handler.refreshMediaV2)
	router.GET("/api/v2/media/suggest", handler.movieSuggest)
	router.GET("/discover", handler.discover)
	router.GET("/discover/:type", handler.discover)
	router.GET("/api/htmx/douban-card", handler.doubanCard)
}

func (handler *Handler) discover(c *gin.Context) {
	movieType := c.Param("type")
	if strings.TrimSpace(movieType) == "" {
		movieType = c.Query("type")
	}
	movieType = normalizeDiscoverType(movieType)
	if c.GetHeader("HX-Request") != "" {
		subjects := []PopularSubject{}
		if handler.popular != nil {
			subjects, _ = handler.popular.Popular(c.Request.Context(), movieType)
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
	}
	c.HTML(http.StatusOK, "discover.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: title + " - " + handler.config.SiteName, Description: description, Keywords: keywords,
		Canonical: platformweb.CanonicalURL(handler.config.SiteURL, "/discover/"+movieType),
	}, gin.H{"CurrentType": movieType}))
}

func normalizeDiscoverType(movieType string) string {
	switch strings.ToLower(strings.TrimSpace(movieType)) {
	case "tv":
		return "tv"
	case "show":
		return "show"
	case "cartoon":
		return "cartoon"
	default:
		return "movie"
	}
}

func (handler *Handler) doubanCard(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("kw"))
	if keyword == "" {
		c.String(http.StatusOK, "")
		return
	}
	movies, err := handler.store.Suggest(c.Request.Context(), keyword, 5)
	if err != nil {
		c.String(http.StatusOK, "")
		return
	}
	matches := make([]DoubanMatch, 0, 5)
	seen := make(map[string]bool, 5)
	for _, movie := range movies {
		if movie.DoubanID == "" || seen[movie.DoubanID] {
			continue
		}
		seen[movie.DoubanID] = true
		matches = append(matches, DoubanMatch{DoubanID: movie.DoubanID, Title: movie.Title,
			OriginalTitle: movie.OriginalTitle, Year: movie.Year, Poster: movie.Poster, Rating: movie.Rating, IsLocal: true})
	}
	if len(matches) < 5 {
		if suggester, ok := handler.suggester.(externalSuggester); ok {
			if suggestions, suggestErr := suggester.SuggestExternal(c.Request.Context(), keyword); suggestErr == nil {
				for _, suggestion := range suggestions {
					if len(matches) == 5 {
						break
					}
					if !validDoubanID(suggestion.ID) || suggestion.Title == "" || seen[suggestion.ID] {
						continue
					}
					seen[suggestion.ID] = true
					matches = append(matches, DoubanMatch{DoubanID: suggestion.ID, Title: suggestion.Title,
						OriginalTitle: suggestion.SubTitle, Year: suggestion.Year, Poster: suggestion.Img})
					if queued, queueErr := handler.enqueueRefresh(c.Request.Context(), suggestion.ID, RefreshProviderDouban, "search_discovery"); queued && queueErr != nil {
						requestmeta.Logger(c.Request.Context()).Warn("queue discovered metadata", "douban_id", suggestion.ID, "error", queueErr)
					}
				}
			}
		}
	}
	if len(matches) == 0 {
		c.String(http.StatusOK, "")
		return
	}
	sortDoubanMatches(keyword, matches)
	data := gin.H{"Matches": matches, "Multiple": len(matches) > 1 || len(movies) == 0}
	if len(matches) == 1 && len(movies) == 1 {
		movie := movies[0]
		data["Movie"] = &movie
		data["DirectorNames"] = namesFromPeopleJSON(movie.Directors, 3)
		data["ActorNames"] = namesFromPeopleJSON(movie.Actors, 4)
		data["SummaryShort"] = truncateDescription(movie.Summary, 120)
		data["Genres"] = splitCommaValues(movie.Genres)
		data["Countries"] = splitCommaValues(movie.Countries)
		if handler.resources != nil && movie.DoubanID != "" {
			if linked, listErr := handler.resources.ListResourcesByDoubanID(c.Request.Context(), movie.DoubanID); listErr == nil && len(linked) > 0 {
				data["Resources"] = linked
			}
		}
	}
	c.HTML(http.StatusOK, "partials/douban_card.html", data)
}

func sortDoubanMatches(keyword string, matches []DoubanMatch) {
	keyword = strings.TrimSpace(keyword)
	rank := func(title string) int {
		switch {
		case strings.EqualFold(strings.TrimSpace(title), keyword):
			return 0
		case strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), strings.ToLower(keyword)):
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		leftRank, rightRank := rank(matches[i].Title), rank(matches[j].Title)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		if matches[i].Year != matches[j].Year {
			if matches[i].Year == "" {
				return false
			}
			if matches[j].Year == "" {
				return true
			}
			return matches[i].Year < matches[j].Year
		}
		return matches[i].Rating > matches[j].Rating
	})
}

func namesFromPeopleJSON(value string, limit int) []string {
	var people []Director
	_ = json.Unmarshal([]byte(value), &people)
	names := make([]string, 0, limit)
	for _, person := range people {
		if len(names) >= limit {
			break
		}
		if person.Name != "" {
			names = append(names, person.Name)
		}
	}
	return names
}

func splitCommaValues(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

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
			if time.Since(movie.ReviewsUpdatedAt) > 72*time.Hour {
				if queued, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderReviews, "stale_reviews"); !queued {
					handler.queueReviewRefresh(doubanID)
				} else if queueErr != nil {
					requestmeta.Logger(c.Request.Context()).Warn("queue stale Douban reviews", "douban_id", doubanID, "error", queueErr)
				}
			}
			c.HTML(http.StatusOK, "partials/reviews.html", gin.H{"Reviews": reviews})
			return
		}
	}
	if queued, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderReviews, "missing_reviews"); queued {
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
		if queued, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderTMDB, "missing_backdrops"); queued {
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
		if _, queueErr := handler.enqueueRefresh(c.Request.Context(), doubanID, RefreshProviderDouban, "partial_metadata"); queueErr != nil {
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
		handler.similarMu.Lock()
		if handler.similarCache == nil {
			handler.similarCache = make(map[string]similarCacheEntry)
		}
		if len(handler.similarCache) >= similarCacheCapacity {
			now := time.Now()
			for cachedKey, entry := range handler.similarCache {
				if now.After(entry.expiresAt) {
					delete(handler.similarCache, cachedKey)
				}
			}
			if len(handler.similarCache) >= similarCacheCapacity {
				for cachedKey := range handler.similarCache {
					delete(handler.similarCache, cachedKey)
					break
				}
			}
		}
		handler.similarCache[key] = similarCacheEntry{movies: copyMovies, expiresAt: time.Now().Add(similarCacheTTL)}
		handler.similarMu.Unlock()
		return copyMovies, nil
	})
	if err != nil || value == nil {
		return []Movie{}
	}
	return append([]Movie(nil), value.([]Movie)...)
}

func (handler *Handler) cachedSimilar(key string) ([]Movie, bool) {
	handler.similarMu.Lock()
	defer handler.similarMu.Unlock()
	entry, ok := handler.similarCache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(handler.similarCache, key)
		}
		return nil, false
	}
	return append([]Movie(nil), entry.movies...), true
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

func (handler *Handler) queueEmbedding(ctx context.Context, doubanID string) {
	if queued, err := handler.enqueueRefresh(ctx, doubanID, RefreshProviderEmbedding, "missing_embedding"); queued {
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

func (handler *Handler) fetchMissing(ctx context.Context, doubanID string) (bool, error) {
	if queued, err := handler.enqueueRefresh(ctx, doubanID, RefreshProviderDouban, "missing_metadata"); queued {
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

func (handler *Handler) enqueueRefresh(ctx context.Context, doubanID, provider, reason string) (bool, error) {
	if handler.refreshQueue == nil {
		return false, nil
	}
	_, err := handler.refreshQueue.EnqueueRefresh(ctx, doubanID, provider, reason, 0)
	return true, err
}

func (handler *Handler) runBackground(task func(context.Context)) bool {
	if accepting, ok := handler.runner.(acceptingBackgroundRunner); ok {
		return accepting.TryRun(task)
	}
	handler.runner.Run(task)
	return true
}

func truncateDescription(summary string, limit int) string {
	runes := []rune(strings.TrimSpace(summary))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

func proxyImageURL(value string) string {
	if value == "" || strings.HasPrefix(value, "/api/proxy/image") {
		return value
	}
	return "/api/proxy/image/r76RqSIVvUryzx" + base64.RawURLEncoding.EncodeToString([]byte(value))
}
