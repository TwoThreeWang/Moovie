package handler

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/utils"
)

// ==================== htmx API ====================

// AddFavorite 添加收藏（htmx）
func (h *Handler) AddFavorite(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, `<button class="btn btn-primary" disabled>请先登录</button>`)
		return
	}

	movieID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "无效的电影 ID")
		return
	}

	if err := h.Repos.Favorite.Add(userID, movieID); err != nil {
		c.String(http.StatusInternalServerError, "收藏失败")
		return
	}

	// 返回已收藏状态的按钮
	c.HTML(http.StatusOK, "partials/favorite_btn.html", gin.H{
		"MovieID":     movieID,
		"IsFavorited": true,
	})
}

// RemoveFavorite 取消收藏（htmx）
func (h *Handler) RemoveFavorite(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, `<button class="btn btn-primary" disabled>请先登录</button>`)
		return
	}

	movieID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "无效的电影 ID")
		return
	}

	if err := h.Repos.Favorite.Remove(userID, movieID); err != nil {
		c.String(http.StatusInternalServerError, "取消收藏失败")
		return
	}

	// 返回未收藏状态的按钮
	c.HTML(http.StatusOK, "partials/favorite_btn.html", gin.H{
		"MovieID":     movieID,
		"IsFavorited": false,
	})
}

// SubmitFeedback 提交反馈（htmx）
func (h *Handler) SubmitFeedback(c *gin.Context) {
	feedback := &model.Feedback{
		UserID:   middleware.GetUserIDPtr(c),
		Type:     c.PostForm("type"),
		Content:  c.PostForm("content"),
		MovieURL: c.PostForm("movie_url"),
	}

	if feedback.Content == "" {
		c.String(http.StatusBadRequest, `<div class="alert alert-error">请填写反馈内容</div>`)
		return
	}

	if err := h.Repos.Feedback.Create(feedback); err != nil {
		c.String(http.StatusInternalServerError, `<div class="alert alert-error">提交失败，请重试</div>`)
		return
	}

	c.String(http.StatusOK, `<div class="alert alert-success">感谢您的反馈！我们会尽快处理。</div>`)
}

// SyncHistory 同步观影历史（JSON API）
func (h *Handler) SyncHistory(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		utils.Unauthorized(c, "未登录")
		return
	}

	var req struct {
		Records    []*model.WatchHistory `json:"records"`
		LastSyncAt int64                 `json:"lastSyncAt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "无效的请求数据")
		return
	}

	// 保存客户端记录到服务端
	for _, record := range req.Records {
		record.UserID = userID
		h.Repos.History.Upsert(record)
	}

	// 获取服务端最新记录返回给客户端
	serverRecords, _ := h.Repos.History.ListByUser(userID, 100, 0)

	var serverSyncedAt int64
	if len(serverRecords) > 0 {
		serverSyncedAt = serverRecords[0].WatchedAt.Unix()
	}

	utils.Success(c, gin.H{
		"serverRecords":  serverRecords,
		"serverSyncedAt": serverSyncedAt,
	})
}

// ==================== 豆瓣电影搜索API ====================

// DoubanMovieSuggest 豆瓣电影搜索建议
type DoubanMovieSuggest struct {
	Episode  string `json:"episode"`
	Img      string `json:"img"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Type     string `json:"type"`
	Year     string `json:"year"`
	SubTitle string `json:"sub_title"`
	ID       string `json:"id"`
}

// MovieSuggestResponse 返回给前端的电影建议
type MovieSuggestResponse struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SubTitle string `json:"sub_title"`
	Type     string `json:"type"`
	Year     string `json:"year"`
	Episode  string `json:"episode"`
	Img      string `json:"img"`
}

// MovieSuggest 电影搜索建议API
func (h *Handler) MovieSuggest(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("q"))
	if keyword == "" {
		utils.BadRequest(c, "搜索关键词不能为空")
		return
	}

	// 检查缓存
	cacheKey := fmt.Sprintf("douban_suggest:%s", keyword)
	if cached, found := utils.CacheGet(cacheKey); found {
		utils.Success(c, cached)
		return
	}

	// 调用豆瓣API
	url := fmt.Sprintf("https://movie.douban.com/j/subject_suggest?q=%s", keyword)

	// 使用自定义HTTP客户端
	client := utils.NewHTTPClient()
	var doubanResults []DoubanMovieSuggest

	if err := client.GetJSON(url, &doubanResults); err != nil {
		utils.InternalServerError(c, "搜索服务暂时不可用")
		log.Printf("豆瓣API调用失败: %v", err)
		return
	}

	// 转换数据格式
	var results []MovieSuggestResponse
	for _, item := range doubanResults {
		// 使用本地图片代理，绕过防盗链
		proxyImg := fmt.Sprintf("/api/proxy/image?url=%s", item.Img)

		results = append(results, MovieSuggestResponse{
			ID:       item.ID,
			Title:    item.Title,
			SubTitle: item.SubTitle,
			Type:     item.Type,
			Year:     item.Year,
			Episode:  item.Episode,
			Img:      proxyImg,
		})
	}

	// 缓存结果，缓存时间5分钟
	utils.CacheSet(cacheKey, results, 5*time.Minute)

	utils.Success(c, results)
}

