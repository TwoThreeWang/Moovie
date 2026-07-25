package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sync"


	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/user/moovie/internal/config"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/service"
	"github.com/user/moovie/internal/utils"
)

// 全局 validator 实例
var validate = validator.New()

// 全局变量，记录正在抓取的电影ID，避免重复抓取
var crawlingMap sync.Map

// Handler HTTP 处理器
type Handler struct {
	Repos                 *repository.Repositories
	Config                *config.Config
	DoubanCrawler         *service.DoubanCrawler
	DoubanSyncService     *service.DoubanSyncService
	DoubanSyncScheduler   *service.DoubanSyncScheduler
	TMDBService           *service.TMDBService
	SearchService         *service.SearchService
	RecommendationService *service.RecommendationService
	MonthlyReportService  *service.MonthlyReportService
	SearchCache           *utils.SearchCache[service.SearchResult]
}

// NewHandler 创建处理器
func NewHandler(repos *repository.Repositories, cfg *config.Config) *Handler {
	// 创建爬虫服务
	doubanCrawler := service.NewDoubanCrawler(repos.Movie, cfg)

	// 创建豆瓣同步服务
	doubanSyncService := service.NewDoubanSyncService(repos, doubanCrawler)

	// 创建 TMDB 服务
	tmdbService := service.NewTMDBService(repos.Movie, cfg)

	// 创建资源网爬虫
	sourceCrawler := service.NewSourceCrawler(10 * time.Second)

	// 创建搜索服务
	searchService := service.NewSearchService(repos.Site, repos.VodItem, repos.CopyrightFilter, repos.CategoryFilter, sourceCrawler)

	// 创建推荐服务
	recommendationService := service.NewRecommendationService(repos.Movie)

	// 创建月度报告服务（供管理员手动生成/重新生成月报使用）
	monthlyReportService := service.NewMonthlyReportService(repos)

	// 创建搜索缓存（容量1000条，TTL 3小时）
	searchCache := utils.NewSearchCache[service.SearchResult](500, 3*time.Hour)

	return &Handler{
		Repos:                 repos,
		Config:                cfg,
		DoubanCrawler:         doubanCrawler,
		DoubanSyncService:     doubanSyncService,
		TMDBService:           tmdbService,
		SearchService:         searchService,
		RecommendationService: recommendationService,
		MonthlyReportService:  monthlyReportService,
		SearchCache:           searchCache,
	}
}

// generateSearchCacheKey 生成搜索缓存key
func (h *Handler) generateSearchCacheKey(keyword string, bypass bool) string {
	bypassStr := "0"
	if bypass {
		bypassStr = "1"
	}
	// 统一小写，避免大小写敏感重复缓存
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	return fmt.Sprintf("search:%s:%s", keyword, bypassStr)
}

// RenderData 统一封装公共渲染数据
func (h *Handler) RenderData(c *gin.Context, data gin.H) gin.H {
	// 基础数据
	res := gin.H{
		"SiteName": h.Config.SiteName,
		"SiteUrl":  h.Config.SiteUrl,
		"Path":     c.Request.URL.Path,
		"FullPath": c.Request.RequestURI,
		"Referer":  c.Request.Referer(),
	}

	// 注入用户信息
	session := sessions.Default(c)
	if userinfo := session.Get("userinfo"); userinfo != nil {
		if su, ok := userinfo.(model.SessionUser); ok {
			res["UserInfo"] = su
		}
	}

	// 菜单高亮逻辑
	res["ActiveMenu"] = h.getActiveMenu(c)

	// 合并传入的数据
	for k, v := range data {
		res[k] = v
	}

	return res
}

// getActiveMenu 根据路径判断当前高亮菜单
func (h *Handler) getActiveMenu(c *gin.Context) string {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/dashboard") || path == "/history" || path == "/settings" {
		return "user"
	}

	if strings.HasPrefix(path, "/admin") {
		return "admin"
	}

	if path == "/search" {
		t := c.Query("type")
		if t != "" {
			return t
		}
		return "search"
	}

	switch path {
	case "/":
		return "home"
	case "/discover":
		return "discover"
	case "/trends":
		return "trends"
	case "/foryou":
		return "foryou"
	case "/square":
		return "square"
	case "/player":
		return "player"
	case "/iptv":
		return "iptv"
	case "/feedback":
		return "feedback"
	case "/about":
		return "about"
	default:
		return ""
	}
}

// ==================== 公开页面 ====================

// Home 首页
func (h *Handler) Home(c *gin.Context) {
	c.HTML(http.StatusOK, "home.html", h.RenderData(c, gin.H{
		"Title": h.Config.SiteName + " - 发现你的下一部电影",
	}))
}

// Search 搜索结果页
func (h *Handler) Search(c *gin.Context) {
	keyword := c.Query("kw")
	if keyword == "" {
		c.Redirect(http.StatusFound, "/")
		return
	}
	// 如果传了豆瓣ID，直接跳转到详情页（详情页会处理抓取逻辑）
	doubanID := c.Query("doubanId")
	if doubanID != "" {
		target := "/movie/" + doubanID
		if keyword != "" {
			target += "?title=" + url.QueryEscape(keyword)
		}
		c.Redirect(http.StatusFound, target)
		return
	}
	// 隐藏参数：跳过版权过滤
	bypass := c.Query("bypass") == "1"
	c.HTML(http.StatusOK, "search.html", h.RenderData(c, gin.H{
		"Title":       keyword + "在线观看 - " + keyword + "免费高清资源搜索 - " + h.Config.SiteName,
		"Keyword":     keyword,
		"Description": "Moovie影牛 为您找到关于“" + keyword + "”的相关资源。包含最新电影、电视剧在线观看线路，支持4K/高清多源码切换。",
		"Canonical":   fmt.Sprintf("%s/search?kw=%s", h.Config.SiteUrl, keyword),
		"Bypass":      bypass,
	}))
}

