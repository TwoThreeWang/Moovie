package social

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

const (
	weeklyFilmLimit      = 6
	featuredCommentLimit = 6
	filmFriendLimit      = 5
)

type Handler struct {
	config config.Config
	store  Store
	now    func() time.Time
}

func NewHandler(cfg config.Config, store Store) *Handler {
	return &Handler{config: cfg, store: store, now: time.Now}
}

func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/cinema", optional, handler.cinema)
	router.GET("/api/htmx/movie-comments", optional, handler.movieComments)
	router.POST("/api/comments/:id/like", optional, handler.toggleLike)
	router.GET("/api/comments/:id/replies", optional, handler.replies)
	router.POST("/api/comments/:id/replies", optional, handler.createReply)
}

func (handler *Handler) cinema(c *gin.Context) {
	now := handler.now()
	weekStart := startOfWeek(now)
	weeklyFilms, err := handler.store.ListWeeklyFilms(c.Request.Context(), weekStart, weeklyFilmLimit)
	if err != nil {
		c.String(http.StatusInternalServerError, "片场暂时无法开场")
		return
	}
	comments, err := handler.store.ListFeaturedComments(c.Request.Context(), featuredCommentLimit)
	if err != nil {
		c.String(http.StatusInternalServerError, "片场暂时无法开场")
		return
	}
	currentUserID := auth.UserID(c)
	friends, err := handler.store.ListFilmFriends(c.Request.Context(), currentUserID, filmFriendLimit)
	if err != nil {
		c.String(http.StatusInternalServerError, "片场暂时无法开场")
		return
	}
	commentIDs := make([]int, 0, len(comments))
	for _, comment := range comments {
		commentIDs = append(commentIDs, comment.ID)
	}
	likeCounts, _ := handler.store.CountLikes(c.Request.Context(), commentIDs)
	replyCounts, _ := handler.store.CountReplies(c.Request.Context(), commentIDs)
	liked, _ := handler.store.LikedByUser(c.Request.Context(), commentIDs, currentUserID)

	c.HTML(http.StatusOK, "cinema.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       "片场 - " + handler.config.SiteName,
		Description: "从片友的放映单与短评里，遇见下一部电影。",
		Canonical:   platformweb.CanonicalURL(handler.config.SiteURL, "/cinema"),
	}, gin.H{
		"WeeklyFilms": weeklyFilms, "FeaturedComments": comments, "FilmFriends": friends,
		"LikeCounts": likeCounts, "ReplyCounts": replyCounts, "Liked": liked,
		"CurrentUserID": currentUserID, "WeekStart": weekStart, "WeekEnd": weekStart.AddDate(0, 0, 6),
	}))
}

func (handler *Handler) movieComments(c *gin.Context) {
	movieID := c.Query("douban_id")
	if movieID == "" {
		c.String(http.StatusOK, "")
		return
	}
	comments, err := handler.store.ListCommentsByMovie(c.Request.Context(), movieID, 10)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	ids := make([]int, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	likeCounts, _ := handler.store.CountLikes(c.Request.Context(), ids)
	replyCounts, _ := handler.store.CountReplies(c.Request.Context(), ids)
	liked, _ := handler.store.LikedByUser(c.Request.Context(), ids, auth.UserID(c))
	c.HTML(http.StatusOK, "partials/movie_user_comments.html", gin.H{
		"Comments": comments, "LikeCounts": likeCounts, "ReplyCounts": replyCounts,
		"Liked": liked, "CurrentUserID": auth.UserID(c),
	})
}

func (handler *Handler) toggleLike(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	count, liked, err := handler.store.ToggleLike(c.Request.Context(), userMovieID, userID)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	c.HTML(http.StatusOK, "partials/comment_like_button.html", gin.H{"UserMovieID": userMovieID, "LikeCount": count, "Liked": liked})
}

func (handler *Handler) replies(c *gin.Context) {
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	handler.renderReplies(c, userMovieID, auth.UserID(c))
}

func (handler *Handler) createReply(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	content := strings.TrimSpace(c.PostForm("content"))
	if content == "" {
		c.String(http.StatusBadRequest, "回复内容不能为空")
		return
	}
	characters := []rune(content)
	if len(characters) > 300 {
		content = string(characters[:300])
	}
	if _, err := handler.store.CreateReply(c.Request.Context(), userMovieID, userID, content); err != nil {
		c.String(http.StatusInternalServerError, "回复失败")
		return
	}
	handler.renderReplies(c, userMovieID, userID)
}

func (handler *Handler) renderReplies(c *gin.Context, userMovieID, userID int) {
	replies, err := handler.store.ListReplies(c.Request.Context(), userMovieID)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	c.HTML(http.StatusOK, "partials/comment_replies.html", gin.H{"UserMovieID": userMovieID, "Replies": replies, "CurrentUserID": userID})
}

func startOfWeek(value time.Time) time.Time {
	daysSinceMonday := (int(value.Weekday()) + 6) % 7
	year, month, day := value.Date()
	return time.Date(year, month, day-daysSinceMonday, 0, 0, 0, 0, value.Location())
}

func positiveID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}
