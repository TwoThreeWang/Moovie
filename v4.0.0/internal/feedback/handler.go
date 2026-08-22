package feedback

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

// Handler 提供反馈页面、提交接口和后台管理接口。
type Handler struct {
	config config.Config
	store  Store
}

// NewHandler 创建反馈处理器。
func NewHandler(cfg config.Config, store Store) *Handler { return &Handler{config: cfg, store: store} }

// Register 注册路由：前台可选登录，后台要求登录且必须是管理员。
func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/feedback", optional, handler.page)
	router.POST("/api/feedback", optional, handler.submit)
	router.GET("/api/htmx/feedback-list", optional, handler.publicList)
	router.GET("/api/htmx/dashboard/feedback", optional, handler.dashboardList)
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.GET("/admin/feedback", require, requireAdmin, handler.adminPage)
	router.PUT("/admin/feedback/:id/status", require, requireAdmin, handler.adminStatus)
	router.PUT("/admin/feedback/:id/reply", require, requireAdmin, handler.adminReply)
}

// page 渲染反馈页。
func (handler *Handler) page(c *gin.Context) {
	c.HTML(http.StatusOK, "feedback.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "反馈建议 - " + handler.config.SiteName}, nil))
}

// submit 提交反馈，返回 HTMX 片段而不是 JSON；内容上限 5000 字。
func (handler *Handler) submit(c *gin.Context) {
	content := strings.TrimSpace(c.PostForm("content"))
	feedbackType := strings.TrimSpace(c.PostForm("type"))
	movieURL := strings.TrimSpace(c.PostForm("movie_url"))
	if content == "" {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(`<div class="alert alert-error">请填写反馈内容</div>`))
		return
	}
	if len([]rune(content)) > 5000 {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(`<div class="alert alert-error">反馈内容不能超过 5000 个字符</div>`))
		return
	}
	if !validType(feedbackType) {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(`<div class="alert alert-error">反馈类型无效</div>`))
		return
	}
	if movieURL != "" && !validURL(movieURL) {
		c.Data(http.StatusBadRequest, "text/html; charset=utf-8", []byte(`<div class="alert alert-error">相关链接无效</div>`))
		return
	}
	record := Feedback{Type: feedbackType, Content: content, MovieURL: movieURL}
	if userID := auth.UserID(c); userID > 0 {
		record.UserID = &userID
	}
	if _, err := handler.store.Create(c.Request.Context(), record); err != nil {
		c.Data(http.StatusInternalServerError, "text/html; charset=utf-8", []byte(`<div class="alert alert-error">提交失败，请重试</div>`))
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="alert alert-success">感谢您的反馈！我们会尽快处理。</div>`))
}

// publicList 渲染公开的反馈列表。
func (handler *Handler) publicList(c *gin.Context) {
	page := pageNumber(c)
	feedbackType := c.Query("type")
	if feedbackType != "" && !validType(feedbackType) {
		feedbackType = ""
	}
	handler.renderList(c, feedbackType, 0, page, false)
}

// dashboardList 渲染当前用户自己的反馈列表。
func (handler *Handler) dashboardList(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}
	handler.renderList(c, "", userID, pageNumber(c), true)
}

// renderList 是两个列表接口共用的渲染逻辑。
func (handler *Handler) renderList(c *gin.Context, feedbackType string, userID, page int, dashboard bool) {
	const pageSize = 10
	offset := (page - 1) * pageSize
	var records []Feedback
	var total int
	var err error
	if dashboard {
		records, err = handler.store.ListByUser(c.Request.Context(), userID, pageSize, offset)
		total, _ = handler.store.CountByUser(c.Request.Context(), userID)
	} else {
		records, err = handler.store.ListPublic(c.Request.Context(), feedbackType, pageSize, offset)
		total, _ = handler.store.CountPublic(c.Request.Context(), feedbackType)
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	templateName := "partials/feedback_list.html"
	if dashboard {
		templateName = "partials/dashboard_feedback.html"
	}
	c.HTML(http.StatusOK, templateName, gin.H{
		"Feedbacks": records, "HasMore": page*pageSize < total, "NextPage": page + 1,
		"IsFirstPage": page == 1, "Type": feedbackType,
	})
}

// adminPage 渲染后台反馈管理页。
func (handler *Handler) adminPage(c *gin.Context) {
	status := c.Query("status")
	if status != "" && !validStatus(status) {
		status = ""
	}
	records, err := handler.store.ListAdmin(c.Request.Context(), status, 100, 0)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	c.HTML(http.StatusOK, "admin_feedback.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "反馈管理 - Moovie影牛"}, gin.H{"Feedbacks": records, "Status": status}))
}

// adminStatus 修改反馈状态。
func (handler *Handler) adminStatus(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的反馈 ID")
		return
	}
	status := c.PostForm("status")
	if !validStatus(status) {
		apiError(c, http.StatusBadRequest, "无效的状态")
		return
	}
	if err := handler.store.UpdateStatus(c.Request.Context(), id, status); err != nil {
		apiError(c, http.StatusInternalServerError, "更新失败")
		return
	}
	apiSuccess(c, gin.H{"message": "状态已更新"})
}

// adminReply 回复反馈。
func (handler *Handler) adminReply(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的反馈 ID")
		return
	}
	reply := strings.TrimSpace(c.PostForm("reply"))
	if reply == "" {
		apiError(c, http.StatusBadRequest, "回复内容不能为空")
		return
	}
	if len([]rune(reply)) > 5000 {
		apiError(c, http.StatusBadRequest, "回复内容不能超过 5000 个字符")
		return
	}
	if err := handler.store.Reply(c.Request.Context(), id, reply); err != nil {
		apiError(c, http.StatusInternalServerError, "回复失败")
		return
	}
	apiSuccess(c, gin.H{"message": "回复成功"})
}

// requireAdmin 是管理员校验中间件。
func requireAdmin(c *gin.Context) {
	if role, exists := c.Get("role"); !exists || role != "admin" {
		apiError(c, http.StatusForbidden, "需要管理员权限")
		c.Abort()
		return
	}
	c.Next()
}

// apiSuccess 返回成功响应。
func apiSuccess(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "success", "data": data, "success": true})
}

// apiError 返回错误响应。
func apiError(c *gin.Context, code int, message string) {
	c.JSON(code, gin.H{"code": code, "message": message, "data": nil, "success": false})
}

// pageNumber 读取页码参数，非法值一律当第 1 页。
func pageNumber(c *gin.Context) int {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		return 1
	}
	return page
}

// positiveID 解析并校验正整数 ID。
func positiveID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}

// validType 校验反馈类型。
func validType(value string) bool {
	// 系统告警已不再写入反馈，仅保留历史数据。持久化层与 CHECK 约束仍接受该类型，
	// 但它不是允许提交的公开反馈类型。
	return value == TypeBug || value == TypeRequest || value == TypeSuggestion || value == TypeDMCA
}

// validStatus 校验处理状态。
func validStatus(value string) bool {
	return value == StatusPending || value == StatusResolved || value == StatusRejected
}

// validURL 校验相关链接必须是 http/https 绝对地址。
func validURL(value string) bool {
	if len(value) > 2000 {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
