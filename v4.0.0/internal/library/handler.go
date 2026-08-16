package library

import (
	"net/http"
	"strconv"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/gin-gonic/gin"
)

const dashboardGridPageSize = 24

type Handler struct {
	store  Store
	secret string
}

func NewHandler(store Store, secret string) *Handler {
	return &Handler{store: store, secret: secret}
}

func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.secret)
	router.POST("/api/user-movies/:id/wish", optional, handler.markWish)
	router.POST("/api/user-movies/:id/watched", optional, handler.markWatched)
	router.DELETE("/api/user-movies/:id", optional, handler.remove)
	router.PATCH("/api/user-movies/:id", optional, handler.update)
	router.GET("/api/htmx/user-movie/edit", optional, handler.editForm)
	router.GET("/api/htmx/user-movie/mark-watched", optional, handler.markWatchedForm)
	router.GET("/api/htmx/user-movie/buttons", optional, handler.buttons)
	router.GET("/api/htmx/dashboard/wish", optional, handler.dashboardWish)
	router.GET("/api/htmx/dashboard/watched", optional, handler.dashboardWatched)
}

func (handler *Handler) markWish(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	movieID := c.Param("id")
	if err := handler.store.Upsert(c.Request.Context(), Record{
		UserID: userID, MovieID: movieID, Title: c.Query("title"), Poster: c.Query("poster"),
		Year: c.Query("year"), Status: StatusWish,
	}); err != nil {
		c.String(http.StatusInternalServerError, "操作失败")
		return
	}
	handler.renderButtons(c, movieID, true, false, true)
}

func (handler *Handler) markWatched(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	ratingText := formOrQuery(c, "rating")
	if ratingText == "" {
		ratingText = "0"
	}
	rating, _ := strconv.Atoi(ratingText)
	movieID := c.Param("id")
	if err := handler.store.Upsert(c.Request.Context(), Record{
		UserID: userID, MovieID: movieID, Title: formOrQuery(c, "title"), Poster: formOrQuery(c, "poster"),
		Year: formOrQuery(c, "year"), Status: StatusWatched, Rating: rating, Comment: formOrQuery(c, "comment"),
	}); err != nil {
		c.String(http.StatusInternalServerError, "操作失败")
		return
	}
	handler.renderButtons(c, movieID, false, true, true)
}

func (handler *Handler) remove(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	if err := handler.store.Remove(c.Request.Context(), userID, c.Param("id")); err != nil {
		c.String(http.StatusInternalServerError, "删除失败")
		return
	}
	if c.Query("source") == "dashboard" {
		c.String(http.StatusOK, "")
		return
	}
	handler.renderButtons(c, c.Param("id"), false, false, true)
}

func (handler *Handler) update(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	id, _ := strconv.Atoi(c.Param("id"))
	rating, _ := strconv.Atoi(c.DefaultPostForm("rating", "0"))
	if rating < 0 {
		rating = 0
	}
	if rating > 5 {
		rating = 5
	}
	if err := handler.store.UpdateRatingComment(c.Request.Context(), userID, id, rating, c.PostForm("comment")); err != nil {
		c.String(http.StatusInternalServerError, "保存失败")
		return
	}
	record, err := handler.store.GetByID(c.Request.Context(), userID, id)
	if err != nil || record == nil {
		c.String(http.StatusOK, "已保存")
		return
	}
	c.HTML(http.StatusOK, "partials/dashboard_watched_item.html", record)
}

func (handler *Handler) editForm(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "")
		return
	}
	id, _ := strconv.Atoi(c.Query("id"))
	record, err := handler.store.GetByID(c.Request.Context(), userID, id)
	if err != nil || record == nil {
		c.String(http.StatusOK, "")
		return
	}
	c.HTML(http.StatusOK, "partials/user_movie_edit_form.html", gin.H{"Record": record})
}

func (handler *Handler) markWatchedForm(c *gin.Context) {
	if auth.UserID(c) == 0 {
		c.String(http.StatusOK, "")
		return
	}
	movieID := c.Query("douban_id")
	variant := userMovieVariant(c)
	c.HTML(http.StatusOK, "partials/user_movie_mark_watched_form.html", gin.H{
		"DoubanID": movieID, "Title": c.Query("title"), "Poster": c.Query("poster"), "Year": c.Query("year"),
		"Variant": variant, "TargetID": userMovieActionsTargetID(variant, movieID),
	})
}

func (handler *Handler) buttons(c *gin.Context) {
	userID := auth.UserID(c)
	movieID := c.Query("douban_id")
	isWish, isWatched := false, false
	if userID > 0 && movieID != "" {
		if record, err := handler.store.GetByUserAndMovie(c.Request.Context(), userID, movieID); err == nil && record != nil {
			isWish, isWatched = record.Status == StatusWish, record.Status == StatusWatched
		}
	}
	handler.renderButtons(c, movieID, isWish, isWatched, userID > 0)
}

func (handler *Handler) dashboardWish(c *gin.Context) {
	handler.dashboardList(c, StatusWish)
}

func (handler *Handler) dashboardWatched(c *gin.Context) {
	handler.dashboardList(c, StatusWatched)
}

func (handler *Handler) dashboardList(c *gin.Context, status string) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * dashboardGridPageSize
	records, _ := handler.store.ListByUser(c.Request.Context(), userID, status, dashboardGridPageSize, offset)
	count, _ := handler.store.CountByUser(c.Request.Context(), userID, status)
	data := gin.H{"HasMore": offset+len(records) < count, "NextPage": page + 1, "IsFirstPage": page == 1}
	partial := ""
	if status == StatusWish {
		data["Wish"], data["WishCount"] = records, count
		partial = "dashboard_wish"
	} else {
		data["Watched"], data["WatchedCount"] = records, count
		partial = "dashboard_watched"
	}
	if page > 1 {
		partial += "_grid"
	}
	c.HTML(http.StatusOK, "partials/"+partial+".html", data)
}

func (handler *Handler) renderButtons(c *gin.Context, movieID string, isWish, isWatched, loggedIn bool) {
	variant := userMovieVariant(c)
	c.HTML(http.StatusOK, userMovieButtonTemplate(variant), gin.H{
		"DoubanID": movieID, "IsWish": isWish, "IsWatched": isWatched,
		"Title": formOrQuery(c, "title"), "Poster": formOrQuery(c, "poster"), "Year": formOrQuery(c, "year"),
		"LoggedIn": loggedIn, "Redirect": c.Query("redirect"),
	})
}

func formOrQuery(c *gin.Context, key string) string {
	if value := c.PostForm(key); value != "" {
		return value
	}
	return c.Query(key)
}

func userMovieVariant(c *gin.Context) string {
	if formOrQuery(c, "variant") == "play" {
		return "play"
	}
	return ""
}

func userMovieButtonTemplate(variant string) string {
	if variant == "play" {
		return "partials/play_watched_button.html"
	}
	return "partials/user_movie_buttons.html"
}

func userMovieActionsTargetID(variant, movieID string) string {
	if variant == "play" {
		return "play-actions-" + movieID
	}
	return "actions-" + movieID
}
