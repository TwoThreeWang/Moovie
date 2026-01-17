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

// SyncHistoryReq 同步请求结构
type SyncHistoryReq struct {
	Records    []HistoryRecordDTO `json:"records"`
	LastSyncAt int64              `json:"lastSyncAt"`
}

// HistoryRecordDTO 观影历史 DTO（用于处理前端毫秒时间戳）
type HistoryRecordDTO struct {
	DoubanID  string  `json:"douban_id"`
	VodID     string  `json:"vod_id"`
	Title     string  `json:"title"`
	Poster    string  `json:"poster"`
	Episode   string  `json:"episode"`
	Progress  int     `json:"progress"`
	LastTime  float64 `json:"last_time"`
	Duration  float64 `json:"duration"`
	Source    string  `json:"source"`
	WatchedAt int64   `json:"watchedAt"` // 毫秒时间戳
}

// SyncHistory 同步观影历史（JSON API）
func (h *Handler) SyncHistory(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		utils.Unauthorized(c, "未登录")
		return
	}

	var req SyncHistoryReq
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "无效的请求数据")
		return
	}

	// 1. 将客户端记录保存到服务端
	for _, dto := range req.Records {
		record := &model.WatchHistory{
			UserID:    userID,
			DoubanID:  dto.DoubanID,
			VodID:     dto.VodID,
			Title:     dto.Title,
			Poster:    dto.Poster,
			Episode:   dto.Episode,
			Progress:  dto.Progress,
			LastTime:  dto.LastTime,
			Duration:  dto.Duration,
			Source:    dto.Source,
			WatchedAt: time.UnixMilli(dto.WatchedAt),
		}
		// 异步处理以提高响应速度，或者同步处理确保一致性
		// 这里选择同步处理，因为观影记录不多且需要确保同步成功
		if err := h.Repos.History.Upsert(record); err != nil {
			log.Printf("[SyncHistory] 保存记录失败: %v", err)
		}
	}

	// 2. 获取服务端在 lastSyncAt 之后的所有更新返回给客户端
	// 将 lastSyncAt (毫秒) 转换为 time.Time
	lastSyncTime := time.UnixMilli(req.LastSyncAt)
	serverRecords, err := h.Repos.History.GetAfter(userID, lastSyncTime)
	if err != nil {
		log.Printf("[SyncHistory] 获取服务端新记录失败: %v", err)
	}

	// 3. 返回同步成功的最新状态
	utils.Success(c, gin.H{
		"serverRecords": serverRecords,
		"syncedAt":      time.Now().UnixMilli(),
	})
}

// RemoveHistory 删除观影历史记录
func (h *Handler) RemoveHistory(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		utils.Unauthorized(c, "未登录")
		return
	}

	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		utils.BadRequest(c, "无效的记录 ID")
		return
	}

	if err := h.Repos.History.Delete(userID, id); err != nil {
		log.Printf("[RemoveHistory] 删除记录失败: %v", err)
		utils.InternalServerError(c, "删除失败")
		return
	}

	// 如果是 HTMX 请求，返回空响应
	if c.GetHeader("HX-Request") != "" {
		c.Status(http.StatusOK)
		return
	}

	utils.Success(c, nil)
}

// MovieSuggest 电影搜索建议（JSON API）
func (h *Handler) MovieSuggest(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("kw"))
	if keyword == "" {
		utils.BadRequest(c, "搜索关键词不能为空")
		return
	}

	results, err := h.DoubanCrawler.SearchSuggest(keyword)
	if err != nil {
		utils.InternalServerError(c, "搜索建议服务暂时不可用")
		log.Printf("豆瓣搜索建议失败: %v", err)
		return
	}

	utils.Success(c, results)
}

// ProxyImage 图片代理，绕过防盗链
func (h *Handler) ProxyImage(c *gin.Context) {
	targetURL := c.Query("url")
	if targetURL == "" {
		c.String(http.StatusBadRequest, "URL 不能为空")
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
	if h.DoubanCrawler != nil {
		h.DoubanCrawler.CrawlAsync(doubanID)
		log.Printf("[API] 触发异步爬取豆瓣电影: %s", doubanID)
	}

	// 跳转到搜索页
	redirectURL := "/search"
	if title != "" {
		redirectURL = fmt.Sprintf("/search?kw=%s", title)
	}

	utils.Success(c, CheckMovieResponse{
		Exists:      false,
		RedirectURL: redirectURL,
	})
}

// ==================== 资源网视频搜索 API ====================

// VodSearchResponse 资源网搜索响应
type VodSearchResponse struct {
	Items []model.VodItem `json:"items"`
	Total int             `json:"total"`
}

// VodSearch 资源网视频搜索
// GET /api/vod/search?kw=xxx
func (h *Handler) VodSearch(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("kw"))
	if keyword == "" {
		utils.BadRequest(c, "搜索关键词不能为空")
		return
	}

	// 使用 SearchService 搜索
	result, err := h.SearchService.Search(c.Request.Context(), keyword, false)
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
		Items: result.Items,
		Total: len(result.Items),
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

// SearchHTMX 资源网搜索（htmx 片段）
// GET /api/htmx/search?kw=xxx&year=xxx
func (h *Handler) SearchHTMX(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("kw"))
	// 对关键词进行清洗，过滤掉垃圾标签、集数等信息，提高匹配率
	keyword = utils.CleanMovieTitle(keyword)

	year := strings.TrimSpace(c.Query("year"))
	exclude := strings.TrimSpace(c.Query("exclude"))
	bypass := c.Query("bypass") == "1" // 隐藏参数：跳过版权过滤

	if keyword == "" {
		c.String(http.StatusBadRequest, "搜索关键词不能为空")
		return
	}

	// 使用 SearchService 搜索
	result, err := h.SearchService.Search(c.Request.Context(), keyword, bypass)
	if err != nil {
		log.Printf("[SearchHTMX] 搜索失败: %v", err)
		c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
			"Results": nil,
			"Error":   "搜索服务暂时不可用",
		})
		return
	}

	// 结果过滤
	finalResults := result.Items
	if (year != "" || exclude != "") && len(finalResults) > 0 {
		var filtered []model.VodItem
		for _, item := range finalResults {
			// 1. 排除指定的资源项 (用于播放页排除当前的播放项)
			if exclude != "" && (item.SourceKey+":"+item.VodId) == exclude {
				continue
			}

			// 2. 年份过滤
			// 如果结果中包含目标年份，或者结果中完全没有年份信息，则保留
			if year != "" && item.VodYear != "" && !strings.Contains(item.VodYear, year) {
				continue
			}

			filtered = append(filtered, item)
		}
		finalResults = filtered
	}

	// 仅当有结果时记录搜索日志
	if len(finalResults) > 0 {
		go func() {
			_ = h.Repos.SearchLog.Log(keyword, middleware.GetUserIDPtr(c), utils.HashIP(c.ClientIP()))
		}()
	}

	// 返回结果片段
	c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
		"Results":       finalResults,
		"FilteredCount": result.FilteredCount,
	})
}

