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
	userCount, _ := h.Repos.User.Count()
	feedbackCount, _ := h.Repos.Feedback.CountPending()
	movieCount, _ := h.Repos.Movie.Count()

	c.HTML(http.StatusOK, "admin_dashboard.html", h.RenderData(c, gin.H{
		"Title":         "管理后台 - Moovie",
		"SiteCount":     len(sites),
		"UserCount":     userCount,
		"FeedbackCount": feedbackCount,
		"MovieCount":    movieCount,
	}))
}

// AdminUsers 用户管理
func (h *Handler) AdminUsers(c *gin.Context) {
	users, err := h.Repos.User.ListAll()
	if err != nil {
		users = []*model.User{}
	}

	c.HTML(http.StatusOK, "admin_users.html", h.RenderData(c, gin.H{
		"Title": "用户管理 - Moovie",
		"Users": users,
	}))
}

// AdminSites 资源网管理页面
func (h *Handler) AdminSites(c *gin.Context) {
	sites, err := h.Repos.Site.ListAll()
	if err != nil {
		sites = []*model.Site{}
	}

	c.HTML(http.StatusOK, "admin_sites.html", h.RenderData(c, gin.H{
		"Title": "资源网管理 - Moovie",
		"Sites": sites,
	}))
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

// AdminSiteTest 测试资源网搜索
func (h *Handler) AdminSiteTest(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		utils.BadRequest(c, "无效的 ID")
		return
	}

	// 查找站点
	var site model.Site
	if err := h.Repos.DB.First(&site, id).Error; err != nil {
		utils.NotFound(c, "资源网不存在")
		return
	}

	// 测试关键词
	keyword := "肖申克的救赎"

	// 调用搜索服务进行测试
	items, err := h.SearchService.GetSearchCrawler().Search(c.Request.Context(), site.BaseUrl, keyword, site.Key)
	if err != nil {
		utils.InternalServerError(c, "测试失败: "+err.Error())
		return
	}

	utils.Success(c, gin.H{
		"count":   len(items),
		"keyword": keyword,
		"items":   items,
	})
}

// AdminData 搜索数据管理页面
func (h *Handler) AdminData(c *gin.Context) {
	c.HTML(http.StatusOK, "admin_cache.html", h.RenderData(c, gin.H{
		"Title": "搜索数据管理 - Moovie",
	}))
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

// AdminFeedback 反馈管理页面
func (h *Handler) AdminFeedback(c *gin.Context) {
	status := c.DefaultQuery("status", "")
	feedbacks, err := h.Repos.Feedback.List(status, 100, 0)
	if err != nil {
		feedbacks = []*model.Feedback{}
	}

	c.HTML(http.StatusOK, "admin_feedback.html", h.RenderData(c, gin.H{
		"Title":     "反馈管理 - Moovie",
		"Feedbacks": feedbacks,
		"Status":    status,
	}))
}

// AdminUserUpdateRole 更新用户角色
func (h *Handler) AdminUserUpdateRole(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		utils.BadRequest(c, "无效的用户 ID")
		return
	}

	role := c.PostForm("role")
	if role != "user" && role != "admin" {
		utils.BadRequest(c, "无效的角色")
		return
	}

	// 不能修改自己的角色
	currentUserID := middleware.GetUserID(c)
	if currentUserID == id {
		utils.BadRequest(c, "不能修改自己的角色")
		return
	}

	if err := h.Repos.User.UpdateRole(id, role); err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	utils.Success(c, gin.H{"message": "角色已更新"})
}

// AdminUserDelete 删除用户
func (h *Handler) AdminUserDelete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		utils.BadRequest(c, "无效的用户 ID")
		return
	}

	// 不能删除自己
	currentUserID := middleware.GetUserID(c)
	if currentUserID == id {
		utils.BadRequest(c, "不能删除自己的账号")
		return
	}

	if err := h.Repos.User.Delete(id); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	utils.Success(c, gin.H{"message": "用户已删除"})
}

// AdminFeedbackStatus 更新反馈状态
func (h *Handler) AdminFeedbackStatus(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		utils.BadRequest(c, "无效的反馈 ID")
		return
	}

	status := c.PostForm("status")
	if status != "pending" && status != "resolved" && status != "rejected" {
		utils.BadRequest(c, "无效的状态")
		return
	}

	if err := h.Repos.Feedback.UpdateStatus(id, status); err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	utils.Success(c, gin.H{"message": "状态已更新"})
}

// AdminFeedbackReply 回复反馈
func (h *Handler) AdminFeedbackReply(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		utils.BadRequest(c, "无效的反馈 ID")
		return
	}

	reply := c.PostForm("reply")
	if reply == "" {
		utils.BadRequest(c, "回复内容不能为空")
		return
	}

	if err := h.Repos.Feedback.Reply(id, reply); err != nil {
		utils.InternalServerError(c, "回复失败")
		return
	}

	utils.Success(c, gin.H{"message": "回复成功"})
}

// ==================== 版权限制管理 ====================

// AdminCopyright 版权限制管理页面
func (h *Handler) AdminCopyright(c *gin.Context) {
	filters, err := h.Repos.CopyrightFilter.ListAll()
	if err != nil {
		filters = []*model.CopyrightFilter{}
	}

	c.HTML(http.StatusOK, "admin_copyright.html", h.RenderData(c, gin.H{
		"Title":   "版權限制管理 - Moovie",
		"Filters": filters,
	}))
}

// AdminCopyrightCreate 添加版权关键词
func (h *Handler) AdminCopyrightCreate(c *gin.Context) {
	keyword := c.PostForm("keyword")
	if keyword == "" {
		utils.BadRequest(c, "关键词不能为空")
		return
	}

	filter := &model.CopyrightFilter{
		Keyword: keyword,
	}

	if err := h.Repos.CopyrightFilter.Create(filter); err != nil {
		utils.InternalServerError(c, "创建失败: "+err.Error())
		return
	}

	utils.Success(c, filter)
}

// AdminCopyrightUpdate 更新版权关键词
func (h *Handler) AdminCopyrightUpdate(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		utils.BadRequest(c, "无效的 ID")
		return
	}

	keyword := c.PostForm("keyword")
	if keyword == "" {
		utils.BadRequest(c, "关键词不能为空")
		return
	}

	filter := &model.CopyrightFilter{
		ID:      uint(id),
		Keyword: keyword,
	}

	if err := h.Repos.CopyrightFilter.Update(filter); err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	utils.Success(c, filter)
}

// AdminCopyrightDelete 删除版权关键词
func (h *Handler) AdminCopyrightDelete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		utils.BadRequest(c, "无效的 ID")
		return
	}

	if err := h.Repos.CopyrightFilter.Delete(uint(id)); err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	utils.Success(c, nil)
}
