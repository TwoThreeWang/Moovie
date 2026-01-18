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
	movieID, _ := strconv.Atoi(c.Param("id"))
	if err := h.Repos.Favorite.Add(userID, movieID); err != nil {
		utils.InternalServerError(c, "收藏失败")
		return
	}
	utils.Success(c, nil)
}

// RemoveFavorite 移除收藏
func (h *Handler) RemoveFavorite(c *gin.Context) {
	userID := middleware.GetUserID(c)
	movieID, _ := strconv.Atoi(c.Param("id"))
	if err := h.Repos.Favorite.Remove(userID, movieID); err != nil {
		utils.InternalServerError(c, "移除失败")
		return
	}
	utils.Success(c, nil)
}

// SubmitFeedback 提交反馈
func (h *Handler) SubmitFeedback(c *gin.Context) {
	userID := middleware.GetUserID(c)
	content := c.PostForm("content")
	if content == "" {
		utils.BadRequest(c, "反馈内容不能为空")
		return
	}

	feedback := &model.Feedback{
		Content: content,
		Status:  "pending",
	}
	if userID > 0 {
		tmpID := userID
		feedback.UserID = &tmpID
	}

	if err := h.Repos.Feedback.Create(feedback); err != nil {
		utils.InternalServerError(c, "提交失败")
		return
	}
	utils.Success(c, nil)
}

// RemoveHistory 删除历史记录
func (h *Handler) RemoveHistory(c *gin.Context) {
	userID := middleware.GetUserID(c)
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.Repos.History.Delete(userID, id); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}
	utils.Success(c, nil)
}

// SyncHistory 同步历史记录
func (h *Handler) SyncHistory(c *gin.Context) {
	userID := middleware.GetUserID(c)
	var history model.WatchHistory
	if err := c.ShouldBindJSON(&history); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}
	history.UserID = userID
	history.WatchedAt = time.Now()
	if err := h.Repos.History.Upsert(&history); err != nil {
		utils.InternalServerError(c, "同步失败")
		return
	}
	utils.Success(c, nil)
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
	c.HTML(http.StatusOK, "partials/discover_grid.html", gin.H{
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
	favorites, err := h.Repos.Favorite.ListByUser(userID, 100, 0)
	if err != nil {
		log.Printf("[DashboardFavoritesHTMX] 获取收藏失败: %v", err)
	}

	c.HTML(http.StatusOK, "partials/dashboard_favorites.html", gin.H{
		"Favorites": favorites,
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
	histories, err := h.Repos.History.ListByUser(userID, 20, 0)
	if err != nil {
		log.Printf("[DashboardHistoryHTMX] 获取历史失败: %v", err)
	}

	c.HTML(http.StatusOK, "partials/dashboard_history.html", gin.H{
		"Histories": histories,
	})
}
