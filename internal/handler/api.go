package handler

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/service"
	"github.com/user/moovie/internal/utils"
)

// AddFavorite 添加收藏
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

// RemoveFavorite 移除收藏
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

	// 如果带有 hx-target 参数，说明是仪表盘删除操作，返回空（删除 DOM）
	if c.GetHeader("HX-Target") != "" {
		c.Status(http.StatusOK)
		return
	}

	// 返回未收藏状态的按钮
	c.HTML(http.StatusOK, "partials/favorite_btn.html", gin.H{
		"MovieID":     movieID,
		"IsFavorited": false,
	})
}

// SubmitFeedback 提交反馈
func (h *Handler) SubmitFeedback(c *gin.Context) {
	userID := middleware.GetUserID(c)
	content := c.PostForm("content")
	feedbackType := c.PostForm("type")
	movieURL := c.PostForm("movie_url")

	if content == "" {
		c.String(http.StatusBadRequest, `<div class="alert alert-error">请填写反馈内容</div>`)
		return
	}

	feedback := &model.Feedback{
		Content:  content,
		Type:     feedbackType,
		MovieURL: movieURL,
		Status:   "pending",
	}
	if userID > 0 {
		tmpID := userID
		feedback.UserID = &tmpID
	}

	if err := h.Repos.Feedback.Create(feedback); err != nil {
		c.String(http.StatusInternalServerError, `<div class="alert alert-error">提交失败，请重试</div>`)
		return
	}

	c.String(http.StatusOK, `<div class="alert alert-success">感谢您的反馈！我们会尽快处理。</div>`)
}

// RemoveHistory 删除历史记录
func (h *Handler) RemoveHistory(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.Repos.History.Delete(userID, id); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	// 如果是 htmx 请求，返回空字符串（以便前端删除 DOM）
	if c.GetHeader("HX-Request") == "true" {
		c.Status(http.StatusOK)
		return
	}

	utils.Success(c, nil)
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
		// 同步处理，确保观影记录成功保存
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

// SearchHTMX 搜索结果片段
func (h *Handler) SearchHTMX(c *gin.Context) {
	keyword := c.Query("kw")
	bypass := c.Query("bypass") == "1"
	if keyword == "" {
		c.String(http.StatusOK, "")
		return
	}
	results, err := h.SearchService.Search(c.Request.Context(), keyword, bypass)
	if err != nil {
		log.Printf("搜索失败: %v", err)
	}
	c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
		"Results":       results.Items,
		"FilteredCount": results.FilteredCount,
	})
}

// SimilarMoviesHTMX 相似电影推荐
func (h *Handler) SimilarMoviesHTMX(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" {
		id, _ := strconv.Atoi(c.Query("id"))
		if movie, _ := h.Repos.Movie.FindByID(id); movie != nil {
			doubanID = movie.DoubanID
		}
	}

	movies, err := h.Repos.Movie.FindSimilar(doubanID, 12)
	if err != nil {
		log.Printf("获取相似电影失败: %v", err)
	}
	c.HTML(http.StatusOK, "partials/similar_movies.html", gin.H{
		"Movies": movies,
	})
}

// MovieSuggest 搜索建议
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

// ProxyImage 豆瓣图片代理
func (h *Handler) ProxyImage(c *gin.Context) {
	targetURL := c.Query("url")
	if targetURL == "" {
		utils.BadRequest(c, "URL 不能为空")
		return
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		utils.InternalServerError(c, "创建请求失败")
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://movie.douban.com/")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		utils.InternalServerError(c, "请求图片失败")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		return
	}

	c.DataFromReader(http.StatusOK, resp.ContentLength, resp.Header.Get("Content-Type"), resp.Body, nil)
}

// ForYouHTMX 为你推荐（htmx 片段）
func (h *Handler) ForYouHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "")
		return
	}

	// 尝试从缓存获取
	cacheKey := "foryou:" + strconv.Itoa(userID)
	if cached, found := utils.CacheGet(cacheKey); found {
		if movies, ok := cached.([]model.Movie); ok {
			c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{
				"Movies": movies,
			})
			return
		}
	}

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

	// 1. 尝试从数据库获取
	movie, err := h.Repos.Movie.FindByDoubanID(doubanID)
	if err == nil && movie != nil && movie.ReviewsJSON != "" {
		// 检查是否过期 (3天)
		isExpired := time.Since(movie.ReviewsUpdatedAt) > 3*24*time.Hour

		var reviews []service.DoubanReview
		if json.Unmarshal([]byte(movie.ReviewsJSON), &reviews) == nil {
			// 如果数据有效
			if !isExpired {
				c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
					"Reviews": reviews,
				})
				return
			}

			// 如果过期，异步静默更新
			go func() {
				log.Printf("[ReviewsHTMX] 数据过期，静默更新 (豆瓣ID: %s)", doubanID)
				h.DoubanCrawler.GetReviews(doubanID)
			}()

			// 返回旧数据
			c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
				"Reviews": reviews,
			})
			return
		}
	}

	// 2. 如果库中没有或数据损坏，异步抓取
	go func() {
		log.Printf("[ReviewsHTMX] 库中无数据，启动采集 (豆瓣ID: %s)", doubanID)
		h.DoubanCrawler.GetReviews(doubanID)
	}()

	// 返回加载中状态
	c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
		"Reviews": nil,
		"Message": "正在从豆瓣采集精彩短评...",
	})
}

// FeedbackListHTMX 反馈列表（htmx 片段，分页）
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

// DashboardFavoritesHTMX 仪表盘收藏列表（htmx 片段）
func (h *Handler) DashboardFavoritesHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}

	// 使用 ListByUser
	favorites, err := h.Repos.Favorite.ListByUser(userID, 10000, 0)
	if err != nil {
		log.Printf("[DashboardFavoritesHTMX] 获取收藏失败: %v", err)
	}

	count, _ := h.Repos.Favorite.CountByUser(userID)

	c.HTML(http.StatusOK, "partials/dashboard_favorites.html", gin.H{
		"Favorites":     favorites,
		"FavoriteCount": count,
	})
}

// DashboardHistoryHTMX 仪表盘历史记录（htmx 片段）
func (h *Handler) DashboardHistoryHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}

	// 使用 ListByUser
	histories, err := h.Repos.History.ListByUser(userID, 10000, 0)
	if err != nil {
		log.Printf("[DashboardHistoryHTMX] 获取历史失败: %v", err)
	}

	c.HTML(http.StatusOK, "partials/dashboard_history.html", gin.H{
		"History": histories,
	})
}