// SimilarMoviesHTMX 相似电影推荐（htmx 片段）
// GET /api/htmx/similar?douban_id=xxx
func (h *Handler) SimilarMoviesHTMX(c *gin.Context) {
	doubanID := strings.TrimSpace(c.Query("douban_id"))
	if doubanID == "" {
		c.String(http.StatusBadRequest, "豆瓣 ID 不能为空")
		return
	}

	// 检查缓存
	cacheKey := fmt.Sprintf("similar_movies:%s", doubanID)
	if cached, found := utils.CacheGet(cacheKey); found {
		if movies, ok := cached.([]model.Movie); ok {
			c.HTML(http.StatusOK, "partials/similar_movies.html", gin.H{
				"Movies": movies,
			})
			return
		}
	}

	// 查询相似电影
	movies, err := h.Repos.Movie.FindSimilar(doubanID, 12)
	if err != nil {
		log.Printf("[SimilarMoviesHTMX] 查询相似电影失败: %v", err)
		c.HTML(http.StatusOK, "partials/similar_movies.html", gin.H{
			"Movies": nil,
		})
		return
	}

	// 缓存结果，过期时间 1 小时
	utils.CacheSet(cacheKey, movies, 1*time.Hour)

	c.HTML(http.StatusOK, "partials/similar_movies.html", gin.H{
		"Movies": movies,
	})
}

// ForYouHTMX 为你推荐（htmx 片段）
// GET /api/htmx/foryou
func (h *Handler) ForYouHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)

	// 未登录用户
	if userID == 0 {
		c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{
			"NeedLogin": true,
		})
		return
	}

	// 检查缓存
	cacheKey := fmt.Sprintf("foryou:%d", userID)
	if cached, found := utils.CacheGet(cacheKey); found {
		if movies, ok := cached.([]model.Movie); ok {
			c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{
				"Movies": movies,
			})
			return
		}
	}

	// 获取个性化推荐
	movies, err := h.Repos.Movie.GetUserRecommendations(userID, 24)
	if err != nil {
		log.Printf("[ForYouHTMX] 获取推荐失败: %v", err)
	}

	// 如果没有推荐结果，尝试获取热门电影作为降级
	if len(movies) == 0 {
		movies, _ = h.Repos.Movie.GetPopularMovies(24)
		if len(movies) == 0 {
			c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{
				"NoData": true,
			})
			return
		}
	}

	// 缓存 6 小时
	utils.CacheSet(cacheKey, movies, 6*time.Hour)

	c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{
		"Movies": movies,
	})
}

// ReviewsHTMX 豆瓣短评（htmx 片段）
// GET /api/htmx/reviews?douban_id=xxx
func (h *Handler) ReviewsHTMX(c *gin.Context) {
	doubanID := strings.TrimSpace(c.Query("douban_id"))
	if doubanID == "" {
		c.String(http.StatusBadRequest, "豆瓣 ID 不能为空")
		return
	}

	// 获取短评
	reviews, err := h.DoubanCrawler.GetReviews(doubanID)
	if err != nil {
		log.Printf("[ReviewsHTMX] 获取短评失败 (豆瓣ID: %s): %v", doubanID, err)
		c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
			"Reviews": nil,
			"Error":   "暂时无法获取短评",
		})
		return
	}

	c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
		"Reviews": reviews,
	})
}

// FeedbackListHTMX 反馈列表（htmx 片段，分页）
// GET /api/htmx/feedback-list?page=1
func (h *Handler) FeedbackListHTMX(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	const pageSize = 10
	offset := (page - 1) * pageSize

	// 获取反馈列表
	feedbacks, err := h.Repos.Feedback.ListPublic(pageSize, offset)
	if err != nil {
		log.Printf("[FeedbackListHTMX] 获取反馈列表失败: %v", err)
		feedbacks = []*model.Feedback{}
	}

	// 获取总数用于判断是否有更多
	total, _ := h.Repos.Feedback.CountPublic()
	hasMore := int64(page*pageSize) < total

	c.HTML(http.StatusOK, "partials/feedback_list.html", gin.H{
		"Feedbacks":   feedbacks,
		"HasMore":     hasMore,
		"NextPage":    page + 1,
		"IsFirstPage": page == 1,
	})
}
