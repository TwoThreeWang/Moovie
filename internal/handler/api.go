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

func (h *Handler) MarkWish(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	doubanID := c.Param("id")
	title := c.Query("title")
	poster := c.Query("poster")
	year := c.Query("year")
	record := &model.UserMovie{
		UserID:  userID,
		MovieID: doubanID,
		Title:   title,
		Poster:  poster,
		Year:    year,
		Status:  "wish",
	}
	if err := h.Repos.UserMovie.Upsert(record); err != nil {
		c.String(http.StatusInternalServerError, "操作失败")
		return
	}
	isWish := true
	isWatched, _ := h.Repos.UserMovie.IsMarked(userID, doubanID, "watched")
	c.HTML(http.StatusOK, "partials/user_movie_buttons.html", gin.H{
		"DoubanID":  doubanID,
		"IsWish":    isWish,
		"IsWatched": isWatched,
		"Title":     title,
		"Poster":    poster,
		"Year":      year,
	})
}

func (h *Handler) MarkWatched(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	doubanID := c.Param("id")
	// 优先从 POST 表单获取，因为拟态框是 POST 提交
	title := c.PostForm("title")
	if title == "" {
		title = c.Query("title")
	}
	poster := c.PostForm("poster")
	if poster == "" {
		poster = c.Query("poster")
	}
	year := c.PostForm("year")
	if year == "" {
		year = c.Query("year")
	}
	// 兼容从表单或查询字符串传入评分与短评
	ratingStr := c.PostForm("rating")
	if ratingStr == "" {
		ratingStr = c.DefaultQuery("rating", "0")
	}
	rating, _ := strconv.Atoi(ratingStr)
	comment := c.PostForm("comment")
	if comment == "" {
		comment = c.Query("comment")
	}
	record := &model.UserMovie{
		UserID:  userID,
		MovieID: doubanID,
		Title:   title,
		Poster:  poster,
		Year:    year,
		Status:  "watched",
		Rating:  rating,
		Comment: comment,
	}
	if err := h.Repos.UserMovie.Upsert(record); err != nil {
		c.String(http.StatusInternalServerError, "操作失败")
		return
	}
	isWatched := true
	isWish, _ := h.Repos.UserMovie.IsMarked(userID, doubanID, "wish")
	c.HTML(http.StatusOK, "partials/user_movie_buttons.html", gin.H{
		"DoubanID":  doubanID,
		"IsWish":    isWish,
		"IsWatched": isWatched,
		"Title":     title,
		"Poster":    poster,
		"Year":      year,
		"Rating":    rating,
	})
}

// UserMovieMarkWatchedFormHTMX 标记“已看过”前的评分/短评表单
func (h *Handler) UserMovieMarkWatchedFormHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "")
		return
	}
	doubanID := c.Query("douban_id")
	title := c.Query("title")
	poster := c.Query("poster")
	year := c.Query("year")
	c.HTML(http.StatusOK, "partials/user_movie_mark_watched_form.html", gin.H{
		"DoubanID": doubanID,
		"Title":    title,
		"Poster":   poster,
		"Year":     year,
	})
}

// UserMovieButtonsHTMX 返回当前电影的操作按钮片段
func (h *Handler) UserMovieButtonsHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	doubanID := c.Query("douban_id")
	title := c.Query("title")
	poster := c.Query("poster")
	year := c.Query("year")
	isWish := false
	isWatched := false
	if userID > 0 && doubanID != "" {
		if rec, err := h.Repos.UserMovie.GetByUserAndMovie(userID, doubanID); err == nil && rec != nil {
			isWish = rec.Status == "wish"
			isWatched = rec.Status == "watched"
		}
	}
	c.HTML(http.StatusOK, "partials/user_movie_buttons.html", gin.H{
		"DoubanID":  doubanID,
		"IsWish":    isWish,
		"IsWatched": isWatched,
		"Title":     title,
		"Poster":    poster,
		"Year":      year,
	})
}

func (h *Handler) RemoveUserMovie(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	doubanID := c.Param("id")
	title := c.Query("title")
	poster := c.Query("poster")
	year := c.Query("year")
	if err := h.Repos.UserMovie.Remove(userID, doubanID); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	// 如果是从个人中心发起的删除，返回空字符串以移除 DOM
	if c.Query("source") == "dashboard" {
		c.String(http.StatusOK, "")
		return
	}

	// 返回最新的按钮片段（未标记状态）
	c.HTML(http.StatusOK, "partials/user_movie_buttons.html", gin.H{
		"DoubanID":  doubanID,
		"IsWish":    false,
		"IsWatched": false,
		"Title":     title,
		"Poster":    poster,
		"Year":      year,
	})
}

