package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/user/moovie/internal/config"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
)

// Handler HTTP 处理器
type Handler struct {
	Repos  *repository.Repositories
	Config *config.Config
}

// NewHandler 创建处理器
func NewHandler(repos *repository.Repositories, cfg *config.Config) *Handler {
	return &Handler{Repos: repos, Config: cfg}
}

// ==================== 公开页面 ====================

// Home 首页
func (h *Handler) Home(c *gin.Context) {
	// 获取热搜关键词
	trending, _ := h.Repos.SearchLog.GetTrending(24, 10)

	c.HTML(http.StatusOK, "home.html", gin.H{
		"Title":    "Moovie - 聚合电影搜索",
		"Trending": trending,
		"UserID":   middleware.GetUserID(c),
	})
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

	c.HTML(http.StatusOK, "search.html", gin.H{
		"Title":   keyword + " - 搜索结果 - Moovie",
		"Keyword": keyword,
		"Results": mockResults,
		"UserID":  middleware.GetUserID(c),
	})
}

// Movie 电影详情页
func (h *Handler) Movie(c *gin.Context) {
	doubanID := c.Param("id")

	movie, err := h.Repos.Movie.FindByDoubanID(doubanID)
	if err != nil || movie == nil {
		c.HTML(http.StatusNotFound, "404.html", gin.H{
			"Title": "电影未找到 - Moovie",
		})
		return
	}

	// 检查是否已收藏
	userID := middleware.GetUserID(c)
	isFavorited := false
	if userID > 0 {
		isFavorited, _ = h.Repos.Favorite.IsFavorited(userID, movie.ID)
	}

	c.HTML(http.StatusOK, "movie.html", gin.H{
		"Title":       movie.Title + " (" + movie.Year + ") - Moovie",
		"Movie":       movie,
		"IsFavorited": isFavorited,
		"UserID":      userID,
	})
}

// Play 播放页
func (h *Handler) Play(c *gin.Context) {
	doubanID := c.Param("id")
	source := c.Query("source")
	episode := c.Query("ep")

	// TODO: 获取播放链接
	c.HTML(http.StatusOK, "play.html", gin.H{
		"Title":    "正在播放 - Moovie",
		"DoubanID": doubanID,
		"Source":   source,
		"Episode":  episode,
		"UserID":   middleware.GetUserID(c),
	})
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
	c.HTML(http.StatusOK, "discover.html", gin.H{
		"Title":  "发现 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// Rankings 排行榜
func (h *Handler) Rankings(c *gin.Context) {
	c.HTML(http.StatusOK, "rankings.html", gin.H{
		"Title":  "排行榜 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// Trends 热搜趋势
func (h *Handler) Trends(c *gin.Context) {
	trending, _ := h.Repos.SearchLog.GetTrending(24, 50)
	c.HTML(http.StatusOK, "trends.html", gin.H{
		"Title":    "热门搜索 - Moovie",
		"Trending": trending,
		"UserID":   middleware.GetUserID(c),
	})
}

// FeedbackPage 反馈页面
func (h *Handler) FeedbackPage(c *gin.Context) {
	c.HTML(http.StatusOK, "feedback.html", gin.H{
		"Title":  "反馈建议 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// About 关于页面
func (h *Handler) About(c *gin.Context) {
	c.HTML(http.StatusOK, "about.html", gin.H{
		"Title":  "关于 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// DMCA DMCA 声明
func (h *Handler) DMCA(c *gin.Context) {
	c.HTML(http.StatusOK, "dmca.html", gin.H{
		"Title":  "DMCA 声明 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// Privacy 隐私政策
func (h *Handler) Privacy(c *gin.Context) {
	c.HTML(http.StatusOK, "privacy.html", gin.H{
		"Title":  "隐私政策 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// Terms 服务协议
func (h *Handler) Terms(c *gin.Context) {
	c.HTML(http.StatusOK, "terms.html", gin.H{
		"Title":  "服务协议 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
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
	c.HTML(http.StatusOK, "login.html", gin.H{
		"Title":    "登录 - Moovie",
		"Redirect": c.Query("redirect"),
	})
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

	// 设置 Cookie
	c.SetCookie("token", token, int(h.Config.JWTExpiry.Seconds()), "/", "", false, true)
	c.Redirect(http.StatusFound, redirect)
}

// RegisterPage 注册页面
func (h *Handler) RegisterPage(c *gin.Context) {
	c.HTML(http.StatusOK, "register.html", gin.H{
		"Title": "注册 - Moovie",
	})
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
	user, err := h.Repos.User.Create(email, password)
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
	c.Redirect(http.StatusFound, "/")
}

// Logout 登出
func (h *Handler) Logout(c *gin.Context) {
	c.SetCookie("token", "", -1, "/", "", false, true)
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
	return token.SignedString([]byte(h.Config.JWTSecret))
}

// ==================== 用户中心 ====================

// Dashboard 用户中心
func (h *Handler) Dashboard(c *gin.Context) {
	userID := middleware.GetUserID(c)

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"Title":  "用户中心 - Moovie",
		"UserID": userID,
	})
}

// Favorites 收藏夹
func (h *Handler) Favorites(c *gin.Context) {
	userID := middleware.GetUserID(c)
	favorites, _ := h.Repos.Favorite.ListByUser(userID, 50, 0)

	c.HTML(http.StatusOK, "favorites.html", gin.H{
		"Title":     "我的收藏 - Moovie",
		"Favorites": favorites,
		"UserID":    userID,
	})
}

// History 观影历史
func (h *Handler) History(c *gin.Context) {
	userID := middleware.GetUserID(c)
	histories, _ := h.Repos.History.ListByUser(userID, 50, 0)

	c.HTML(http.StatusOK, "history.html", gin.H{
		"Title":   "观影历史 - Moovie",
		"History": histories,
		"UserID":  userID,
	})
}

// Settings 账号设置
func (h *Handler) Settings(c *gin.Context) {
	userID := middleware.GetUserID(c)

	c.HTML(http.StatusOK, "settings.html", gin.H{
		"Title":  "账号设置 - Moovie",
		"UserID": userID,
	})
}