// Movie 电影详情页
func (h *Handler) Movie(c *gin.Context) {
	doubanID := c.Param("id")
	title := c.Query("title")

	movie, err := h.Repos.Movie.FindByDoubanID(doubanID)

	// 数据完整性校验：如果标题为空，视为脏数据，需要重新抓取
	if movie != nil && movie.Title == "" {
		log.Printf("[Handler] 发现脏数据 (标题为空)，准备删除并重新抓取 ID: %s", doubanID)
		h.Repos.Movie.DeleteByDoubanID(doubanID)
		movie = nil // 强制触发后续抓取逻辑
	}

	if err != nil || movie == nil {
		// 如果数据库中没有电影数据，显示正在抓取页面

		// 检查是否已有抓取任务在进行，避免重复抓取
		if _, isCrawling := crawlingMap.Load(doubanID); !isCrawling {
			// 标记为正在抓取
			crawlingMap.Store(doubanID, time.Now())

			// 启动后台异步抓取任务
			utils.GoSafe(60*time.Second, func(ctx context.Context) {
				defer crawlingMap.Delete(doubanID) // 抓取完成后删除标记

				log.Printf("[Handler] 后台异步抓取电影信息 ID: %s", doubanID)
				if h.DoubanCrawler != nil {
					if err := h.DoubanCrawler.CrawlMovieSafe(ctx, doubanID); err != nil {
						log.Printf("[Handler] 豆瓣抓取失败 (已尝试 API 和网页回退): %v", err)
					}
				}
			})
		}

		// 返回正在抓取中的页面，提供刷新按钮和搜索链接
		c.HTML(http.StatusOK, "fetching.html", h.RenderData(c, gin.H{
			"Title":    title,
			"DoubanID": doubanID,
			"SiteName": h.Config.SiteName,
		}))
		return
	}

	// 检查状态
	userID := middleware.GetUserID(c)
	isWish := false
	isWatched := false
	if userID > 0 {
		if rec, err := h.Repos.UserMovie.GetByUserAndMovie(userID, movie.DoubanID); err == nil && rec != nil {
			isWish = rec.Status == "wish"
			isWatched = rec.Status == "watched"
		}
	}

	// 全站有多少人看过/想看这部电影，作为详情页的社交信号，让访客感觉到"这里还有别的真实用户"
	watchedByCount, _ := h.Repos.UserMovie.CountByMovie(movie.DoubanID, "watched")
	wishByCount, _ := h.Repos.UserMovie.CountByMovie(movie.DoubanID, "wish")

	// 构建 SEO 关键词
	var keywords []string
	keywords = append(keywords, movie.Title)
	if movie.Year != "" {
		keywords = append(keywords, movie.Year)
	}
	if movie.Genres != "" {
		// 分割流派
		parts := strings.Split(movie.Genres, ",")
		keywords = append(keywords, parts...)
	}
	keywords = append(keywords, "在线观看", "免费下载", "高清资源", "Moovie", "影牛")

	// 构建描述 (去除空白字符)
	desc := strings.TrimSpace(movie.Summary)
	if len([]rune(desc)) > 150 {
		desc = string([]rune(desc)[:150]) + "..."
	}

	// 将 "导演" 转为 []string{"导演A", "导演B"}
	// 定义导演结构体
	type Director struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	// 1. 解析 JSON 字符串到切片中
	var directorList []Director
	err = json.Unmarshal([]byte(movie.Directors), &directorList)
	if err != nil {
		// 如果解析失败，处理错误或设为空
		directorList = []Director{}
	}
	// 相似电影推荐 - 添加缓存机制
	cacheKey := fmt.Sprintf("similar_movies_%s", doubanID)
	var movies []model.Movie

	// 尝试从缓存获取
	if val, found := utils.CacheGet(cacheKey); found {
		if cached, ok := val.([]model.Movie); ok {
			movies = cached
		}
	}

	// 如果缓存中没有，查询数据库
	if movies == nil {
		movies, err = h.Repos.Movie.FindSimilar(doubanID, 6)
		if err != nil {
			log.Printf("获取相似电影失败: %v", err)
		} else if len(movies) > 0 {
			// 存入缓存，设置1小时过期时间
			utils.CacheSet(cacheKey, movies, 1*time.Hour)
		}
	}
	// 如果EmbeddingContent为空，异步生成并更新数据库
	if movie.EmbeddingContent == "" {
		utils.GoSafe(30*time.Second, func(ctx context.Context) {
			log.Printf("[Handler] 正在异步生成EmbeddingContent ID: %s", doubanID)
			if err := h.DoubanCrawler.EnrichMovieWithVector(movie); err == nil {
				_ = h.Repos.Movie.Upsert(movie)
			}
		})
	}

	c.HTML(http.StatusOK, "movie.html", h.RenderData(c, gin.H{
		"Title":         "《" + movie.Title + "》 (" + movie.Year + ") - 剧情介绍/演职员表 - " + h.Config.SiteName,
		"Description":   desc,
		"Keywords":      strings.Join(keywords, ","),
		"Cover":         utils.EncodeProxyImageURL(movie.Poster),
		"Canonical":     fmt.Sprintf("%s/movie/%s", h.Config.SiteUrl, movie.DoubanID),
		"Movie":          movie,
		"IsWish":         isWish,
		"IsWatched":      isWatched,
		"WatchedByCount": watchedByCount,
		"WishByCount":    wishByCount,
		"DirectorList":   directorList,
		"SearchTitle":    title,
		"SimilarMovies":  movies,
	}))
}