// SubmitFeedback 提交反馈
func (h *Handler) SubmitFeedback(c *gin.Context) {
	userID := middleware.GetUserIDPtr(c)
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
	if userID != nil {
		feedback.UserID = userID
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

	// 获取分页参数
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "12"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 12
	}

	// 生成缓存key
	cacheKey := h.generateSearchCacheKey(keyword, bypass)

	// 获取搜索结果（优先缓存）
	var results *service.SearchResult
	if cached, found := h.SearchCache.Get(cacheKey); found {
		results = &cached
	} else {
		// 缓存未命中，走原有搜索逻辑
		searchResults, err := h.SearchService.Search(c.Request.Context(), keyword, bypass)
		if err != nil {
			log.Printf("搜索失败: %v", err)
		}
		results = searchResults
		// 存入缓存
		h.SearchCache.Set(cacheKey, *searchResults)
	}

	// 只要有查询结果就记录搜索日志
	if len(results.Items) > 0 {
		userID := middleware.GetUserIDPtr(c)
		ipHash := utils.HashIP(c.ClientIP())
		go func(kw string, uid *int, ip string) {
			if err := h.Repos.SearchLog.Log(kw, uid, ip); err != nil {
				log.Printf("[SearchHTMX] 记录搜索日志失败: %v", err)
			}
		}(keyword, userID, ipHash)
	}

	// 分页返回
	h.renderSearchResults(c, results, page, pageSize)
}

// renderSearchResults 渲染搜索结果（带分页）
func (h *Handler) renderSearchResults(c *gin.Context, results *service.SearchResult, page, pageSize int) {
	totalCount := len(results.Items)
	start := (page - 1) * pageSize

	if start >= totalCount {
		c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
			"Results":       []model.VodItem{},
			"FilteredCount": results.FilteredCount,
			"CurrentPage":   page,
			"HasNextPage":   false,
		})
		return
	}

	end := start + pageSize
	if end > totalCount {
		end = totalCount
	}

	// 获取查询参数用于模板
	keyword := c.Query("kw")
	bypass := c.Query("bypass") == "1"

	c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
		"Results":       results.Items[start:end],
		"FilteredCount": results.FilteredCount,
		"Keyword":       keyword,
		"CurrentPage":   page,
		"PrevPage":      page - 1,
		"NextPage":      page + 1,
		"HasNextPage":   end < totalCount,
		"Bypass":        bypass,
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

	movies, err := h.Repos.Movie.FindSimilar(doubanID, 6)
	if err != nil {
		log.Printf("获取相似电影失败: %v", err)
	}
	c.HTML(http.StatusOK, "partials/similar_movies.html", gin.H{
		"Movies":   movies,
		"doubanID": doubanID,
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
		c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{
			"NeedLogin": true,
		})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	pageSize := 12

	// 尝试从缓存获取
	cacheKey := "foryou_v3:" + strconv.Itoa(userID)
	var allData gin.H
	if cached, found := utils.CacheGet(cacheKey); found {
		if data, ok := cached.(gin.H); ok {
			allData = data
		}
	}

	if allData == nil {
		// 聚合推荐数据
		allData = gin.H{
			"UserID": userID,
		}

		// 1. 获取个性化推荐 (获取更多数据以便分页)
		personalized, _ := h.Repos.Movie.GetUserRecommendations(userID, 60)
		allData["Personalized"] = personalized

		// 2. 获取“重温经典”
		reliveClassics, _ := h.Repos.Movie.GetReliveClassics(userID, 12)
		allData["ReliveClassics"] = reliveClassics

		// 3. 获取“关联推荐”
		similarToLast, lastTitle, _ := h.Repos.Movie.GetRecentSimilarMovies(userID, 12)
		allData["SimilarToLast"] = similarToLast
		allData["LastMovieTitle"] = lastTitle

		// 4. 确定 Hero Movie 并整理列表
		var heroMovie *model.Movie
		if len(personalized) > 0 {
			// 也就是取第一个作为 Hero
			heroMovie = &personalized[0]
			// 从列表中移除 (避免重复显示)
			personalized = personalized[1:]
		} else if len(reliveClassics) > 0 {
			heroMovie = &reliveClassics[0]
		}

		// 如果推荐列表太短（少于 24 条），用热门电影补全
		if len(personalized) < 24 {
			popular, _ := h.Repos.Movie.GetPopularMovies(60)

			// 建立已存在 ID 的 map 用于去重
			exists := make(map[int]bool)
			if heroMovie != nil {
				exists[heroMovie.ID] = true
			}
			for _, m := range personalized {
				exists[m.ID] = true
			}

			for _, p := range popular {
				if !exists[p.ID] {
					personalized = append(personalized, p)
					exists[p.ID] = true
				}
				// 补够 60 条就停
				if len(personalized) >= 60 {
					break
				}
			}
		}

		// 如果还是没有 Hero (说明连热门都没有)，那就真没办法了
		if heroMovie == nil {
			if len(personalized) > 0 {
				heroMovie = &personalized[0]
				personalized = personalized[1:]
			} else {
				// 尝试再次降级（虽然理论上 GetPopularMovies 应该能拿到数据）
				popular, _ := h.Repos.Movie.GetPopularMovies(24)
				if len(popular) > 0 {
					heroMovie = &popular[0]
					// 剩下的放进列表
					if len(popular) > 1 {
						personalized = popular[1:]
					}
				}
			}
		}

		allData["HeroMovie"] = heroMovie
		allData["Personalized"] = personalized // 更新补全后的列表

		// 检查是否真的没有任何数据
		if heroMovie == nil {
			c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{"NoData": true})
			return
		}

		// 缓存数据
		utils.CacheSet(cacheKey, allData, 1*time.Hour)
	}

	// 处理分页
	personalized, ok := allData["Personalized"].([]model.Movie)
	if !ok {
		personalized = []model.Movie{}
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	totalCount := len(personalized)

	if start >= totalCount {
		c.String(http.StatusOK, "")
		return
	}
	if end > totalCount {
		end = totalCount
	}

	pagedPersonalized := personalized[start:end]
	hasMore := end < totalCount

	// 如果是 page > 1，只返回网格项片段
	if page > 1 {
		c.HTML(http.StatusOK, "foryou_movies_grid.html", gin.H{
			"Personalized": pagedPersonalized,
			"HasMore":      hasMore,
			"NextPage":     page + 1,
			"SectionType":  "personalized",
		})
		return
	}

	// 合并分页后的数据用于首屏渲染
	renderData := gin.H{}
	for k, v := range allData {
		renderData[k] = v
	}
	renderData["Personalized"] = pagedPersonalized
	renderData["HasMore"] = hasMore
	renderData["NextPage"] = page + 1
	renderData["IsFirstPage"] = true

	c.HTML(http.StatusOK, "partials/foryou_movies.html", renderData)
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
				h.DoubanCrawler.GetReviewsApi(doubanID)
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
		h.DoubanCrawler.GetReviewsApi(doubanID)
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

func (h *Handler) DashboardWishHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}
	records, err := h.Repos.UserMovie.ListByUser(userID, "wish", 10000, 0)
	if err != nil {
		log.Printf("[DashboardWishHTMX] 获取想看失败: %v", err)
	}
	count, _ := h.Repos.UserMovie.CountByUser(userID, "wish")
	c.HTML(http.StatusOK, "partials/dashboard_wish.html", gin.H{
		"Wish":      records,
		"WishCount": count,
	})
}

