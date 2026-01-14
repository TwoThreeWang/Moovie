package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	var req struct {
		Records    []*model.WatchHistory `json:"records"`
		LastSyncAt int64                 `json:"lastSyncAt"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求数据"})
		return
	}

	// 保存客户端记录到服务端
	for _, record := range req.Records {
		record.UserID = userID
		h.Repos.History.Upsert(record)
	}

	// 获取服务端最新记录返回给客户端
	serverRecords, _ := h.Repos.History.ListByUser(userID, 100, 0)

	c.JSON(http.StatusOK, gin.H{
		"serverRecords":  serverRecords,
		"serverSyncedAt": serverRecords[0].WatchedAt.Unix(),
	})
}