// Play 播放页
func (h *Handler) Play(c *gin.Context) {
	// 核心参数从路径获取
	sourceKey := c.Param("source_key")
	vodId := c.Param("vod_id")
	// 可选参数从查询字符串获取
	doubanID := c.Query("douban_id") // 可选，用于展示增强
	episode := c.Query("ep")

	var detail *model.VodItem
	var err error

	if sourceKey != "" && vodId != "" {
		detail, err = h.SearchService.GetDetail(c.Request.Context(), sourceKey, vodId)
	}

	if err != nil || detail == nil {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "视频未找到 - " + h.Config.SiteName,
		}))
		return
	}

	// 解析播放列表
	sources := utils.ParsePlayUrl(detail.VodPlayUrl)
	var currentSource *utils.PlaySource
	if len(sources) > 0 {
		currentSource = &sources[0]
		// 如果指定了 source，则切换
		reqSource := c.Query("source")
		if reqSource != "" {
			for _, s := range sources {
				if s.Name == reqSource {
					currentSource = &s
					break
				}
			}
		}
	}

	// 确定当前播放的集数和 URL
	playURL := ""
	if currentSource != nil {
		// 如果没传 ep，默认播放第一集
		if episode == "" && len(currentSource.Episodes) > 0 {
			episode = currentSource.Episodes[0].Title
			playURL = currentSource.Episodes[0].URL
		} else {
			for _, ep := range currentSource.Episodes {
				if ep.Title == episode {
					playURL = ep.URL
					break
				}
			}
		}
	}
	if doubanID == "" {
		doubanID = detail.VodDoubanId
	}

	// 如果有豆瓣ID，异步安全抓取豆瓣电影信息
	// 使用 singleflight 机制防止同一电影被并发重复抓取
	if doubanID != "" && doubanID != "0" && h.DoubanCrawler != nil {
		h.DoubanCrawler.CrawlMovieSafeAsync(doubanID)
	}

	// 版权限制拦截
	if blocked, _ := h.SearchService.IsCopyrightRestricted(detail.VodName); blocked {
		c.Redirect(http.StatusFound, "/copyright-restricted?title="+url.QueryEscape(detail.VodName))
		return
	}

	// 动态生成标题
	pageTitle := "《" + detail.VodName + "》"
	if episode != "" {
		pageTitle += "(" + episode + ")"
	}
	pageTitle += " - 在线播放免费高清线路 - " + h.Config.SiteName

	// 获取视频加载统计信息
	loadStats, _ := h.Repos.Movie.GetLoadStatsBySource(sourceKey, vodId)

	// 准备渲染数据
	renderData := gin.H{
		"Title":         pageTitle,
		"DoubanID":      doubanID,
		"VodID":         vodId,
		"SourceKey":     sourceKey,
		"Detail":        detail,
		"Sources":       sources,
		"CurrentSource": currentSource,
		"Episode":       episode,
		"PlayURL":       playURL,
		"ContentClass":  "full-width",
		"Description":   fmt.Sprintf("在线观看 %s - %s", detail.VodName, h.Config.SiteName),
		"Keywords":      fmt.Sprintf("%s,在线播放,高清视频,%s", detail.VodName, h.Config.SiteName),
		"Cover":         detail.VodPic,
		"LoadStats":     loadStats,
	}

	if currentSource != nil {
		renderData["Episodes"] = currentSource.Episodes
		renderData["Source"] = currentSource.Name
	}

	c.HTML(http.StatusOK, "play.html", h.RenderData(c, renderData))
}

// CopyrightRestricted 版权限制提示页
func (h *Handler) CopyrightRestricted(c *gin.Context) {
	c.HTML(http.StatusOK, "copyright_restricted.html", h.RenderData(c, gin.H{
		"Title":      "版权限制 - " + h.Config.SiteName,
		"MovieTitle": c.Query("title"),
	}))
}

// IPTV IPTV电视直播页面
func (h *Handler) IPTV(c *gin.Context) {
	c.HTML(http.StatusOK, "iptv.html", h.RenderData(c, gin.H{
		"Title":        fmt.Sprintf("IPTV电视直播 - 全国卫视央视在线观看 - %s", h.Config.SiteName),
		"Description":  fmt.Sprintf("%s 提供的免费 IPTV 电视直播播放器。支持导入 M3U 直播源，观看央视、卫视等频道。", h.Config.SiteName),
		"Keywords":     fmt.Sprintf("IPTV,电视直播,央视,卫视,在线观看,%s", h.Config.SiteName),
		"ContentClass": "full-width",
	}))
}

// Player 播放器页面
func (h *Handler) Player(c *gin.Context) {
	url := c.Query("url")
	isEmbed := c.Query("embed") == "1"

	if isEmbed {
		c.HTML(http.StatusOK, "player_embed.html", gin.H{
			"URL": url,
		})
		return
	}

	c.HTML(http.StatusOK, "player.html", h.RenderData(c, gin.H{
		"Title":        fmt.Sprintf("M3U8在线播放器 - HLS直播流测试工具 - 极简无广告 - %s", h.Config.SiteName),
		"Description":  fmt.Sprintf("%s 提供的免费 M3U8 在线播放工具。支持 HLS (.m3u8) 视频流测试，跨平台兼容，无需插件，高清流畅。适用于开发者测试和日常观影。", h.Config.SiteName),
		"Keywords":     fmt.Sprintf("M3U8,在线播放,直播流测试,无广告,%s", h.Config.SiteName),
		"URL":          url,
		"ContentClass": "full-width",
		"Canonical":    fmt.Sprintf("%s/player", h.Config.SiteUrl),
	}))
}

// Discover 发现/分类页
func (h *Handler) Discover(c *gin.Context) {
	movieType := c.Param("type")
	if movieType == "" {
		movieType = "movie"
	}

	// 检查是否是 HTMX 请求
	isHTMX := c.GetHeader("HX-Request") != ""

	if isHTMX {
		subjects, err := h.DoubanCrawler.GetPopularSubjects(movieType)
		if err != nil {
			log.Printf("获取热门电影失败: %v", err)
		}
		c.HTML(http.StatusOK, "partials/discover_grid.html", gin.H{
			"Subjects":    subjects,
			"CurrentType": movieType,
		})
		return
	}
	Title := "2026豆瓣高分电影推荐 - 热门在线电影发现"
	Description := "发现最新上映及豆瓣高分电影，涵盖动作、科幻、悬疑等多种题材，实时同步全网热度。"
	Keywords := "热门电影,最新电视剧,高分佳作,Moovie影牛发现"
	switch movieType {
	case "tv":
		Title = "2026近期热门电视剧排行榜 - 好剧推荐在线看"
		Description = "为您整理近期最火的电视剧、国产剧、美剧及韩剧，支持全网资源搜索与在线播放。"
		Keywords = "热门电视剧,最新电视剧,高分佳作,Moovie影牛发现"
	case "show":
		Title = "2026豆瓣高分综艺推荐 - 热门在线综艺发现"
		Description = "发现最新、最热的综艺，满足你的综艺需求。"
		Keywords = "热门综艺,最新综艺,高分佳作,Moovie影牛发现"
	case "cartoon":
		Title = "2026热门动漫新番推荐 - 豆瓣高分动画榜单"
		Description = "发现本季最强新番及经典高分动漫，支持多线路高清搜索。"
		Keywords = "热门动漫,最新动漫,高分佳作,Moovie影牛发现"
	}

	c.HTML(http.StatusOK, "discover.html", h.RenderData(c, gin.H{
		"Title":       Title + " - " + h.Config.SiteName,
		"Description": Description,
		"Keywords":    Keywords,
		"Canonical":   fmt.Sprintf("%s/discover/%s", h.Config.SiteUrl, movieType),
		"CurrentType": movieType,
	}))
}

