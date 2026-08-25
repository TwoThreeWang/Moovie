package recommendation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"github.com/gin-gonic/gin"
)

// Handler 提供相似影片页和「为你推荐」。
// 推荐计算比较重，Web 只读持久化快照，实际计算交给统一 Worker 队列。
type Handler struct {
	config    config.Config
	service   *Service
	snapshots *SnapshotStore
	queue     interface {
		Enqueue(context.Context, workqueue.Spec) (int, error)
	}
}

// NewHandler 创建推荐处理器。
func NewHandler(cfg config.Config, service *Service, snapshots *SnapshotStore) *Handler {
	return &Handler{config: cfg, service: service, snapshots: snapshots}
}

// WithRefreshQueue 注入统一 Worker 队列。
func (handler *Handler) WithRefreshQueue(queue interface {
	Enqueue(context.Context, workqueue.Spec) (int, error)
}) *Handler {
	handler.queue = queue
	return handler
}

// forYouData 是「为你推荐」页面的全部内容。
type forYouData struct {
	Personalized   []catalog.Movie `json:"personalized"`
	ReliveClassics []catalog.Movie `json:"relive_classics"`
	SimilarToLast  []catalog.Movie `json:"similar_to_last"`
	LastMovieTitle string          `json:"last_movie_title"`
	HeroMovie      *catalog.Movie  `json:"hero_movie"`
	NoPersonalData bool            `json:"no_personal_data"`
}

// Register 注册推荐相关路由。
func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/similar/:douban_id", handler.page)
	router.GET("/api/htmx/similar", handler.similar)
	router.GET("/api/htmx/similar-with-reason/:douban_id", handler.similarWithReasons)
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/foryou", optional, handler.forYouPage)
	router.GET("/recommend", optional, handler.forYouPage)
	router.GET("/api/htmx/foryou", optional, handler.forYou)
}

// forYouPage 渲染「为你推荐」页面骨架，内容由 HTMX 异步加载。
func (handler *Handler) forYouPage(c *gin.Context) {
	c.HTML(http.StatusOK, "foryou.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "为你推荐 - " + handler.config.SiteName}, nil))
}

// forYou 返回「为你推荐」的内容片段。
func (handler *Handler) forYou(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{"NeedLogin": true})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	data := handler.loadForYou(c.Request.Context(), userID)
	if data.HeroMovie == nil {
		c.HTML(http.StatusOK, "partials/foryou_movies.html", gin.H{"NoData": true})
		return
	}
	const pageSize = 12
	start, end := (page-1)*pageSize, page*pageSize
	if start >= len(data.Personalized) && page > 1 {
		c.String(http.StatusOK, "")
		return
	}
	if end > len(data.Personalized) {
		end = len(data.Personalized)
	}
	paged := data.Personalized[start:end]
	hasMore := end < len(data.Personalized)
	view := gin.H{"Personalized": paged, "HasMore": hasMore, "NextPage": page + 1, "IsFirstPage": page == 1,
		"ReliveClassics": data.ReliveClassics, "SimilarToLast": data.SimilarToLast, "LastMovieTitle": data.LastMovieTitle,
		"HeroMovie": data.HeroMovie, "NoPersonalData": data.NoPersonalData}
	if page > 1 {
		c.HTML(http.StatusOK, "partials/foryou_movies_grid.html", view)
		return
	}
	c.HTML(http.StatusOK, "partials/foryou_movies.html", view)
}

// loadForYou 只读持久化快照；过期时先返回旧数据并去重入队刷新。
func (handler *Handler) loadForYou(ctx context.Context, userID int) forYouData {
	if handler.snapshots == nil {
		return forYouData{NoPersonalData: true}
	}
	data, fresh, found, err := handler.snapshots.Load(ctx, userID)
	if err != nil {
		slog.Warn("load recommendation snapshot", "user_id", userID, "error", err)
	}
	if found {
		if !fresh {
			handler.enqueueRefresh(ctx, userID, "expired")
		}
		return data
	}
	handler.enqueueRefresh(ctx, userID, "missing")
	popular, err := handler.snapshots.PopularFallback(ctx, 60)
	if err != nil {
		slog.Warn("load recommendation popularity fallback", "user_id", userID, "error", err)
	}
	return fallbackForYou(popular)
}

