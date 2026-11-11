package feedback

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/pow"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

// Handler 提供反馈页面、提交接口和后台管理接口。
type Handler struct {
	config     config.Config
	store      Store
	humanCheck *pow.Issuer
}

// scopeFeedback 是反馈表单的人机校验 scope。
const scopeFeedback = "feedback"

// HandlerOption 是 Handler 的可选装配项。
type HandlerOption func(*Handler)

// WithHumanCheck 注入共享的工作量证明签发器；不注入时使用默认难度的独立签发器。
func WithHumanCheck(issuer *pow.Issuer) HandlerOption {
	return func(handler *Handler) { handler.humanCheck = issuer }
}

// NewHandler 创建反馈处理器。
func NewHandler(cfg config.Config, store Store, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, store: store}
	for _, option := range options {
		option(handler)
	}
	if handler.humanCheck == nil {
		handler.humanCheck = pow.MustNewIssuer(pow.DefaultBits)
	}
	return handler
}

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

// feedbackForm 是反馈表单的回填内容。
type feedbackForm struct {
	Error, Type, MovieURL, Content string
}

// page 渲染反馈页。
func (handler *Handler) page(c *gin.Context) {
	data := platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "反馈建议 - " + handler.config.SiteName, Canonical: platformweb.CanonicalURL(handler.config.SiteURL, "/feedback")}, nil)
	for key, value := range handler.formData(feedbackForm{Type: TypeBug}) {
		data[key] = value
	}
	c.Header("Cache-Control", "no-store")
	c.HTML(http.StatusOK, "feedback.html", data)
}

// submit 提交反馈：先做人机校验再校验内容（上限 5000 字）。
// 出错时返回整个表单片段（错误提示 + 已填内容 + 新挑战），由 hx-swap="outerHTML" 原位替换。
// 状态码用 200：HTMX 默认不替换 4xx 响应，返回 400 的话错误提示根本显示不出来。
func (handler *Handler) submit(c *gin.Context) {
	content := strings.TrimSpace(c.PostForm("content"))
	feedbackType := strings.TrimSpace(c.PostForm("type"))
	movieURL := strings.TrimSpace(c.PostForm("movie_url"))
	form := feedbackForm{Type: feedbackType, MovieURL: movieURL, Content: content}
	fail := func(status int, message string) {
		form.Error = message
		c.Header("Cache-Control", "no-store")
		c.HTML(status, "partials/feedback_form.html", handler.formData(form))
	}
	if err := handler.humanCheck.CheckForm(c.PostForm, scopeFeedback); err != nil {
		slog.Info("human check rejected", "scope", scopeFeedback, "reason", err.Error(), "client_ip", c.ClientIP())
		fail(http.StatusOK, "安全校验失败，请稍候重新提交")
		return
	}
	if content == "" {
		fail(http.StatusOK, "请填写反馈内容")
		return
	}
	if len([]rune(content)) > 5000 {
		fail(http.StatusOK, "反馈内容不能超过 5000 个字符")
		return
	}
	if !validType(feedbackType) {
		form.Type = TypeBug
		fail(http.StatusOK, "反馈类型无效")
		return
	}
	if movieURL != "" && !validURL(movieURL) {
		fail(http.StatusOK, "相关链接无效")
		return
	}
	record := Feedback{Type: feedbackType, Content: content, MovieURL: movieURL}
	if userID := auth.UserID(c); userID > 0 {
		record.UserID = &userID
	}
	if _, err := handler.store.Create(c.Request.Context(), record); err != nil {
		fail(http.StatusOK, "提交失败，请重试")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<div class="alert alert-success">感谢您的反馈！我们会尽快处理。</div>`))
}

// formData 组装反馈表单模板数据，每次都签发新的人机校验挑战。
func (handler *Handler) formData(form feedbackForm) gin.H {
	challenge, err := handler.humanCheck.Issue(scopeFeedback)
	if err != nil {
		slog.Error("issue human check challenge failed", "scope", scopeFeedback, "error", err)
	}
	return gin.H{"PowToken": challenge, "Error": form.Error, "Type": form.Type, "MovieURL": form.MovieURL, "Content": form.Content}
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