// Trends 热搜趋势
func (h *Handler) Trends(c *gin.Context) {
	// 辅助结构
	type TrendItem struct {
		Keyword  string
		Count    int
		Tag      string
		TagClass string
	}

	var items24h []TrendItem
	var itemsAll []TrendItem

	// 1. 尝试从缓存获取 24h 数据
	cacheKey24h := "trending_24h_items"
	if val, found := utils.CacheGet(cacheKey24h); found {
		if cached, ok := val.([]TrendItem); ok {
			items24h = cached
		}
	}

	// 2. 尝试从缓存获取全量数据
	cacheKeyAll := "trending_all_items"
	if val, found := utils.CacheGet(cacheKeyAll); found {
		if cached, ok := val.([]TrendItem); ok {
			itemsAll = cached
		}
	}

	// 3. 如果 24h 缓存失效，查询并处理
	if items24h == nil {
		trending24h, err := h.Repos.SearchLog.GetTrending(24, 20)
		if err != nil {
			log.Printf("获取 24h 热搜失败: %v", err)
		}

		for _, t := range trending24h {
			item := TrendItem{
				Keyword: t.Keyword,
				Count:   t.Count,
			}
			if t.Count > 100 { // 24小时内100次就算热
				item.Tag = "热"
				item.TagClass = "hot"
			} else if t.LastSearchedAt.After(time.Now().Add(-1 * time.Hour)) {
				item.Tag = "新"
				item.TagClass = "new"
			}
			items24h = append(items24h, item)
		}
		// 存入缓存 10 分钟
		utils.CacheSet(cacheKey24h, items24h, 10*time.Minute)
	}

	// 4. 如果全量缓存失效，查询并处理
	if itemsAll == nil {
		trendingAll, err := h.Repos.SearchLog.GetTrending(0, 50)
		if err != nil {
			log.Printf("获取全量热搜失败: %v", err)
		}

		for _, t := range trendingAll {
			item := TrendItem{
				Keyword: t.Keyword,
				Count:   t.Count,
			}
			if t.Count > 4000 {
				item.Tag = "爆"
				item.TagClass = "bao"
			} else if t.Count > 2000 {
				item.Tag = "热"
				item.TagClass = "hot"
			}
			itemsAll = append(itemsAll, item)
		}
		// 存入缓存 10 分钟
		utils.CacheSet(cacheKeyAll, itemsAll, 10*time.Minute)
	}

	c.HTML(http.StatusOK, "trends.html", h.RenderData(c, gin.H{
		"Title":       "今日影视热搜榜 - 热门电影电视剧排行榜 - 实时更新 - " + h.Config.SiteName,
		"Description": "想知道大家都在看什么？Moovie影牛实时汇总全网搜索热度，为您呈现今日最火电影、电视剧及综艺排行榜。发现好片，一键在线观看。",
		"Keywords":    "电影排行榜, 热搜榜, 热门电影, 电视剧排名, 在线电影搜索, 实时影视热度, 搜索趋势,热门搜索,关键词排行,影视风向",
		"Trending24h": items24h,
		"TrendingAll": itemsAll,
		"Canonical":   fmt.Sprintf("%s/trends", h.Config.SiteUrl),
		"SiteUrl":     h.Config.SiteUrl,
		"UpdateTime":  time.Now().Format("15:04"),
	}))
}

// ForYou 为你推荐页面
func (h *Handler) ForYou(c *gin.Context) {
	c.HTML(http.StatusOK, "foryou.html", h.RenderData(c, gin.H{
		"Title": "为你推荐 - " + h.Config.SiteName,
	}))
}

// Square 广场页外壳：动态流 / 排行榜的具体内容都通过 htmx 懒加载，
// 页面本身只负责渲染 tab 结构，和 dashboard/foryou 的加载方式保持一致
func (h *Handler) Square(c *gin.Context) {
	c.HTML(http.StatusOK, "square.html", h.RenderData(c, gin.H{
		"Title": "广场 - " + h.Config.SiteName,
	}))
}

// FeedbackPage 反馈页面
func (h *Handler) FeedbackPage(c *gin.Context) {
	c.HTML(http.StatusOK, "feedback.html", h.RenderData(c, gin.H{
		"Title": "反馈建议 - " + h.Config.SiteName,
	}))
}

// TVBoxGuide TVBox 配置指南页面
func (h *Handler) TVBoxGuide(c *gin.Context) {
	c.HTML(http.StatusOK, "tvbox.html", h.RenderData(c, gin.H{
		"Title":       "TVBox 配置指南 - " + h.Config.SiteName,
		"TVBoxAPIURL": h.Config.SiteUrl + "/api/tvbox.json",
	}))
}

// About 关于页面
func (h *Handler) About(c *gin.Context) {
	c.HTML(http.StatusOK, "about.html", h.RenderData(c, gin.H{
		"Title": "关于 - " + h.Config.SiteName,
	}))
}

// Advertise 广告合作
func (h *Handler) Advertise(c *gin.Context) {
	c.HTML(http.StatusOK, "advertise.html", h.RenderData(c, gin.H{
		"Title":      "广告合作 - " + h.Config.SiteName,
		"ActiveMenu": "advertise",
	}))
}

// Changelog 更新记录页面
func (h *Handler) Changelog(c *gin.Context) {
	c.HTML(http.StatusOK, "changelog.html", h.RenderData(c, gin.H{
		"Title": "更新记录 - " + h.Config.SiteName,
	}))
}

