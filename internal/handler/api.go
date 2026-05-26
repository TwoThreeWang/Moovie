package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
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
		utils.GoSafe(5*time.Second, func(ctx context.Context) {
			if err := h.Repos.SearchLog.Log(keyword, userID, ipHash); err != nil {
				log.Printf("[SearchHTMX] 记录搜索日志失败: %v", err)
			}
		})
	}

	// 处理过滤参数：年份和 exclude
	year := c.Query("year")
	exclude := c.Query("exclude")

	// 解析 exclude 参数（格式：sourceKey:vodID）
	var excludeSourceKey, excludeVodID string
	if exclude != "" {
		parts := strings.Split(exclude, ":")
		if len(parts) == 2 {
			excludeSourceKey = parts[0]
			excludeVodID = parts[1]
		}
	}

	// 只遍历一次，同时应用年份和 exclude 过滤
	filteredItems := make([]model.VodItem, 0)
	for _, item := range results.Items {
		// 年份过滤：如果指定了年份且当前项年份不匹配，则跳过
		if year != "" && item.VodYear != year && item.VodYear != "" {
			continue
		}
		// exclude 过滤：如果当前项与 exclude 参数匹配，则跳过
		if excludeSourceKey != "" && item.SourceKey == excludeSourceKey && item.VodId == excludeVodID {
			continue
		}
		// 通过所有过滤条件，添加到结果中
		filteredItems = append(filteredItems, item)
	}

	// 创建一个新的 SearchResult 结构体，避免修改缓存中的 Items 原始切片
	finalResults := &service.SearchResult{
		Items:         filteredItems,
		FilteredCount: results.FilteredCount,
	}

	h.renderSearchResults(c, finalResults, page, pageSize)
}

