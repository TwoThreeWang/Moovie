package handler

import (
	"net/http"
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
)

// Handler HTTP 处理器
type Handler struct {
	Repos         *repository.Repositories
	Config        *config.Config
	Crawler       *service.Crawler
	SearchService *service.SearchService
}

// NewHandler 创建处理器
func NewHandler(repos *repository.Repositories, cfg *config.Config) *Handler {
	// 创建爬虫服务
	crawler := service.NewCrawler(repos.Movie)

	// 创建资源网爬虫
	sourceCrawler := service.NewSourceCrawler(10 * time.Second)

	// 创建搜索服务
	searchService := service.NewSearchService(repos.Site, repos.SearchCache, sourceCrawler)

	return &Handler{
		Repos:         repos,
		Config:        cfg,
		Crawler:       crawler,
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
	res["ActiveMenu"] = h.getActiveMenu(c.Request.URL.Path)

	// 合并传入的数据
	for k, v := range data {
		res[k] = v
	}

	return res
}

// getActiveMenu 根据路径判断当前高亮菜单
func (h *Handler) getActiveMenu(path string) string {
	switch path {
	case "/":
		return "home"
	case "/discover":
		return "discover"
	case "/rankings":
		return "rankings"
	case "/trends":
		return "trends"
	case "/dashboard", "/favorites", "/history", "/settings":
		return "user"
	default:
		return ""
	}
}

// ==================== 公开页面 ====================

// Home 首页
func (h *Handler) Home(c *gin.Context) {
	// 获取热搜关键词
	trending, _ := h.Repos.SearchLog.GetTrending(24, 10)

	c.HTML(http.StatusOK, "home.html", h.RenderData(c, gin.H{
		"Title":    h.Config.SiteName + " - 聚合电影搜索",
		"Trending": trending,
	}))
}

// Search 搜索结果页
func (h *Handler) Search(c *gin.Context) {
	keyword := c.Query("q")
	if keyword == "" {
		c.Redirect(http.StatusFound, "/")
		return
	}

	// 示例数据，用于展示样式效果
	mockResults := []map[string]interface{}{
		{
			"DoubanID":      "1291546",
			"Title":         "霸王别姬",
			"OriginalTitle": "Erta",
			"Poster":        "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2561716440.webp",
			"Rating":        "9.6",
			"RatingCount":   "2018732",
			"Year":          "1993",
			"Region":        "中国大陆 / 中国香港",
			"Genres":        "剧情 / 爱情 / 同性",
			"Duration":      "171分钟",
			"Playable":      true,
			"Sources": []map[string]interface{}{
				{"Name": "爱奇艺", "Status": "available", "Speed": "2.3s"},
				{"Name": "优酷", "Status": "available", "Speed": "1.8s"},
				{"Name": "腾讯", "Status": "slow", "Speed": "5.2s"},
			},
		},
		{
			"DoubanID":      "1292052",
			"Title":         "肖申克的救赎",
			"OriginalTitle": "The Shawshank Redemption",
			"Poster":        "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p480747492.webp",
			"Rating":        "9.7",
			"RatingCount":   "3125634",
			"Year":          "1994",
			"Region":        "美国",
			"Genres":        "剧情 / 犯罪",
			"Duration":      "142分钟",
			"Playable":      true,
			"Sources": []map[string]interface{}{
				{"Name": "Netflix", "Status": "available", "Speed": "1.2s"},
				{"Name": "B站", "Status": "unavailable", "Speed": ""},
			},
		},
		{
			"DoubanID":      "1291561",
			"Title":         "千与千寻",
			"OriginalTitle": "千と千尋の神隠し",
			"Poster":        "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2557573348.webp",
			"Rating":        "9.4",
			"RatingCount":   "2456123",
			"Year":          "2001",
			"Region":        "日本",
			"Genres":        "剧情 / 动画 / 奇幻",
			"Duration":      "125分钟",
			"Playable":      true,
			"Sources": []map[string]interface{}{
				{"Name": "B站", "Status": "available", "Speed": "0.8s"},
			},
		},
		{
			"DoubanID":      "1292720",
			"Title":         "阿甘正传",
			"OriginalTitle": "Forrest Gump",
			"Poster":        "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2372307693.webp",
			"Rating":        "9.5",
			"RatingCount":   "2234567",
			"Year":          "1994",
			"Region":        "美国",
			"Genres":        "剧情 / 爱情",
			"Duration":      "142分钟",
			"Playable":      false,
			"Sources":       []map[string]interface{}{},
		},
	}

	c.HTML(http.StatusOK, "search.html", h.RenderData(c, gin.H{
		"Title":   keyword + " - 搜索结果 - " + h.Config.SiteName,
		"Keyword": keyword,
		"Results": mockResults,
	}))
}

// Movie 电影详情页
func (h *Handler) Movie(c *gin.Context) {
	doubanID := c.Param("id")

	movie, err := h.Repos.Movie.FindByDoubanID(doubanID)
	if err != nil || movie == nil {
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
	doubanID := c.Param("id")
	source := c.Query("source")
	episode := c.Query("ep")

	// TODO: 获取播放链接
	c.HTML(http.StatusOK, "play.html", h.RenderData(c, gin.H{
		"Title":    "正在播放 - " + h.Config.SiteName,
		"DoubanID": doubanID,
		"Source":   source,
		"Episode":  episode,
	}))
}

// Player m3u8 专用播放器
func (h *Handler) Player(c *gin.Context) {
	url := c.Query("url")
	c.HTML(http.StatusOK, "player.html", gin.H{
		"Title": "M3U8 播放器 - Moovie",
		"URL":   url,
	})
}

// Discover 发现/分类页
func (h *Handler) Discover(c *gin.Context) {
	c.HTML(http.StatusOK, "discover.html", h.RenderData(c, gin.H{
		"Title": "发现 - " + h.Config.SiteName,
	}))
}

// Rankings 排行榜
func (h *Handler) Rankings(c *gin.Context) {
	// 假数据：热门电影列表
	hotMovies := []model.Movie{
		{
			DoubanID:  "35465232",
			Title:     "封神第一部：朝歌风云",
			Year:      "2023",
			Poster:    "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2898748250.webp",
			Rating:    7.8,
			Genres:    []string{"剧情", "动作", "奇幻"},
			Directors: []model.Person{{Name: "乌尔善"}},
		},
		{
			DoubanID:  "26647087",
			Title:     "三体",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2885955777.webp",
			Rating:    8.7,
			Genres:    []string{"剧情", "科幻"},
			Directors: []model.Person{{Name: "杨磊"}},
		},
		{
			DoubanID:  "35267208",
			Title:     "流浪地球2",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2885842436.webp",
			Rating:    8.3,
			Genres:    []string{"科幻", "冒险", "灾难"},
			Directors: []model.Person{{Name: "郭帆"}},
		},
		{
			DoubanID:  "35183042",
			Title:     "狂飙",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2884063548.webp",
			Rating:    8.5,
			Genres:    []string{"剧情", "犯罪"},
			Directors: []model.Person{{Name: "徐纪周"}},
		},
		{
			DoubanID:  "36190039",
			Title:     "繁花",
			Year:      "2023",
			Poster:    "https://img9.doubanio.com/view/photo/s_ratio_poster/public/p2904209695.webp",
			Rating:    8.7,
			Genres:    []string{"剧情"},
			Directors: []model.Person{{Name: "王家卫"}},
		},
		{
			DoubanID:  "35069Mo4",
			Title:     "漫长的季节",
			Year:      "2023",
			Poster:    "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2894989679.webp",
			Rating:    9.4,
			Genres:    []string{"剧情", "悬疑"},
			Directors: []model.Person{{Name: "辛爽"}},
		},
		{
			DoubanID:  "26873Mo3",
			Title:     "奥本海默",
			Year:      "2023",
			Poster:    "https://img9.doubanio.com/view/photo/s_ratio_poster/public/p2893907974.webp",
			Rating:    8.9,
			Genres:    []string{"剧情", "传记", "历史"},
			Directors: []model.Person{{Name: "克里斯托弗·诺兰"}},
		},
		{
			DoubanID:  "35551Mo9",
			Title:     "芭比",
			Year:      "2023",
			Poster:    "https://img3.doubanio.com/view/photo/s_ratio_poster/public/p2895879710.webp",
			Rating:    8.3,
			Genres:    []string{"喜剧", "冒险", "奇幻"},
			Directors: []model.Person{{Name: "格蕾塔·葛韦格"}},
		},
		{
			DoubanID:  "30475768",
			Title:     "坠落的审判",
			Year:      "2023",
			Poster:    "https://img1.doubanio.com/view/photo/s_ratio_poster/public/p2899335708.webp",
			Rating:    8.8,
			Genres:    []string{"剧情", "悬疑", "家庭"},
			Directors: []model.Person{{Name: "茹斯汀·特里耶"}},
		},
		{
			DoubanID:  "35900652",
			Title:     "年会不能停！",
			Year:      "2023",
			Poster:    "https://img2.doubanio.com/view/photo/s_ratio_poster/public/p2902429131.webp",
			Rating:    8.1,
			Genres:    []string{"喜剧"},
			Directors: []model.Person{{Name: "董润年"}},
		},
	}

	c.HTML(http.StatusOK, "rankings.html", h.RenderData(c, gin.H{
		"Title":     "热门电影 - " + h.Config.SiteName,
		"HotMovies": hotMovies,
	}))
}

// Trends 热搜趋势
func (h *Handler) Trends(c *gin.Context) {
	// 假数据：热门搜索关键词
	type TrendItem struct {
		Keyword  string
		Count    string
		Tag      string
		TagClass string
	}

	trending := []TrendItem{
		{Keyword: "三体", Count: "2.3万", Tag: "热", TagClass: "hot"},
		{Keyword: "繁花", Count: "1.8万", Tag: "新", TagClass: "new"},
		{Keyword: "漫长的季节", Count: "1.5万", Tag: "荐", TagClass: "recommend"},
		{Keyword: "狂飙", Count: "1.2万", Tag: "剧", TagClass: "tv"},
		{Keyword: "流浪地球2", Count: "9876", Tag: "影", TagClass: "movie"},
		{Keyword: "封神第一部", Count: "8234", Tag: "", TagClass: ""},
		{Keyword: "奥本海默", Count: "7652", Tag: "影", TagClass: "movie"},
		{Keyword: "年会不能停", Count: "6543", Tag: "新", TagClass: "new"},
		{Keyword: "芭比", Count: "5432", Tag: "", TagClass: ""},
		{Keyword: "坠落的审判", Count: "4321", Tag: "", TagClass: ""},
		{Keyword: "涉过愤怒的海", Count: "3890", Tag: "", TagClass: ""},
		{Keyword: "周处除三害", Count: "3654", Tag: "热", TagClass: "hot"},
		{Keyword: "热辣滚烫", Count: "3210", Tag: "", TagClass: ""},
		{Keyword: "第二十条", Count: "2987", Tag: "", TagClass: ""},
		{Keyword: "飞驰人生2", Count: "2765", Tag: "", TagClass: ""},
		{Keyword: "你想活出怎样的人生", Count: "2543", Tag: "", TagClass: ""},
		{Keyword: "沙丘2", Count: "2321", Tag: "新", TagClass: "new"},
		{Keyword: "哥斯拉大战金刚2", Count: "2109", Tag: "", TagClass: ""},
		{Keyword: "功夫熊猫4", Count: "1987", Tag: "", TagClass: ""},
		{Keyword: "猩球崛起：新世界", Count: "1765", Tag: "", TagClass: ""},
	}

	c.HTML(http.StatusOK, "trends.html", h.RenderData(c, gin.H{
		"Title":      "热门搜索 - " + h.Config.SiteName,
		"Trending":   trending,
		"UpdateTime": "刚刚",
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
	// TODO: 动态生成站点地图
	c.Header("Content-Type", "application/xml")
	c.String(http.StatusOK, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://moovie.example.com/</loc></url>
</urlset>`)
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