// DMCA DMCA 声明
func (h *Handler) DMCA(c *gin.Context) {
	c.HTML(http.StatusOK, "dmca.html", h.RenderData(c, gin.H{
		"Title": "DMCA 声明 - " + h.Config.SiteName,
	}))
}

// Privacy 隐私政策
func (h *Handler) Privacy(c *gin.Context) {
	c.HTML(http.StatusOK, "privacy.html", h.RenderData(c, gin.H{
		"Title": "隐私政策 - " + h.Config.SiteName,
	}))
}

// Terms 服务协议
func (h *Handler) Terms(c *gin.Context) {
	c.HTML(http.StatusOK, "terms.html", h.RenderData(c, gin.H{
		"Title": "服务协议 - " + h.Config.SiteName,
	}))
}

// Sitemap 站点地图
func (h *Handler) Sitemap(c *gin.Context) {
	baseUrl := h.Config.SiteUrl
	if strings.HasSuffix(baseUrl, "/") {
		baseUrl = strings.TrimSuffix(baseUrl, "/")
	}

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	sb.WriteString("\n")

	// 1. 静态页面
	staticPages := []struct {
		path     string
		priority string
		freq     string
	}{
		{"/", "1.0", "daily"},
		{"/discover/movie", "0.8", "daily"},
		{"/discover/tv", "0.8", "daily"},
		{"/discover/show", "0.8", "daily"},
		{"/discover/cartoon", "0.8", "daily"},
		{"/trends", "0.8", "daily"},
		{"/feedback", "0.5", "monthly"},
		{"/changelog", "0.5", "weekly"},
		{"/about", "0.5", "monthly"},
		{"/dmca", "0.5", "monthly"},
		{"/privacy", "0.5", "monthly"},
		{"/terms", "0.5", "monthly"},
	}

	for _, p := range staticPages {
		sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s%s</loc>\n    <changefreq>%s</changefreq>\n    <priority>%s</priority>\n  </url>\n", baseUrl, p.path, p.freq, p.priority))
	}

	// 2. 电影详情页和相似电影页 (取最近更新的 1000 条)
	movies, err := h.Repos.Movie.GetSitemapMovies(1000)
	if err == nil {
		for _, m := range movies {
			lastmod := m.UpdatedAt.Format("2006-01-02")
			// 电影详情页
			sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s/movie/%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>weekly</changefreq>\n    <priority>0.7</priority>\n  </url>\n", baseUrl, m.DoubanID, lastmod))
			// 相似电影页 (新增加)
			sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s/similar/%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>weekly</changefreq>\n    <priority>0.6</priority>\n  </url>\n", baseUrl, m.DoubanID, lastmod))
		}
	}

	sb.WriteString(`</urlset>`)

	c.Header("Content-Type", "application/xml")
	c.String(http.StatusOK, sb.String())
}

// Robots robots.txt
func (h *Handler) Robots(c *gin.Context) {
	baseUrl := h.Config.SiteUrl
	if strings.HasSuffix(baseUrl, "/") {
		baseUrl = strings.TrimSuffix(baseUrl, "/")
	}

	var sb strings.Builder
	sb.WriteString("User-agent: *\n")
	sb.WriteString("Disallow: /admin/\n")
	sb.WriteString("Disallow: /auth/\n")
	sb.WriteString("Disallow: /dashboard/\n")
	sb.WriteString("Disallow: /api/proxy/image/\n")
	sb.WriteString("Disallow: /api/\n")
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Sitemap: %s/sitemap.xml\n", baseUrl))

	c.Header("Content-Type", "text/plain")
	c.String(http.StatusOK, sb.String())
}

// ==================== 认证页面 ====================

// LoginPage 登录页面
func (h *Handler) LoginPage(c *gin.Context) {
	// 如果已经登录，直接跳转到首页
	if middleware.GetUserID(c) > 0 {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.HTML(http.StatusOK, "login.html", h.RenderData(c, gin.H{
		"Title":    "登录 - " + h.Config.SiteName,
		"Redirect": c.Query("redirect"),
	}))
}

// Login 登录处理
func (h *Handler) Login(c *gin.Context) {
	email := c.PostForm("email")
	password := c.PostForm("password")
	redirect := c.PostForm("redirect")

	if redirect == "" || !strings.HasPrefix(redirect, "/") || strings.HasPrefix(redirect, "//") {
		redirect = "/"
	}

	// 查找用户
	user, err := h.Repos.User.FindByEmail(email)
	if err != nil || user == nil {
		c.HTML(http.StatusOK, "login.html", gin.H{
			"Title": "登录 - Moovie影牛",
			"Error": "邮箱或密码错误",
		})
		return
	}

	// 验证密码
	if !h.Repos.User.CheckPassword(user, password) {
		c.HTML(http.StatusOK, "login.html", gin.H{
			"Title": "登录 - Moovie影牛",
			"Error": "邮箱或密码错误",
		})
		return
	}

	// 生成 JWT
	token, err := middleware.GenerateToken(user.ID, user.Email, user.Role, h.Config.AppSecret, h.Config.JWTExpiry)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"Title": "登录 - Moovie影牛",
			"Error": "登录失败，请重试",
		})
		return
	}

	// 设置 Cookie (JWT)
	c.SetCookie("token", token, int(h.Config.JWTExpiry.Seconds()), "/", "", false, true)

	// 保存 UserInfo 到 Session
	session := sessions.Default(c)
	session.Set("userinfo", model.SessionUser{
		ID:       user.ID,
		Email:    user.Email,
		Username: user.Username,
		Role:     user.Role,
		Avatar:   user.Avatar,
	})
	session.Save()

	c.Redirect(http.StatusFound, redirect)
}

// RegisterPage 注册页面
func (h *Handler) RegisterPage(c *gin.Context) {
	// 如果已经登录，直接跳转到首页
	if middleware.GetUserID(c) > 0 {
		c.Redirect(http.StatusFound, "/")
		return
	}
	c.HTML(http.StatusOK, "register.html", h.RenderData(c, gin.H{
		"Title": "注册 - " + h.Config.SiteName,
	}))
}

