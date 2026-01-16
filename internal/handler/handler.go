package handler

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/user/moovie/internal/config"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/service"
	"github.com/user/moovie/internal/utils"
)

// Handler HTTP 处理器
type Handler struct {
	Repos         *repository.Repositories
	Config        *config.Config
	DoubanCrawler *service.DoubanCrawler
	SearchService *service.SearchService
}

// NewHandler 创建处理器
func NewHandler(repos *repository.Repositories, cfg *config.Config) *Handler {
	// 创建爬虫服务
	doubanCrawler := service.NewDoubanCrawler(repos.Movie)

	// 创建资源网爬虫
	sourceCrawler := service.NewSourceCrawler(10 * time.Second)

	// 创建搜索服务
	searchService := service.NewSearchService(repos.Site, repos.VodItem, sourceCrawler)

	return &Handler{
		Repos:         repos,
		Config:        cfg,
		DoubanCrawler: doubanCrawler,
		SearchService: searchService,
	}
}

// RenderData 统一封装公共渲染数据
func (h *Handler) RenderData(c *gin.Context, data gin.H) gin.H {
	// 基础数据
	res := gin.H{
		"SiteName": h.Config.SiteName,
		"SiteUrl":  h.Config.SiteUrl,
		"Path":     c.Request.URL.Path,
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
	case "/rankings":
		return "rankings"
	case "/trends":
		return "trends"
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
		"Title": h.Config.SiteName + " - 聚合电影搜索",
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
	c.HTML(http.StatusOK, "search.html", h.RenderData(c, gin.H{
		"Title":   keyword + " - 搜索结果 - " + h.Config.SiteName,
		"Keyword": keyword,
	}))
}

// Movie 电影详情页
func (h *Handler) Movie(c *gin.Context) {
	doubanID := c.Param("id")

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
			if err := h.DoubanCrawler.CrawlDoubanMovie(doubanID); err == nil {
				// 抓取成功后再次查询
				movie, _ = h.Repos.Movie.FindByDoubanID(doubanID)
			} else {
				log.Printf("[Handler] 豆瓣抓取失败: %v", err)
			}
		}
	}

	if movie == nil {
		// 如果采集失败，但传了标题，尝试跳回搜索页
		title := c.Query("title")
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

	c.HTML(http.StatusOK, "movie.html", h.RenderData(c, gin.H{
		"Title":       movie.Title + " (" + movie.Year + ") - " + h.Config.SiteName,
		"Movie":       movie,
		"IsFavorited": isFavorited,
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

	// 动态生成标题
	pageTitle := detail.VodName
	if episode != "" {
		pageTitle += "(" + episode + ")"
	}
	pageTitle += " - 在线播放 - " + h.Config.SiteName

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
		"Title":        "M3U8 播放器 - Moovie",
		"URL":          url,
		"ContentClass": "full-width",
	}))
}

// Discover 发现/分类页
func (h *Handler) Discover(c *gin.Context) {
	movieType := c.DefaultQuery("type", "movie")

	subjects, err := h.DoubanCrawler.GetPopularSubjects(movieType)
	if err != nil {
		log.Printf("获取热门电影失败: %v", err)
	}

	c.HTML(http.StatusOK, "discover.html", h.RenderData(c, gin.H{
		"Title":       "发现 - " + h.Config.SiteName,
		"Subjects":    subjects,
		"CurrentType": movieType,
	}))
}