func (handler *Handler) enqueueRefresh(ctx context.Context, userID int, reason string) {
	if handler.queue == nil {
		return
	}
	if _, err := handler.queue.Enqueue(ctx, workqueue.Spec{TaskType: TaskRefresh,
		SubjectKey: strconv.Itoa(userID), Payload: map[string]int{"user_id": userID}, Reason: reason, RequestedBy: userID, Priority: 10}); err != nil {
		slog.Warn("enqueue recommendation refresh", "user_id", userID, "error", err)
	}
}

// buildForYou 供 Worker 组装推荐内容，没有个人数据时退回评分热门影片。
func buildForYou(ctx context.Context, service *Service, userID int) forYouData {
	personalized, _ := service.UserRecommendations(ctx, userID, 60)
	hadPersonalData := len(personalized) > 0
	relive, _ := service.ReliveClassics(ctx, userID, 12)
	recent, lastTitle, _ := service.RecentSimilar(ctx, userID, 12)
	var hero *catalog.Movie
	if len(personalized) > 0 {
		hero = &personalized[0]
		personalized = personalized[1:]
	} else if len(relive) > 0 {
		hero = &relive[0]
	}
	if len(personalized) < 24 {
		popular, _ := service.Popular(ctx, 60)
		exists := make(map[int]bool)
		if hero != nil {
			exists[hero.ID] = true
		}
		for _, movie := range personalized {
			exists[movie.ID] = true
		}
		for _, movie := range popular {
			if !exists[movie.ID] {
				personalized = append(personalized, movie)
				exists[movie.ID] = true
			}
			if len(personalized) >= 60 {
				break
			}
		}
	}
	if hero == nil && len(personalized) > 0 {
		hero = &personalized[0]
		personalized = personalized[1:]
	}
	return forYouData{Personalized: personalized, ReliveClassics: relive, SimilarToLast: recent, LastMovieTitle: lastTitle, HeroMovie: hero, NoPersonalData: !hadPersonalData}
}

func fallbackForYou(popular []catalog.Movie) forYouData {
	if len(popular) == 0 {
		return forYouData{NoPersonalData: true}
	}
	hero := popular[0]
	return forYouData{Personalized: popular[1:], HeroMovie: &hero, NoPersonalData: true}
}

// compactForYouData 写快照前裁掉用不到的大字段（简介、向量文本），控制存储体积。
func compactForYouData(data forYouData) forYouData {
	data.Personalized = compactForYouMovies(data.Personalized)
	data.ReliveClassics = compactForYouMovies(data.ReliveClassics)
	data.SimilarToLast = compactForYouMovies(data.SimilarToLast)
	data.LastMovieTitle = compactRecommendationText(data.LastMovieTitle, 200)
	if data.HeroMovie != nil {
		movie := compactForYouMovie(*data.HeroMovie)
		data.HeroMovie = &movie
	}
	return data
}

// compactForYouMovies 批量裁剪。
func compactForYouMovies(movies []catalog.Movie) []catalog.Movie {
	result := make([]catalog.Movie, len(movies))
	for index, movie := range movies {
		result[index] = compactForYouMovie(movie)
	}
	return result
}

// compactForYouMovie 裁剪单部影片。
func compactForYouMovie(movie catalog.Movie) catalog.Movie {
	return catalog.Movie{
		ID: movie.ID, DoubanID: compactRecommendationText(movie.DoubanID, 32),
		Title: compactRecommendationText(movie.Title, 200), Year: compactRecommendationText(movie.Year, 16),
		Poster: compactRecommendationText(movie.Poster, 2048), Rating: movie.Rating,
		Genres: compactRecommendationText(movie.Genres, 200), Countries: compactRecommendationText(movie.Countries, 200),
		Summary: compactRecommendationText(movie.Summary, 600),
	}
}