// Register 注册处理
func (h *Handler) Register(c *gin.Context) {
	email := c.PostForm("email")
	password := c.PostForm("password")
	confirmPassword := c.PostForm("confirm_password")

	// 使用 validator 验证邮箱格式
	if err := validate.Var(email, "required,email"); err != nil {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie影牛",
			"Error": "请输入有效的邮箱地址",
		})
		return
	}

	// 验证密码一致性
	if password != confirmPassword {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie影牛",
			"Error": "两次输入的密码不一致",
		})
		return
	}

	if len(password) < 6 {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie影牛",
			"Error": "密码至少需要 6 个字符",
		})
		return
	}

	// 检查邮箱是否已存在
	existing, _ := h.Repos.User.FindByEmail(email)
	if existing != nil {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie影牛",
			"Error": "该邮箱已被注册",
		})
		return
	}

	// 创建用户
	// 默认截取邮箱 @ 符号前的内容作为用户名
	username := email
	if parts := strings.Split(email, "@"); len(parts) > 0 {
		username = parts[0]
	}

	user, err := h.Repos.User.Create(email, username, password)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "register.html", gin.H{
			"Title": "注册 - Moovie影牛",
			"Error": "注册失败，请重试",
		})
		return
	}

	// 生成 JWT 并登录
	token, _ := middleware.GenerateToken(user.ID, user.Email, user.Role, h.Config.AppSecret, h.Config.JWTExpiry)
	c.SetCookie("token", token, int(h.Config.JWTExpiry.Seconds()), "/", "", false, true)

	// 保存 UserInfo 到 Session
	session := sessions.Default(c)
	session.Set("userinfo", model.SessionUser{
		ID:       user.ID,
		Email:    user.Email,
		Username: user.Username,
		Role:     user.Role,
		Avatar:   user.Avatar,
	})
	session.Save()

	c.Redirect(http.StatusFound, "/")
}

// Logout 登出
func (h *Handler) Logout(c *gin.Context) {
	c.SetCookie("token", "", -1, "/", "", false, true)

	// 清理 Session
	session := sessions.Default(c)
	session.Clear()
	session.Save()

	c.Redirect(http.StatusFound, "/")
}

// ==================== 用户中心 ====================

// Dashboard 用户中心
func (h *Handler) Dashboard(c *gin.Context) {
	userID := middleware.GetUserID(c)

	// 获取完整用户信息
	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	// 获取统计数据
	favoriteCount, _ := h.Repos.UserMovie.CountByUser(userID, "wish")
	watchedCount, _ := h.Repos.UserMovie.CountByUser(userID, "watched")
	historyCount, _ := h.Repos.History.CountByUser(userID)
	feedbackCount, _ := h.Repos.Feedback.CountByUserID(userID)

	// 获取最新月度报告
	monthlyReport, _ := h.Repos.MonthlyReport.GetLatestByUser(userID)

	c.HTML(http.StatusOK, "dashboard.html", h.RenderData(c, gin.H{
		"Title":         "用户中心 - " + h.Config.SiteName,
		"User":          user,
		"FavoriteCount": favoriteCount,
		"WatchedCount":  watchedCount,
		"HistoryCount":  historyCount,
		"FeedbackCount": feedbackCount,
		"MonthlyReport": monthlyReport,
	}))
}

// Settings 账号设置
func (h *Handler) Settings(c *gin.Context) {
	userID := middleware.GetUserID(c)

	// 获取完整用户信息
	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	// 获取 success 参数用于显示成功提示
	success := c.Query("success")

	// 获取豆瓣同步状态
	syncJob, _ := h.Repos.DoubanSyncJob.GetLatestByUser(userID)

	c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
		"Title":       "账号设置 - " + h.Config.SiteName,
		"User":        user,
		"Success":     success,
		"DoubanJob":   syncJob,
	}))
}


// renderSettingsError 渲染设置页错误（携带用户信息）
func (h *Handler) renderSettingsError(c *gin.Context, errMsg string) {
	userID := middleware.GetUserID(c)
	user, _ := h.Repos.User.FindByID(userID)
	syncJob, _ := h.Repos.DoubanSyncJob.GetLatestByUser(userID)
	c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
		"Title":     "账号设置 - " + h.Config.SiteName,
		"User":      user,
		"Error":     errMsg,
		"DoubanJob": syncJob,
	}))
}
// UpdateUsername 修改用户名
func (h *Handler) UpdateUsername(c *gin.Context) {
	userID := middleware.GetUserID(c)
	newUsername := strings.TrimSpace(c.PostForm("username"))

	if newUsername == "" || len(newUsername) < 2 || len(newUsername) > 20 {
		h.renderSettingsError(c, "用户名应在 2-20 个字符之间")
		return
	}

	err := h.Repos.User.UpdateUsername(userID, newUsername)
	if err != nil {
		h.renderSettingsError(c, "用户名更新失败")
		return
	}

	// 更新 Session 中的用户名
	session := sessions.Default(c)
	if userinfo := session.Get("userinfo"); userinfo != nil {
		if su, ok := userinfo.(model.SessionUser); ok {
			su.Username = newUsername
			session.Set("userinfo", su)
			session.Save()
		}
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=username")
}

// UpdateEmail 修改邮箱
func (h *Handler) UpdateEmail(c *gin.Context) {
	userID := middleware.GetUserID(c)
	newEmail := strings.TrimSpace(c.PostForm("email"))

	// 简单邮箱格式验证
	if newEmail == "" || !strings.Contains(newEmail, "@") {
		h.renderSettingsError(c, "请输入有效的邮箱地址")
		return
	}

	// 检查邮箱是否已被使用
	existing, _ := h.Repos.User.FindByEmail(newEmail)
	if existing != nil && existing.ID != userID {
		h.renderSettingsError(c, "该邮箱已被其他账号使用")
		return
	}

	err := h.Repos.User.UpdateEmail(userID, newEmail)
	if err != nil {
		h.renderSettingsError(c, "邮箱更新失败")
		return
	}

	// 更新 Session 中的邮箱
	session := sessions.Default(c)
	if userinfo := session.Get("userinfo"); userinfo != nil {
		if su, ok := userinfo.(model.SessionUser); ok {
			su.Email = newEmail
			session.Set("userinfo", su)
			session.Save()
		}
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=email")
}

// UpdatePassword 修改密码
func (h *Handler) UpdatePassword(c *gin.Context) {
	userID := middleware.GetUserID(c)
	currentPassword := c.PostForm("current_password")
	newPassword := c.PostForm("new_password")
	confirmPassword := c.PostForm("confirm_password")

	// 获取当前用户
	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	// 验证当前密码
	if !h.Repos.User.CheckPassword(user, currentPassword) {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "当前密码错误",
		}))
		return
	}

	// 验证新密码
	if newPassword != confirmPassword {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "两次输入的新密码不一致",
		}))
		return
	}

	if len(newPassword) < 6 {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "新密码至少需要 6 个字符",
		}))
		return
	}

	// 更新密码
	err = h.Repos.User.UpdatePassword(userID, newPassword)
	if err != nil {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "密码更新失败",
		}))
		return
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=password")
}

