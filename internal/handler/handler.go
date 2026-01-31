package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

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

// Handler HTTP 处理器
type Handler struct {
	Repos                 *repository.Repositories
	Config                *config.Config
	DoubanCrawler         *service.DoubanCrawler
	SearchService         *service.SearchService
	RecommendationService *service.RecommendationService
	SearchCache           *utils.SearchCache[service.SearchResult]
}

// NewHandler 创建处理器
func NewHandler(repos *repository.Repositories, cfg *config.Config) *Handler {
	// 创建爬虫服务
	doubanCrawler := service.NewDoubanCrawler(repos.Movie)

	// 创建资源网爬虫
	sourceCrawler := service.NewSourceCrawler(10 * time.Second)

	// 创建搜索服务
	searchService := service.NewSearchService(repos.Site, repos.VodItem, repos.CopyrightFilter, repos.CategoryFilter, sourceCrawler)

	// 创建推荐服务
	recommendationService := service.NewRecommendationService(repos.Movie)

	// 创建搜索缓存（容量1000条，TTL 3小时）
	searchCache := utils.NewSearchCache[service.SearchResult](1000, 3*time.Hour)

	return &Handler{
		Repos:                 repos,
		Config:                cfg,
		DoubanCrawler:         doubanCrawler,
		SearchService:         searchService,
		RecommendationService: recommendationService,
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
	if strings.HasPrefix(path, "/dashboard") || path == "/favorites" || path == "/history" || path == "/settings" {
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
	case "/player":
		return "player"
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
		// 如果数据库中没有或已被认定为脏数据，尝试从豆瓣抓取
		if h.DoubanCrawler != nil {
			log.Printf("[Handler] 正在从豆瓣抓取/更新信息 ID: %s", doubanID)
			if err := h.DoubanCrawler.CrawlDoubanMovieApi(doubanID); err == nil {
				// 抓取成功后再次查询
				movie, _ = h.Repos.Movie.FindByDoubanID(doubanID)
			} else {
				log.Printf("[Handler] 豆瓣抓取失败: %v", err)
			}
		}
	}

	if movie == nil {
		// 如果采集失败，但传了标题，尝试跳回搜索页
		if title != "" {
			c.Redirect(http.StatusFound, "/search?kw="+url.QueryEscape(title))
			return
		}

		c.HTML(http.StatusNotFound, "404.html", h.RenderData(c, gin.H{
			"Title": "电影未找到 - " + h.Config.SiteName,
		}))
		return
	}

	// 检查是否已收藏
	userID := middleware.GetUserID(c)
	isFavorited := false
	if userID > 0 {
		isFavorited, _ = h.Repos.Favorite.IsFavorited(userID, movie.ID)
	}

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

	c.HTML(http.StatusOK, "movie.html", h.RenderData(c, gin.H{
		"Title":         "《" + movie.Title + "》 (" + movie.Year + ") - 剧情介绍/演职员表 - " + h.Config.SiteName,
		"Description":   desc,
		"Keywords":      strings.Join(keywords, ","),
		"Cover":         "https://image.baidu.com/search/down?url=" + movie.Poster,
		"Canonical":     fmt.Sprintf("%s/movie/%s", h.Config.SiteUrl, movie.DoubanID),
		"Movie":         movie,
		"IsFavorited":   isFavorited,
		"DirectorList":  directorList,
		"SearchTitle":   title,
		"SimilarMovies": movies,
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
	if doubanID != "" && h.DoubanCrawler != nil {
		h.DoubanCrawler.CrawlMovieSafeAsync(doubanID)
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
			if t.Count > 50 { // 24小时内50次就算热
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
			if t.Count > 200 {
				item.Tag = "火爆"
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

// FeedbackPage 反馈页面
func (h *Handler) FeedbackPage(c *gin.Context) {
	c.HTML(http.StatusOK, "feedback.html", h.RenderData(c, gin.H{
		"Title": "反馈建议 - " + h.Config.SiteName,
	}))
}

// About 关于页面
func (h *Handler) About(c *gin.Context) {
	c.HTML(http.StatusOK, "about.html", h.RenderData(c, gin.H{
		"Title": "关于 - " + h.Config.SiteName,
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
	favoriteCount, _ := h.Repos.Favorite.CountByUser(userID)
	historyCount, _ := h.Repos.History.CountByUser(userID)

	c.HTML(http.StatusOK, "dashboard.html", h.RenderData(c, gin.H{
		"Title":         "用户中心 - " + h.Config.SiteName,
		"User":          user,
		"FavoriteCount": favoriteCount,
		"HistoryCount":  historyCount,
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

	c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
		"Title":   "账号设置 - " + h.Config.SiteName,
		"User":    user,
		"Success": success,
	}))
}

// UpdateUsername 修改用户名
func (h *Handler) UpdateUsername(c *gin.Context) {
	userID := middleware.GetUserID(c)
	newUsername := strings.TrimSpace(c.PostForm("username"))

	if newUsername == "" || len(newUsername) < 2 || len(newUsername) > 20 {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"Error": "用户名应在 2-20 个字符之间",
		}))
		return
	}

	err := h.Repos.User.UpdateUsername(userID, newUsername)
	if err != nil {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"Error": "用户名更新失败",
		}))
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
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"Error": "请输入有效的邮箱地址",
		}))
		return
	}

	// 检查邮箱是否已被使用
	existing, _ := h.Repos.User.FindByEmail(newEmail)
	if existing != nil && existing.ID != userID {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"Error": "该邮箱已被其他账号使用",
		}))
		return
	}

	err := h.Repos.User.UpdateEmail(userID, newEmail)
	if err != nil {
		c.HTML(http.StatusOK, "settings.html", h.RenderData(c, gin.H{
			"Title": "账号设置 - " + h.Config.SiteName,
			"Error": "邮箱更新失败",
		}))
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
