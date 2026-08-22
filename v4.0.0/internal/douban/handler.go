package douban

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

// Validator 校验豆瓣 ID 是否有效。
type Validator interface {
	ValidateUser(ctx context.Context, doubanUserID string) error
}

// JobEnqueuer 负责创建同步任务。
type JobEnqueuer interface {
	CreateFull(ctx context.Context, userID int) (int, error)
}

// Handler 提供设置页里的豆瓣绑定、解绑和手动同步。
type Handler struct {
	config    config.Config
	users     UserStore
	jobs      JobStore
	validator Validator
	enqueuer  JobEnqueuer
}

// NewHandler 创建豆瓣处理器。
func NewHandler(cfg config.Config, users UserStore, jobs JobStore, validator Validator, enqueuer JobEnqueuer) *Handler {
	return &Handler{config: cfg, users: users, jobs: jobs, validator: validator, enqueuer: enqueuer}
}

// Register 注册路由，全部要求登录。
func (handler *Handler) Register(router *gin.Engine) {
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.POST("/dashboard/settings/douban/bind", require, handler.bind)
	router.POST("/dashboard/settings/douban/unbind", require, handler.unbind)
	router.POST("/dashboard/settings/douban/sync", require, handler.sync)
	router.GET("/api/htmx/douban-sync-status", auth.Optional(handler.config.AppSecret), handler.status)
}

// bind 绑定豆瓣账号，校验通过后立即排一次全量同步。
func (handler *Handler) bind(c *gin.Context) {
	userID := auth.UserID(c)
	user, err := handler.users.FindByID(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	doubanUserID := extractUserID(c.PostForm("douban_user_id"))
	if doubanUserID == "" {
		handler.renderSettings(c, user, "请输入有效的豆瓣用户 ID 或主页链接")
		return
	}
	if err := handler.validator.ValidateUser(c.Request.Context(), doubanUserID); err != nil {
		handler.renderSettings(c, user, "验证豆瓣账号失败: "+err.Error())
		return
	}
	if err := handler.users.UpdateDoubanUserID(c.Request.Context(), userID, doubanUserID); err != nil {
		handler.renderSettings(c, user, "绑定失败，请稍后重试")
		return
	}
	_, _ = handler.enqueuer.CreateFull(c.Request.Context(), userID)
	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_bind")
}

// unbind 解绑豆瓣账号（已同步的片单记录保留）。
func (handler *Handler) unbind(c *gin.Context) {
	if err := handler.users.UpdateDoubanUserID(c.Request.Context(), auth.UserID(c), ""); err != nil {
		user, _ := handler.users.FindByID(c.Request.Context(), auth.UserID(c))
		handler.renderSettings(c, user, "解绑失败，请稍后重试")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_unbind")
}

// sync 手动触发一次增量同步。
func (handler *Handler) sync(c *gin.Context) {
	userID := auth.UserID(c)
	user, err := handler.users.FindByID(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	if user.DoubanUserID == "" {
		handler.renderSettings(c, user, "请先绑定豆瓣账号")
		return
	}
	if active, _ := handler.jobs.HasActive(c.Request.Context(), userID); active {
		handler.renderSettings(c, user, "已有同步任务正在运行")
		return
	}
	_, err = handler.enqueuer.CreateFull(c.Request.Context(), userID)
	if err != nil {
		handler.renderSettings(c, user, "创建同步任务失败")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_sync")
}

// status 返回同步进度片段，供页面轮询。
func (handler *Handler) status(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "")
		return
	}
	user, err := handler.users.FindByID(c.Request.Context(), userID)
	if err != nil || user == nil {
		c.String(http.StatusOK, "")
		return
	}
	job, err := handler.jobs.LatestByUser(c.Request.Context(), userID)
	if err != nil || job == nil {
		c.String(http.StatusOK, "")
		return
	}
	c.HTML(http.StatusOK, "partials/douban_sync_status.html", gin.H{"User": user, "DoubanJob": job})
}

// renderSettings 带提示信息重新渲染设置页。
func (handler *Handler) renderSettings(c *gin.Context, user *identity.User, message string) {
	c.HTML(http.StatusOK, "settings.html", platformweb.NewData(c, handler.config,
		platformweb.Metadata{Title: "账号设置 - " + handler.config.SiteName},
		gin.H{"User": user, "UserInfo": user, "Error": message, "DoubanJob": nil}))
}

// doubanUserIDPattern 从豆瓣主页链接里提取用户 ID。
var doubanUserIDPattern = regexp.MustCompile(`(?:people|user)/(\d+)`)

// extractUserID 支持用户直接填 ID 或粘贴主页链接。
func extractUserID(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	allDigits := true
	for _, character := range input {
		if character < '0' || character > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return input
	}
	matches := doubanUserIDPattern.FindStringSubmatch(input)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}