func (h *Handler) DashboardWatchedHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}
	records, err := h.Repos.UserMovie.ListByUser(userID, "watched", 10000, 0)
	if err != nil {
		log.Printf("[DashboardWatchedHTMX] 获取已看过失败: %v", err)
	}
	count, _ := h.Repos.UserMovie.CountByUser(userID, "watched")
	c.HTML(http.StatusOK, "partials/dashboard_watched.html", gin.H{
		"Watched":      records,
		"WatchedCount": count,
	})
}

func (h *Handler) MovieCommentsHTMX(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" {
		c.String(http.StatusOK, "")
		return
	}
	records, err := h.Repos.UserMovie.ListCommentsByMovie(doubanID, 10)
	if err != nil {
		log.Printf("[MovieCommentsHTMX] 获取评论失败: %v", err)
	}
	c.HTML(http.StatusOK, "partials/movie_user_comments.html", gin.H{
		"Comments": records,
	})
}

func (h *Handler) UserMovieEditFormHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "")
		return
	}
	id, _ := strconv.Atoi(c.Query("id"))
	rec, err := h.Repos.UserMovie.GetByID(userID, id)
	if err != nil || rec == nil {
		c.String(http.StatusOK, "")
		return
	}
	c.HTML(http.StatusOK, "partials/user_movie_edit_form.html", gin.H{
		"Record": rec,
	})
}

func (h *Handler) UpdateUserMovie(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	rating, _ := strconv.Atoi(c.DefaultPostForm("rating", "0"))
	comment := c.PostForm("comment")
	if rating < 0 {
		rating = 0
	}
	if rating > 5 {
		rating = 5
	}
	if err := h.Repos.UserMovie.UpdateRatingComment(userID, id, rating, comment); err != nil {
		c.String(http.StatusInternalServerError, "保存失败")
		return
	}

	// 重新获取完整记录并返回片段，以修复海报/标题丢失问题
	rec, err := h.Repos.UserMovie.GetByID(userID, id)
	if err != nil || rec == nil {
		c.String(http.StatusOK, "已保存")
		return
	}

	c.HTML(http.StatusOK, "partials/dashboard_watched_item.html", rec)
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

// GetLoadStats 获取视频加载统计信息
// GET /api/stats/load-speed?source_key=xxx&vod_id=xxx
func (h *Handler) GetLoadStats(c *gin.Context) {
	sourceKey := c.Query("source_key")
	vodID := c.Query("vod_id")

	if sourceKey == "" || vodID == "" {
		utils.BadRequest(c, "source_key 和 vod_id 不能为空")
		return
	}

	stats, err := h.Repos.Movie.GetLoadStatsBySource(sourceKey, vodID)
	if err != nil {
		log.Printf("[GetLoadStats] 获取加载统计失败: %v", err)
		utils.InternalServerError(c, "获取统计信息失败")
		return
	}

	utils.Success(c, stats)
}