// UpdateShareSetting 更新分享设置
func (h *Handler) UpdateShareSetting(c *gin.Context) {
	userID := middleware.GetUserID(c)
	isPublic := c.PostForm("is_public") == "on"

	if err := h.Repos.User.UpdateIsPublic(userID, isPublic); err != nil {
		h.renderSettingsError(c, "分享设置更新失败")
		return
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=share")
}

// UpdateAvatar 更新用户头像 emoji
func (h *Handler) UpdateAvatar(c *gin.Context) {
	userID := middleware.GetUserID(c)
	avatar := strings.TrimSpace(c.PostForm("avatar"))

	if avatar == "" {
		h.renderSettingsError(c, "请选择或输入一个 emoji 作为头像")
		return
	}

	// 验证长度（单个 emoji 最多 8 字节）
	runes := []rune(avatar)
	if len(runes) > 4 {
		h.renderSettingsError(c, "头像最多支持 4 个 emoji 字符")
		return
	}

	if err := h.Repos.User.UpdateAvatar(userID, avatar); err != nil {
		h.renderSettingsError(c, "头像更新失败")
		return
	}

	// 更新 Session 中的头像
	session := sessions.Default(c)
	if userinfo := session.Get("userinfo"); userinfo != nil {
		if su, ok := userinfo.(model.SessionUser); ok {
			su.Avatar = avatar
			session.Set("userinfo", su)
			session.Save()
		}
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=avatar")
}

// PublicProfile 公开观影记录页
func (h *Handler) PublicProfile(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.Atoi(userIDStr)
	if err != nil || userID <= 0 {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "页面未找到 - " + h.Config.SiteName,
		}))
		return
	}

	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "页面未找到 - " + h.Config.SiteName,
		}))
		return
	}

	// 未开启分享时，对任何访问者都显示 404
	if !user.IsPublic {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "页面未找到 - " + h.Config.SiteName,
		}))
		return
	}

	// 公开主页只取第一页（与仪表盘想看/已看的分页逻辑保持一致），避免一次性拉回全部记录；
	// 后续页通过 PublicWishHTMX / PublicWatchedHTMX 的"加载更多"按钮增量获取
	wishList, _ := h.Repos.UserMovie.ListByUser(userID, "wish", dashboardGridPageSize, 0)
	watchedList, _ := h.Repos.UserMovie.ListByUser(userID, "watched", dashboardGridPageSize, 0)
	wishCount, _ := h.Repos.UserMovie.CountByUser(userID, "wish")
	watchedCount, _ := h.Repos.UserMovie.CountByUser(userID, "watched")
	monthlyReports, _ := h.Repos.MonthlyReport.ListByUser(userID, 6, 0)

	// 平均评分改用 SQL 聚合直接算，不再依赖把全部已看记录都拉进内存来累加
	avgRating, ratedCount, err := h.Repos.UserMovie.AvgRatingByUser(userID)
	if err != nil {
		log.Printf("[PublicProfile] 计算平均评分失败 user=%d err=%v", userID, err)
	}

	wishHasMore := wishCount > len(wishList)
	watchedHasMore := watchedCount > len(watchedList)

	c.HTML(http.StatusOK, "share.html", h.RenderData(c, gin.H{
		"Title":          user.Username + " 的观影记录 - " + h.Config.SiteName,
		"User":           user,
		"WishList":       wishList,
		"WatchedList":    watchedList,
		"WishCount":      wishCount,
		"WatchedCount":   watchedCount,
		"WishHasMore":    wishHasMore,
		"WishNextPage":   2,
		"WatchedHasMore": watchedHasMore,
		"WatchedNextPage": 2,
		"AvgRating":      avgRating,
		"RatedCount":     ratedCount,
		"MonthlyReports": monthlyReports,
		"Canonical":      fmt.Sprintf("%s/user/%d", h.Config.SiteUrl, user.ID),
	}))
}

