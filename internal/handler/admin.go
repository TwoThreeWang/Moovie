package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/utils"
)

// ==================== 管理后台 ====================

// AdminDashboard 后台首页
func (h *Handler) AdminDashboard(c *gin.Context) {
	// 获取统计数据
	sites, _ := h.Repos.Site.ListAll()

	c.HTML(http.StatusOK, "admin_dashboard.html", gin.H{
		"Title":     "管理后台 - Moovie",
		"UserID":    middleware.GetUserID(c),
		"SiteCount": len(sites),
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

// AdminSites 资源网管理页面
func (h *Handler) AdminSites(c *gin.Context) {
	sites, err := h.Repos.Site.ListAll()
	if err != nil {
		sites = []*model.Site{}
	}

	c.HTML(http.StatusOK, "admin_sites.html", gin.H{
		"Title":  "资源网管理 - Moovie",
		"UserID": middleware.GetUserID(c),
		"Sites":  sites,
	})
}

// AdminSiteCreate 创建资源网
func (h *Handler) AdminSiteCreate(c *gin.Context) {
	site := &model.Site{
		Key:     c.PostForm("key"),
		BaseUrl: c.PostForm("base_url"),
		Enabled: c.PostForm("enabled") == "on",
	}

	if site.Key == "" || site.BaseUrl == "" {
		utils.BadRequest(c, "Key 和 BaseUrl 不能为空")
		return
	}

	if err := h.Repos.Site.Create(site); err != nil {
		utils.InternalServerError(c, "创建失败: "+err.Error())
		return
	}

	utils.Success(c, site)
}

// AdminSiteUpdate 更新资源网
func (h *Handler) AdminSiteUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		utils.BadRequest(c, "无效的 ID")
		return
	}

	site := &model.Site{
		ID:      uint(id),
		Key:     c.PostForm("key"),
		BaseUrl: c.PostForm("base_url"),
		Enabled: c.PostForm("enabled") == "on" || c.PostForm("enabled") == "true",
	}

	if err := h.Repos.Site.Update(site); err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	utils.Success(c, site)
}

// AdminSiteDelete 删除资源网
func (h *Handler) AdminSiteDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		utils.BadRequest(c, "无效的 ID")
		return
	}

	if err := h.Repos.Site.Delete(uint(id)); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	utils.Success(c, nil)
}

// AdminData 搜索数据管理页面
func (h *Handler) AdminData(c *gin.Context) {
	c.HTML(http.StatusOK, "admin_cache.html", gin.H{
		"Title":  "搜索数据管理 - Moovie",
		"UserID": middleware.GetUserID(c),
	})
}

// AdminDataClean 清理非活跃搜索数据
func (h *Handler) AdminDataClean(c *gin.Context) {
	// 清理 7 天内未访问的数据
	affected, err := h.Repos.VodItem.DeleteInactive(7)
	if err != nil {
		utils.InternalServerError(c, "清理失败")
		return
	}

	utils.Success(c, gin.H{
		"affected": affected,
		"message":  "清理完成",
	})
}