// renderSearchResults 渲染搜索结果（带分页）
func (h *Handler) renderSearchResults(c *gin.Context, results *service.SearchResult, page, pageSize int) {
	totalCount := len(results.Items)
	start := (page - 1) * pageSize

	if start >= totalCount {
		c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
			"Results":       []model.VodItem{},
			"FilteredCount": results.FilteredCount,
			"Year":          "",
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
	year := c.Query("year")
	bypass := c.Query("bypass") == "1"

	c.HTML(http.StatusOK, "partials/search_results.html", gin.H{
		"Results":       results.Items[start:end],
		"FilteredCount": results.FilteredCount,
		"Keyword":       keyword,
		"Year":          year,
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
	// 1. Referer 验证 (防盗链)
	referer := c.GetHeader("Referer")
	if referer != "" {
		if parsedSite, err := url.Parse(h.Config.SiteUrl); err == nil {
			siteHost := parsedSite.Host
			if !strings.Contains(referer, siteHost) && !strings.Contains(referer, "localhost") && !strings.Contains(referer, "127.0.0.1") {
				c.Status(http.StatusForbidden)
				return
			}
		}
	}

	// 2. 获取参数并解码
	targetURL, err := utils.DecodeProxyImageURL(c.Param("url"))
	if err != nil {
		utils.BadRequest(c, "非法的图片代理链接")
		return
	}

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		utils.InternalServerError(c, "创建请求失败")
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://movie.douban.com/")

	resp, err := utils.GlobalHttpClient.Do(req)
	if err != nil {
		utils.InternalServerError(c, "请求图片失败")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.Status(resp.StatusCode)
		return
	}

	// 设置浏览器缓存，缓存时间为 30 天 (2592000 秒)
	c.Header("Cache-Control", "public, max-age=2592000")
	c.Header("Expires", time.Now().AddDate(0, 0, 30).Format(http.TimeFormat))

	// 直接透传原始数据，不再进行解码和转码，彻底消除内存占用
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
		c.HTML(http.StatusOK, "partials/foryou_movies_grid.html", gin.H{
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
			utils.GoSafe(15*time.Second, func(ctx context.Context) {
				log.Printf("[ReviewsHTMX] 数据过期，静默更新 (豆瓣ID: %s)", doubanID)
				h.DoubanCrawler.GetReviewsApi(ctx, doubanID)
			})

			// 返回旧数据
			c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
				"Reviews": reviews,
			})
			return
		}
	}

	// 2. 如果库中没有或数据损坏，异步抓取
	utils.GoSafe(15*time.Second, func(ctx context.Context) {
		log.Printf("[ReviewsHTMX] 库中无数据，启动采集 (豆瓣ID: %s)", doubanID)
		h.DoubanCrawler.GetReviewsApi(ctx, doubanID)
	})

	// 返回加载中状态
	c.HTML(http.StatusOK, "partials/reviews.html", gin.H{
		"Reviews": nil,
		"Message": "正在从豆瓣采集精彩短评...",
	})
}

// FeedbackListHTMX 反馈列表（htmx 片段，分页，支持按类型筛选）
func (h *Handler) FeedbackListHTMX(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	feedbackType := c.DefaultQuery("type", "")

	const pageSize = 10
	offset := (page - 1) * pageSize

	feedbacks, err := h.Repos.Feedback.ListPublic(feedbackType, pageSize, offset)
	if err != nil {
		log.Printf("[FeedbackListHTMX] 获取反馈列表失败: %v", err)
		feedbacks = []*model.Feedback{}
	}

	total, _ := h.Repos.Feedback.CountPublic(feedbackType)
	hasMore := int64(page*pageSize) < total

	c.HTML(http.StatusOK, "partials/feedback_list.html", gin.H{
		"Feedbacks":   feedbacks,
		"HasMore":     hasMore,
		"NextPage":    page + 1,
		"IsFirstPage": page == 1,
		"Type":        feedbackType,
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

func (h *Handler) MovieBackdropsHTMX(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" {
		c.String(http.StatusOK, "")
		return
	}

	movie, err := h.Repos.Movie.FindByDoubanID(doubanID)
	if err != nil || movie == nil {
		c.String(http.StatusOK, "")
		return
	}

	// 如果剧照为空，触发异步同步
	if movie.Backdrops == "" && h.TMDBService != nil {
		h.TMDBService.SyncMovieSafeAsync(doubanID)
		c.String(http.StatusOK, `<div class="reviews-empty">正在后台采集精彩剧照...</div>`)
		return
	}

	// 将逗号分隔的剧照转为数组
	var backdrops []string
	if movie.Backdrops != "" {
		backdrops = strings.Split(movie.Backdrops, ",")
	}

	c.HTML(http.StatusOK, "partials/movie_backdrops.html", gin.H{
		"Backdrops": backdrops,
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

// DashboardFeedbackHTMX 用户中心 - 我的反馈
func (h *Handler) DashboardFeedbackHTMX(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}

	pageStr := c.DefaultQuery("page", "1")
	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	const pageSize = 10
	offset := (page - 1) * pageSize

	feedbacks, err := h.Repos.Feedback.ListByUserID(userID, pageSize, offset)
	if err != nil {
		log.Printf("[DashboardFeedbackHTMX] 获取用户反馈失败: %v", err)
		feedbacks = []*model.Feedback{}
	}

	total, _ := h.Repos.Feedback.CountByUserID(userID)
	hasMore := int64(page*pageSize) < total

	c.HTML(http.StatusOK, "partials/dashboard_feedback.html", gin.H{
		"Feedbacks":   feedbacks,
		"HasMore":     hasMore,
		"NextPage":    page + 1,
		"IsFirstPage": page == 1,
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

// DoubanCardHTMX 搜索页豆瓣电影卡片 (本地库模糊搜索，取第一条)
// GET /api/htmx/douban-card?kw=xxx
func (h *Handler) DoubanCardHTMX(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("kw"))
	if keyword == "" {
		c.String(http.StatusOK, "")
		return
	}

	movie, err := h.Repos.Movie.FuzzySearchFirst(keyword)
	if err != nil {
		log.Printf("[DoubanCardHTMX] 查询失败: %v", err)
		c.String(http.StatusOK, "")
		return
	}
	if movie == nil {
		// 本地无匹配数据，不展示卡片
		c.String(http.StatusOK, "")
		return
	}

	// 解析导演 JSON -> 名字列表（最多取3位）
	type Person struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var directors []Person
	_ = json.Unmarshal([]byte(movie.Directors), &directors)
	directorNames := make([]string, 0, 3)
	for i, d := range directors {
		if i >= 3 {
			break
		}
		directorNames = append(directorNames, d.Name)
	}

	// 解析演员 JSON -> 名字列表（最多取4位）
	var actors []Person
	_ = json.Unmarshal([]byte(movie.Actors), &actors)
	actorNames := make([]string, 0, 4)
	for i, a := range actors {
		if i >= 4 {
			break
		}
		actorNames = append(actorNames, a.Name)
	}

	// 简介截断（最多 120 字）
	summary := []rune(strings.TrimSpace(movie.Summary))
	summaryShort := string(summary)
	if len(summary) > 120 {
		summaryShort = string(summary[:120]) + "..."
	}

	// 类型 / 国家 拆分为 slice（逗号分隔）
	genres := splitTrimmed(movie.Genres)
	countries := splitTrimmed(movie.Countries)

	c.HTML(http.StatusOK, "partials/douban_card.html", gin.H{
		"Movie":         movie,
		"DirectorNames": directorNames,
		"ActorNames":    actorNames,
		"SummaryShort":  summaryShort,
		"Genres":        genres,
		"Countries":     countries,
	})
}

// TVBoxConfig TVBox 配置接口，直接填入 TVBox 配置地址即可使用
func (h *Handler) TVBoxConfig(c *gin.Context) {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, c.Request.Host)

	c.JSON(http.StatusOK, gin.H{
		"sites": []gin.H{
			{
				"key":         "moovie",
				"name":        "Moovie 影牛",
				"type":        1,
				"api":         baseURL + "/api/vod",
				"searchable":  1,
				"quickSearch": 1,
				"filterable":  0,
			},
		},
		"lives":  []gin.H{},
		"parses": []gin.H{},
		"flags":  []string{},
	})
}

// TVBoxVodAPI TVBox 数据接口（兼容苹果CMS v10）
func (h *Handler) TVBoxVodAPI(c *gin.Context) {
	ac := c.Query("ac")
	ids := c.Query("ids")
	wd := c.Query("wd")
	t := c.Query("t")
	pg, _ := strconv.Atoi(c.DefaultQuery("pg", "1"))
	if pg < 1 {
		pg = 1
	}

	// ac=detail&ids=xxx 获取详情（支持逗号分隔的多个 ID）
	if ids != "" {
		h.tvboxDetail(c, ids)
		return
	}

	// wd=xxx 搜索（TVBox 快搜会带 ac=detail&wd=xxx）
	if wd != "" {
		h.tvboxSearch(c, wd, pg)
		return
	}

	// ac=detail&t=xxx 按分类浏览
	if ac == "detail" && t != "" {
		h.tvboxCategory(c, t, pg)
		return
	}

	// 无参数：首屏初始化（返回分类列表 + 热门推荐）
	h.tvboxHome(c, pg)
}

// tvboxEncodeID 将 sourceKey:vodId 编码为纯数字 ID（供 TVBox 使用）
func tvboxEncodeID(sourceKey, vodId string) string {
	// 使用 FNV-1a 哈希生成稳定的数字 ID
	hash := uint32(2166136261)
	for _, b := range []byte(sourceKey + ":" + vodId) {
		hash ^= uint32(b)
		hash *= 16777619
	}
	return strconv.FormatUint(uint64(hash), 10)
}

// tvboxDecodeID 从纯数字 ID 解码回 sourceKey:vodId（需要数据库查询）
// TVBox 传回的 ids 参数就是原始的 sourceKey:vodId 字符串，不需要解码
// 此函数仅用于兼容 TVBox 的 vod_id 类型要求

// tvboxBuildVod 构建 TVBox 兼容的 vod 字段 map
func (h *Handler) tvboxBuildVod(vodId string, item *model.VodItem) gin.H {
	playFrom, playURL := h.formatTVBoxPlayUrl(item.VodPlayUrl)
	return gin.H{
		"vod_id":        vodId,
		"type_id":       tvboxTypeNameToID(item.TypeName),
		"type_name":     item.TypeName,
		"vod_name":      item.VodName,
		"vod_pic":       item.VodPic,
		"vod_lang":      item.VodLang,
		"vod_area":      item.VodArea,
		"vod_year":      item.VodYear,
		"vod_remarks":   item.VodRemarks,
		"vod_actor":     item.VodActor,
		"vod_director":  item.VodDirector,
		"vod_content":   item.VodContent,
		"vod_blurb":     item.VodBlurb,
		"vod_tag":       item.VodTag,
		"vod_time":      item.VodTime,
		"vod_play_from": playFrom,
		"vod_play_url":  playURL,
	}
}

// tvboxSearch 搜索接口
func (h *Handler) tvboxSearch(c *gin.Context, keyword string, page int) {
	items, err := h.Repos.VodItem.Search(keyword)
	if err != nil {
		log.Printf("[TVBox] 搜索失败: %v", err)
	}

	list := make([]gin.H, 0, len(items))
	for _, item := range items {
		list = append(list, h.tvboxBuildVod(item.SourceKey+":"+item.VodId, &item))
	}

	total := len(list)
	pageSize := 20
	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"code":      1,
		"msg":       "数据列表",
		"page":      page,
		"pagecount": totalPages,
		"limit":     strconv.Itoa(pageSize),
		"total":     total,
		"list":      list[start:end],
	})
}

// tvboxDetail 详情接口
func (h *Handler) tvboxDetail(c *gin.Context, ids string) {

	// 测试端点：返回 TVBox 源码中的官方测试数据格式
	if ids == "test" {
		c.JSON(http.StatusOK, gin.H{
			"code":      1,
			"msg":       "数据列表",
			"page":      1,
			"pagecount": 1,
			"limit":     "20",
			"total":     1,
			"list": []gin.H{
				{
					"vod_id":        "test",
					"type_id":       1,
					"type_name":     "电影",
					"vod_name":      "TVBox格式测试",
					"vod_pic":       "https://img9.doubanio.com/view/photo/s_ratio_poster/public/p2656327176.webp",
					"vod_lang":      "国语",
					"vod_area":      "中国大陆",
					"vod_year":      "2024",
					"vod_remarks":   "测试",
					"vod_actor":     "测试演员",
					"vod_director":  "测试导演",
					"vod_content":   "这是一个TVBox格式测试视频",
					"vod_blurb":     "测试简介",
					"vod_tag":       "测试",
					"vod_time":      "2024-01-01 00:00:00",
					"vod_play_from": "测试源",
					"vod_play_url":  "第01集$https://test-stream.m3u8",
				},
			},
		})
		return
	}

	parts := strings.SplitN(ids, ":", 2)
	if len(parts) != 2 {
		log.Printf("[TVBox] 无法解析 ID: %s", ids)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无效的ID", "list": []gin.H{}})
		return
	}
	prefix, vodId := parts[0], parts[1]

	// 豆瓣条目：用标题搜索资源网
	if prefix == "douban" {
		h.tvboxDetailFromDouban(c, vodId)
		return
	}
	sourceKey := prefix

	// 1. 获取详情
	detail, err := h.SearchService.GetDetail(c.Request.Context(), sourceKey, vodId)
	if err != nil || detail == nil {
		log.Printf("[TVBox] 详情查询失败: %v", err)
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到内容", "list": []gin.H{}})
		return
	}

	// 2. 检查播放链接，如果为空，尝试强制同步抓取一次
	if detail.VodPlayUrl == "" {
		site, _ := h.Repos.Site.FindByKey(sourceKey)
		if site != nil {
			crawler := h.SearchService.GetSearchCrawler()
			if d, err := crawler.GetDetail(c.Request.Context(), site.BaseUrl, vodId, sourceKey); err == nil && d != nil {
				detail = d
				_ = h.Repos.VodItem.Upsert(d)
			}
		}
	}

	// 3. 格式化播放源
	_, playURL := h.formatTVBoxPlayUrl(detail.VodPlayUrl)
	if playURL == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "无播放链接", "list": []gin.H{}})
		return
	}

	vod := h.tvboxBuildVod(ids, detail)

	c.JSON(http.StatusOK, gin.H{
		"code":      1,
		"msg":       "数据列表",
		"page":      1,
		"pagecount": 1,
		"limit":     "20",
		"total":     1,
		"list":      []gin.H{vod},
	})
}

// tvboxDetailFromDouban 豆瓣条目详情：搜索资源网获取播放链接
func (h *Handler) tvboxDetailFromDouban(c *gin.Context, doubanID string) {
		log.Printf("[TVBox] 豆瓣条目详情: %s", doubanID)

	// 1. 先用 douban_id 精确匹配资源网数据
	items, _ := h.Repos.VodItem.SearchByDoubanID(doubanID)
	if len(items) > 0 {
		for _, item := range items {
			if _, playURL := h.formatTVBoxPlayUrl(item.VodPlayUrl); playURL != "" {
				vod := h.tvboxBuildVod(item.SourceKey+":"+item.VodId, &item)
				c.JSON(http.StatusOK, gin.H{
					"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": 1,
					"list": []gin.H{vod},
				})
				return
			}
		}
	}

	// 2. 用标题模糊搜索
	movie, _ := h.Repos.Movie.FindByDoubanID(doubanID)
	if movie != nil {
		items, _ = h.Repos.VodItem.Search(movie.Title)
		for _, item := range items {
			if _, playURL := h.formatTVBoxPlayUrl(item.VodPlayUrl); playURL != "" {
				vod := h.tvboxBuildVod(item.SourceKey+":"+item.VodId, &item)
				c.JSON(http.StatusOK, gin.H{
					"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": 1,
					"list": []gin.H{vod},
				})
				return
			}
		}
	}

	log.Printf("[TVBox] 豆瓣 %s 未找到可播放资源", doubanID)
	c.JSON(http.StatusOK, gin.H{"code": 1, "msg": "未找到播放源", "list": []gin.H{}})
}

// formatTVBoxPlayUrl 将内部播放链接格式转为 TVBox 格式
// 内部格式: Source1$url1#url2$$$Source2$url3#url4
// TVBox 需要 vod_play_from: "源1$$$源2"  vod_play_url: "集1$url1#集2$url2$$$集1$url3"
func (h *Handler) formatTVBoxPlayUrl(rawPlayURL string) (string, string) {
	sources := utils.ParsePlayUrl(rawPlayURL)
	if len(sources) == 0 {
		return "", ""
	}

	var names []string
	var urls []string

	for _, src := range sources {
		name := src.Name
		if name == "" {
			name = "默认源"
		}
		names = append(names, name)

		var eps []string
		for _, ep := range src.Episodes {
			eps = append(eps, ep.Title+"$"+ep.URL)
		}
		urls = append(urls, strings.Join(eps, "#"))
	}

	return strings.Join(names, "$$$"), strings.Join(urls, "$$$")
}

// 固定分类（精确匹配数据库中的 type_name）
var tvboxCategories = []gin.H{
	{"type_id": 1, "type_name": "电影"},
	{"type_id": 2, "type_name": "电视剧"},
	{"type_id": 3, "type_name": "综艺"},
	{"type_id": 4, "type_name": "动漫"},
}

var tvboxTypeIDMap = map[int]string{
	1: "电影",
	2: "电视剧",
	3: "综艺",
	4: "动漫",
}

// tvboxTypeNameToID 类型名转 ID
func tvboxTypeNameToID(typeName string) int {
	switch typeName {
	case "电影":
		return 1
	case "电视剧":
		return 2
	case "综艺":
		return 3
	case "动漫":
		return 4
	default:
		return 0
	}
}

// tvboxTypeIDToDoubanType 分类 ID 映射到豆瓣 API 类型
var tvboxTypeIDToDoubanType = map[int]string{
	1: "movie",
	2: "tv",
	3: "show",
	4: "cartoon",
}

// tvboxDoubanToVod 将豆瓣热门条目转为 TVBox vod map，补全图片代理的绝对路径
func (h *Handler) tvboxDoubanToVod(c *gin.Context, subject service.DoubanPopularSubject) gin.H {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	baseURL := fmt.Sprintf("%s://%s", scheme, c.Request.Host)

	pic := subject.Cover
	if strings.HasPrefix(pic, "/") {
		pic = baseURL + pic
	}

	return gin.H{
		"vod_id":        "douban:" + subject.ID,
		"type_id":       0,
		"type_name":     "",
		"vod_name":      subject.Title,
		"vod_pic":       pic,
		"vod_lang":      "",
		"vod_area":      "",
		"vod_year":      "",
		"vod_remarks":   subject.EpisodesInfo,
		"vod_actor":     "",
		"vod_director":  "",
		"vod_content":   "",
		"vod_blurb":     "",
		"vod_tag":       "",
		"vod_time":      "",
		"vod_play_from": "",
		"vod_play_url":  "",
	}
}

// tvboxHome 首屏初始化（无参数请求）
func (h *Handler) tvboxHome(c *gin.Context, page int) {
	// 首屏默认展示豆瓣热门电影
	subjects, err := h.DoubanCrawler.GetPopularSubjects("movie")
	if err != nil {
		log.Printf("[TVBox] 豆瓣热门获取失败: %v", err)
	}

	list := make([]gin.H, 0, len(subjects))
	for _, s := range subjects {
		list = append(list, h.tvboxDoubanToVod(c, s))
	}

	c.JSON(http.StatusOK, gin.H{
		"code":      1,
		"msg":       "数据列表",
		"page":      1,
		"pagecount": 1,
		"limit":     "20",
		"total":     len(list),
		"list":      list,
		"class":     tvboxCategories,
	})
}

// tvboxCategory 按分类浏览（豆瓣热搜）
func (h *Handler) tvboxCategory(c *gin.Context, typeIDStr string, page int) {
	typeID, _ := strconv.Atoi(typeIDStr)
	doubanType := tvboxTypeIDToDoubanType[typeID]

	if doubanType == "" {
		c.JSON(http.StatusOK, gin.H{
			"code": 1, "msg": "数据列表", "page": 1, "pagecount": 1, "limit": "20", "total": 0, "list": []gin.H{},
		})
		return
	}

	subjects, err := h.DoubanCrawler.GetPopularSubjects(doubanType)
	if err != nil {
		log.Printf("[TVBox] 豆瓣分类获取失败 (%s): %v", doubanType, err)
	}

	list := make([]gin.H, 0, len(subjects))
	for _, s := range subjects {
		list = append(list, h.tvboxDoubanToVod(c, s))
	}

	c.JSON(http.StatusOK, gin.H{
		"code":      1,
		"msg":       "数据列表",
		"page":      1,
		"pagecount": 1,
		"limit":     "20",
		"total":     len(list),
		"list":      list,
	})
}

// splitTrimmed 按逗号拆分并去除空白
func splitTrimmed(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