// Rankings 排行榜
func (h *Handler) Rankings(c *gin.Context) {
	// 获取真实的热搜数据 (最近 24 小时)
	trending, err := h.Repos.SearchLog.GetTrending(24, 10)
	if err != nil {
		log.Printf("获取热搜失败: %v", err)
	}

	// 假数据：热门电影列表
	hotMovies := []model.Movie{
		{
			DoubanID:  "35465232",
			Title:     "封神第一部：朝歌风云",
			Year:      "2023",
			Poster:    "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2898748250.webp",
			Rating:    7.8,
			Genres:    "剧情,动作,奇幻",
			Directors: "乌尔善",
		},
		{
			DoubanID:  "26647087",
			Title:     "三体",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2885955777.webp",
			Rating:    8.7,
			Genres:    "剧情,科幻",
			Directors: "杨磊",
		},
		{
			DoubanID:  "35267208",
			Title:     "流浪地球2",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2885842436.webp",
			Rating:    8.3,
			Genres:    "科幻,冒险,灾难",
			Directors: "郭帆",
		},
		{
			DoubanID:  "35183042",
			Title:     "狂飙",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2884063548.webp",
			Rating:    8.5,
			Genres:    "剧情,犯罪",
			Directors: "徐纪周",
		},
		{
			DoubanID:  "36190039",
			Title:     "繁花",
			Year:      "2023",
			Poster:    "https://img9.doubanio.com/view/photo/s_ratio_poster/public/p2904209695.webp",
			Rating:    8.7,
			Genres:    "剧情",
			Directors: "王家卫",
		},
		{
			DoubanID:  "35069Mo4",
			Title:     "漫长的季节",
			Year:      "2023",
			Poster:    "/api/proxy/image?url=https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2894989679.webp",
			Rating:    9.4,
			Genres:    "剧情,悬疑",
			Directors: "辛爽",
		},
		{
			DoubanID:  "26873Mo3",
			Title:     "奥本海默",
			Year:      "2023",
			Poster:    "/api/proxy/image?url=https://img9.doubanio.com/view/photo/s_ratio_poster/public/p2893907974.webp",
			Rating:    8.9,
			Genres:    "剧情,传记,历史",
			Directors: "克里斯托弗·诺兰",
		},
		{
			DoubanID:  "35551Mo9",
			Title:     "芭比",
			Year:      "2023",
			Poster:    "/api/proxy/image?url=https://img3.doubanio.com/view/photo/s_ratio_poster/public/p2895879710.webp",
			Rating:    8.3,
			Genres:    "喜剧,冒险,奇幻",
			Directors: "格蕾塔·葛韦格",
		},
		{
			DoubanID:  "30475768",
			Title:     "坠落的审判",
			Year:      "2023",
			Poster:    "/api/proxy/image?url=https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2899335708.webp",
			Rating:    8.8,
			Genres:    "剧情,悬疑,家庭",
			Directors: "茹斯汀·特里耶",
		},
		{
			DoubanID:  "35900652",
			Title:     "年会不能停！",
			Year:      "2023",
			Poster:    "/api/proxy/image?url=https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2902429131.webp",
			Rating:    8.1,
			Genres:    "喜剧",
			Directors: "董润年",
		},
	}

	c.HTML(http.StatusOK, "rankings.html", h.RenderData(c, gin.H{
		"Title":     "热门电影 - " + h.Config.SiteName,
		"HotMovies": hotMovies,
		"Trending":  trending,
	}))
}