// PublicMonthly 公开月度小记页
func (h *Handler) PublicMonthly(c *gin.Context) {
	userIDStr := c.Param("user_id")
	yearMonth := c.Param("year_month")

	userID, err := strconv.Atoi(userIDStr)
	if err != nil || userID <= 0 {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "页面未找到 - " + h.Config.SiteName,
		}))
		return
	}

	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil || !user.IsPublic {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "页面未找到 - " + h.Config.SiteName,
		}))
		return
	}

	report, err := h.Repos.MonthlyReport.GetByUserAndMonth(userID, yearMonth)
	if err != nil {
		log.Printf("[PublicMonthly] 查询月报失败 user=%d month=%s err=%v", userID, yearMonth, err)
	}
	if report == nil {
		log.Printf("[PublicMonthly] 月报不存在 user=%d month=%s", userID, yearMonth)
	}
	if err != nil || report == nil {
		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "未找到报告 - " + h.Config.SiteName,
		}))
		return
	}

	// 解析类型统计
	var genreStats []service.GenreStat
	if report.GenreStats != "" {
		_ = json.Unmarshal([]byte(report.GenreStats), &genreStats)
	}

	// 解析分享卡片的海报墙（生成时已抽样冻结好的固定几张，不是本月全部）
	var posterWall []model.PosterWallItem
	if report.PosterWall != "" {
		_ = json.Unmarshal([]byte(report.PosterWall), &posterWall)
	}
	// 卡片上未展示的部分，用于海报墙末尾的"+N"角标
	posterWallExtra := report.WatchedCount - len(posterWall)
	if posterWallExtra < 0 {
		posterWallExtra = 0
	}

	// 查询本月完整已看片单（用于卡片下方"完整片单"区块，不影响卡片本身的固定海报墙）
	startTime, _ := time.Parse("2006-01", yearMonth)
	endTime := startTime.AddDate(0, 1, 0)
	monthlyMovies, err := h.Repos.UserMovie.ListByUserAndDateRange(userID, "watched", startTime, endTime)
	if err != nil {
		log.Printf("[PublicMonthly] 查询月度已看列表失败 user=%d month=%s err=%v", userID, yearMonth, err)
	}

	shareURL := fmt.Sprintf("%s/user/%d/monthly/%s", h.Config.SiteUrl, user.ID, yearMonth)
	// 二维码指向用户的公开主页（而不是这一页本身），扫码的人能看到完整的观影记录，
	// 而不只是这一个月的静态截图
	profileURL := fmt.Sprintf("%s/user/%d", h.Config.SiteUrl, user.ID)
	// 展示用的站点域名，去掉协议前缀，如 "moovie.c2v2.com"
	siteDomain := strings.TrimPrefix(strings.TrimPrefix(h.Config.SiteUrl, "https://"), "http://")
	siteDomain = strings.TrimSuffix(siteDomain, "/")

	// 如果来看小记的是另一个登录用户（不是本人），顺手算一下两人观影重合数，
	// 把"对外晒"的页面变成一个能把人带回站内、和别人产生连接的入口
	var overlapCount int
	showOverlap := false
	if viewerID := middleware.GetUserID(c); viewerID > 0 && viewerID != userID {
		overlapCount, err = h.Repos.UserMovie.CountOverlapWatched(viewerID, userID)
		if err != nil {
			log.Printf("[PublicMonthly] 计算观影重合数失败 viewer=%d user=%d err=%v", viewerID, userID, err)
		} else {
			showOverlap = true
		}
	}

	c.HTML(http.StatusOK, "share_monthly.html", h.RenderData(c, gin.H{
		"Title":           user.Username + " " + yearMonth + " 月度观影小记 - " + h.Config.SiteName,
		"User":            user,
		"Report":          report,
		"GenreStats":      genreStats,
		"PosterWall":      posterWall,
		"PosterWallExtra": posterWallExtra,
		"MonthlyMovies":   monthlyMovies,
		"Canonical":       shareURL,
		"ProfileURL":      profileURL,
		"SiteDomain":      siteDomain,
		"ShowOverlap":     showOverlap,
		"OverlapCount":    overlapCount,
	}))
}

// extractDoubanUserID 从输入中提取豆瓣用户 ID（支持纯数字 ID 或个人主页链接）
func extractDoubanUserID(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	// 如果是纯数字直接返回
	isAllDigits := true
	for _, c := range input {
		if c < '0' || c > '9' {
			isAllDigits = false
			break
		}
	}
	if isAllDigits {
		return input
	}

	// 从 URL 中提取 people/xxx 或 user/xxx
	patterns := []string{
		`(?:people|user)/(\d+)`,
		`douban\.com/people/(\d+)`,
		`douban\.com/user/(\d+)`,
	}
	for _, p := range patterns {
		re := regexp.MustCompile(p)
		if matches := re.FindStringSubmatch(input); len(matches) >= 2 {
			return matches[1]
		}
	}

	return ""
}

// BindDouban 绑定豆瓣账号
func (h *Handler) BindDouban(c *gin.Context) {
	userID := middleware.GetUserID(c)

	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	input := strings.TrimSpace(c.PostForm("douban_user_id"))
	doubanUserID := extractDoubanUserID(input)
	if doubanUserID == "" {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "请输入有效的豆瓣用户 ID 或主页链接",
		}))
		return
	}

	// 验证用户是否存在且公开
	if err := h.DoubanSyncService.ValidateUser(c.Request.Context(), doubanUserID); err != nil {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "验证豆瓣账号失败: " + err.Error(),
		}))
		return
	}

	// 保存绑定
	if err := h.Repos.User.UpdateDoubanUserID(userID, doubanUserID); err != nil {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "绑定失败，请稍后重试",
		}))
		return
	}

	// 立即创建全量同步任务并触发执行
	if jobID, err := h.DoubanSyncScheduler.CreateFullSyncJob(userID); err != nil {
		log.Printf("[BindDouban] 为用户 %d 创建全量同步任务失败: %v", userID, err)
	} else {
		h.DoubanSyncScheduler.ExecuteJobNow(jobID)
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_bind")
}

// UnbindDouban 解绑豆瓣账号
func (h *Handler) UnbindDouban(c *gin.Context) {
	userID := middleware.GetUserID(c)

	if err := h.Repos.User.UpdateDoubanUserID(userID, ""); err != nil {
		user, _ := h.Repos.User.FindByID(userID)
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "解绑失败，请稍后重试",
		}))
		return
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_unbind")
}

// SyncDouban 手动触发豆瓣同步
func (h *Handler) SyncDouban(c *gin.Context) {
	userID := middleware.GetUserID(c)

	user, err := h.Repos.User.FindByID(userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}

	if user.DoubanUserID == "" {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "请先绑定豆瓣账号",
		}))
		return
	}

	// 检查是否已有运行中的任务
	hasRunning, _ := h.Repos.DoubanSyncJob.HasRunningJob(userID)
	if hasRunning {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "已有同步任务正在运行",
		}))
		return
	}

	// 创建全量同步任务并触发执行
	if jobID, err := h.DoubanSyncScheduler.CreateFullSyncJob(userID); err != nil {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"User":  user,
			"Error": "创建同步任务失败",
		}))
		return
	} else {
		h.DoubanSyncScheduler.ExecuteJobNow(jobID)
	}

	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_sync")
}
