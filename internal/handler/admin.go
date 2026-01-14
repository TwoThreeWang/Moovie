package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/middleware"
)

// ==================== 管理后台 ====================

// AdminDashboard 后台首页
func (h *Handler) AdminDashboard(c *gin.Context) {
	c.HTML(http.StatusOK, "admin_dashboard.html", gin.H{
		"Title":  "管理后台 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// AdminUsers 用户管理
func (h *Handler) AdminUsers(c *gin.Context) {
	c.HTML(http.StatusOK, "admin_users.html", gin.H{
		"Title":  "用户管理 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// AdminCrawlers 爬虫监控
func (h *Handler) AdminCrawlers(c *gin.Context) {
	c.HTML(http.StatusOK, "admin_crawlers.html", gin.H{
		"Title":  "爬虫监控 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}