// Trends 热搜趋势
func (h *Handler) Trends(c *gin.Context) {
	// 1. 获取 24 小时热搜
	trending24h, err := h.Repos.SearchLog.GetTrending(24, 20)
	if err != nil {
		log.Printf("获取 24h 热搜失败: %v", err)
	}

	// 2. 获取全量热搜
	trendingAll, err := h.Repos.SearchLog.GetTrending(0, 50)
	if err != nil {
		log.Printf("获取全量热搜失败: %v", err)
	}

	// 辅助结构
	type TrendItem struct {
		Keyword  string
		Count    int
		Tag      string
		TagClass string
	}

	// 转换 24h 数据
	var items24h []TrendItem
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

	// 转换全量数据
	var itemsAll []TrendItem
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

	c.HTML(http.StatusOK, "trends.html", h.RenderData(c, gin.H{
		"Title":       "热门搜索 - " + h.Config.SiteName,
		"Trending24h": items24h,
		"TrendingAll": itemsAll,
		"UpdateTime":  time.Now().Format("15:04"),
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
		{"/discover", "0.8", "daily"},
		{"/rankings", "0.8", "daily"},
		{"/trends", "0.8", "daily"},
		{"/about", "0.5", "monthly"},
		{"/dmca", "0.5", "monthly"},
		{"/privacy", "0.5", "monthly"},
		{"/terms", "0.5", "monthly"},
	}

	for _, p := range staticPages {
		sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s%s</loc>\n    <changefreq>%s</changefreq>\n    <priority>%s</priority>\n  </url>\n", baseUrl, p.path, p.freq, p.priority))
	}

	// 2. 电影详情页 (取最近更新的 1000 条)
	movies, err := h.Repos.Movie.GetSitemapMovies(1000)
	if err == nil {
		for _, m := range movies {
			lastmod := m.UpdatedAt.Format("2006-01-02")
			sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s/movie/%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>weekly</changefreq>\n    <priority>0.7</priority>\n  </url>\n", baseUrl, m.DoubanID, lastmod))
		}
	}

	sb.WriteString(`</urlset>`)

	c.Header("Content-Type", "application/xml")
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

	if redirect == "" {
		redirect = "/"
	}

	// 查找用户
	user, err := h.Repos.User.FindByEmail(email)
	if err != nil || user == nil {
		c.HTML(http.StatusOK, "login.html", gin.H{
			"Title": "登录 - Moovie",
			"Error": "邮箱或密码错误",
		})
		return
	}

	// 验证密码
	if !h.Repos.User.CheckPassword(user, password) {
		c.HTML(http.StatusOK, "login.html", gin.H{
			"Title": "登录 - Moovie",
			"Error": "邮箱或密码错误",
		})
		return
	}

	// 生成 JWT
	token, err := h.generateToken(user)
	if err != nil {
		c.HTML(http.StatusInternalServerError, "login.html", gin.H{
			"Title": "登录 - Moovie",
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

	// 验证
	if password != confirmPassword {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie",
			"Error": "两次输入的密码不一致",
		})
		return
	}

	if len(password) < 6 {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie",
			"Error": "密码至少需要 6 个字符",
		})
		return
	}

	// 检查邮箱是否已存在
	existing, _ := h.Repos.User.FindByEmail(email)
	if existing != nil {
		c.HTML(http.StatusOK, "register.html", gin.H{
			"Title": "注册 - Moovie",
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
			"Title": "注册 - Moovie",
			"Error": "注册失败，请重试",
		})
		return
	}

	// 生成 JWT 并登录
	token, _ := h.generateToken(user)
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

// generateToken 生成 JWT
func (h *Handler) generateToken(user *model.User) (string, error) {
	claims := &middleware.Claims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(h.Config.JWTExpiry)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(h.Config.AppSecret))
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

	// 获取收藏和历史列表（用于 Tab 默认显示）
	favorites, _ := h.Repos.Favorite.ListByUser(userID, 20, 0)
	histories, _ := h.Repos.History.ListByUser(userID, 20, 0)

	c.HTML(http.StatusOK, "dashboard.html", h.RenderData(c, gin.H{
		"Title":         "用户中心 - " + h.Config.SiteName,
		"User":          user,
		"FavoriteCount": favoriteCount,
		"HistoryCount":  historyCount,
		"Favorites":     favorites,
		"History":       histories,
	}))
}

// Favorites 收藏夹
func (h *Handler) Favorites(c *gin.Context) {
	userID := middleware.GetUserID(c)
	favorites, _ := h.Repos.Favorite.ListByUser(userID, 50, 0)

	c.HTML(http.StatusOK, "favorites.html", h.RenderData(c, gin.H{
		"Title":     "我的收藏 - " + h.Config.SiteName,
		"Favorites": favorites,
	}))
}

// History 观影历史
func (h *Handler) History(c *gin.Context) {
	userID := middleware.GetUserID(c)
	histories, _ := h.Repos.History.ListByUser(userID, 50, 0)

	c.HTML(http.StatusOK, "history.html", h.RenderData(c, gin.H{
		"Title":   "观影历史 - " + h.Config.SiteName,
		"History": histories,
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