// compactRecommendationText 截断长文本。
func compactRecommendationText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return strings.Clone(value)
}

// similar 返回相似影片片段。
func (handler *Handler) similar(c *gin.Context) {
	doubanID := c.Query("douban_id")
	if doubanID == "" {
		id, _ := strconv.Atoi(c.Query("id"))
		if movie, _ := handler.service.FindByID(c.Request.Context(), id); movie != nil {
			doubanID = movie.DoubanID
		}
	}
	movies, _ := handler.service.FindSimilar(c.Request.Context(), doubanID, 6)
	c.HTML(http.StatusOK, "partials/similar_movies.html", gin.H{"Movies": movies, "doubanID": doubanID})
}

// similarWithReasons 返回带推荐理由的相似影片片段。
func (handler *Handler) similarWithReasons(c *gin.Context) {
	doubanID := c.Param("douban_id")
	if doubanID == "" {
		c.String(http.StatusBadRequest, "豆瓣ID不能为空")
		return
	}
	movies, _, err := handler.service.FindSimilarWithReasons(c.Request.Context(), doubanID, 8)
	if err != nil {
		c.String(http.StatusInternalServerError, "获取相似电影失败")
		return
	}
	c.HTML(http.StatusOK, "partials/similar_movies_with_reasons.html", gin.H{"SimilarMovies": movies})
}

// page 渲染相似影片页。
func (handler *Handler) page(c *gin.Context) {
	doubanID := c.Param("douban_id")
	if doubanID == "" {
		handler.notFound(c, "页面未找到 - Moovie影牛")
		return
	}
	movies, source, err := handler.service.FindSimilarWithReasons(c.Request.Context(), doubanID, 12)
	if err != nil || source == nil {
		handler.notFound(c, "电影未找到 - "+handler.config.SiteName)
		return
	}
	directors := directorNames(source.Directors)
	primaryGenre := ""
	if parts := strings.Split(source.Genres, ","); len(parts) > 0 {
		primaryGenre = strings.TrimSpace(parts[0])
	}
	description := fmt.Sprintf("为您精选多部类似《%s》的电影。", source.Title)
	reason := "基于剧情内核"
	if directors != "" {
		reason += "、导演" + directors + "风格"
	}
	if primaryGenre != "" {
		reason += "及" + primaryGenre + "题材"
	}
	reason += "，结合向量相似度，为您推荐"
	if len(movies) > 1 {
		reason += fmt.Sprintf(" 《%s》、《%s》等高相关佳作。", movies[0].Movie.Title, movies[1].Movie.Title)
	} else if len(movies) == 1 {
		reason += fmt.Sprintf(" 《%s》等高相关佳作。", movies[0].Movie.Title)
	}
	description = strings.TrimSpace(description + " " + reason)
	title := fmt.Sprintf("类似《%s》的电影推荐_和《%s》差不多的电影 - %s", source.Title, source.Title, handler.config.SiteName)
	c.HTML(http.StatusOK, "recommendations.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: title, Description: description, Canonical: fmt.Sprintf("%s/similar/%s", handler.config.SiteURL, source.DoubanID)}, gin.H{"SourceMovie": source, "SimilarMovies": movies, "PrimaryGenre": primaryGenre}))
}

// directorNames 截取前几位导演用于页面展示。
func directorNames(value string) string {
	var directors []catalog.Director
	if json.Unmarshal([]byte(value), &directors) != nil {
		return ""
	}
	names := make([]string, 0, len(directors))
	for _, director := range directors {
		if director.Name != "" {
			names = append(names, director.Name)
		}
	}
	return strings.Join(names, "、")
}

// notFound 渲染 404。
func (handler *Handler) notFound(c *gin.Context, title string) {
	c.HTML(http.StatusNotFound, "404.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: title}, nil))
}