// ProxyImage 图片代理，绕过防盗链
func (h *Handler) ProxyImage(c *gin.Context) {
	targetURL := c.Query("url")
	if targetURL == "" {
		c.String(http.StatusBadRequest, "URL 不能为空")
		return
	}

	// 只允许代理特定的域名（安全考虑）
	if !strings.Contains(targetURL, "doubanio.com") {
		c.String(http.StatusForbidden, "不支持的图片源")
		return
	}

	// 创建请求
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		c.String(http.StatusInternalServerError, "创建请求失败")
		return
	}

	// 设置伪造的 Referer
	req.Header.Set("Referer", "https://movie.douban.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	// 使用默认客户端发送请求
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		c.String(http.StatusBadGateway, "代理请求失败")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.String(resp.StatusCode, "图片源返回错误")
		return
	}

	// 转发 Content-Type
	c.Header("Content-Type", resp.Header.Get("Content-Type"))
	c.Header("Cache-Control", "public, max-age=86400") // 缓存一天

	// 流式转发响应体
	io.Copy(c.Writer, resp.Body)
}

// ==================== 电影检查 API ====================

// CheckMovieResponse 检查电影响应
type CheckMovieResponse struct {
	Exists      bool   `json:"exists"`
	MovieID     int    `json:"movie_id,omitempty"`
	RedirectURL string `json:"redirect_url"`
}

// CheckMovie 检查电影是否存在，并决定跳转目标
// GET /api/movies/check/:doubanId?title=xxx
func (h *Handler) CheckMovie(c *gin.Context) {
	doubanID := c.Param("doubanId")
	title := c.Query("title")

	if doubanID == "" {
		utils.BadRequest(c, "豆瓣ID不能为空")
		return
	}

	// 查询数据库中是否存在该电影
	movie, err := h.Repos.Movie.FindByDoubanID(doubanID)
	if err != nil {
		log.Printf("查询电影失败: %v", err)
		utils.InternalServerError(c, "查询失败")
		return
	}

	if movie != nil {
		// 电影存在，跳转到详情页（使用豆瓣ID）
		utils.Success(c, CheckMovieResponse{
			Exists:      true,
			MovieID:     movie.ID,
			RedirectURL: fmt.Sprintf("/movie/%s", movie.DoubanID),
		})
		return
	}

	// 电影不存在，触发异步爬取
	if h.Crawler != nil {
		h.Crawler.CrawlAsync(doubanID)
		log.Printf("[API] 触发异步爬取豆瓣电影: %s", doubanID)
	}

	// 跳转到搜索页
	redirectURL := "/search"
	if title != "" {
		redirectURL = fmt.Sprintf("/search?q=%s", title)
	}

	utils.Success(c, CheckMovieResponse{
		Exists:      false,
		RedirectURL: redirectURL,
	})
}

// ==================== 资源网视频搜索 API ====================

// VodSearchResponse 资源网搜索响应
type VodSearchResponse struct {
	Items     []model.VodItem `json:"items"`
	FromCache bool            `json:"from_cache"`
	Expired   bool            `json:"expired"`
	Total     int             `json:"total"`
}

// VodSearch 资源网视频搜索
// GET /api/vod/search?keyword=xxx
func (h *Handler) VodSearch(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("keyword"))
	if keyword == "" {
		utils.BadRequest(c, "搜索关键词不能为空")
		return
	}

	// 使用 SearchService 搜索
	result, err := h.SearchService.Search(c.Request.Context(), keyword)
	if err != nil {
		log.Printf("[VodSearch] 搜索失败: %v", err)
		utils.InternalServerError(c, "搜索服务暂时不可用")
		return
	}

	// 记录搜索日志
	go func() {
		_ = h.Repos.SearchLog.Log(keyword, middleware.GetUserIDPtr(c), utils.HashIP(c.ClientIP()))
	}()

	utils.Success(c, VodSearchResponse{
		Items:     result.Items,
		FromCache: result.FromCache,
		Expired:   result.Expired,
		Total:     len(result.Items),
	})
}

// VodDetail 资源网视频详情
// GET /api/vod/detail?source_key=xxx&vod_id=xxx
func (h *Handler) VodDetail(c *gin.Context) {
	sourceKey := strings.TrimSpace(c.Query("source_key"))
	vodId := strings.TrimSpace(c.Query("vod_id"))

	if sourceKey == "" || vodId == "" {
		utils.BadRequest(c, "source_key 和 vod_id 不能为空")
		return
	}

	// 获取详情
	detail, err := h.SearchService.GetDetail(c.Request.Context(), sourceKey, vodId)
	if err != nil {
		log.Printf("[VodDetail] 获取详情失败: %v", err)
		utils.InternalServerError(c, "获取详情失败")
		return
	}

	if detail == nil {
		utils.NotFound(c, "视频不存在")
		return
	}

	utils.Success(c, detail)
}
