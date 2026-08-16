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

type Validator interface {
	ValidateUser(ctx context.Context, doubanUserID string) error
}

type JobEnqueuer interface {
	CreateFull(ctx context.Context, userID int) (int, error)
}

type Handler struct {
	config    config.Config
	users     UserStore
	jobs      JobStore
	validator Validator
	enqueuer  JobEnqueuer
}

func NewHandler(cfg config.Config, users UserStore, jobs JobStore, validator Validator, enqueuer JobEnqueuer) *Handler {
	return &Handler{config: cfg, users: users, jobs: jobs, validator: validator, enqueuer: enqueuer}
}

func (handler *Handler) Register(router *gin.Engine) {
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.POST("/dashboard/settings/douban/bind", require, handler.bind)
	router.POST("/dashboard/settings/douban/unbind", require, handler.unbind)
	router.POST("/dashboard/settings/douban/sync", require, handler.sync)
	router.GET("/api/htmx/douban-sync-status", auth.Optional(handler.config.AppSecret), handler.status)
}

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

func (handler *Handler) unbind(c *gin.Context) {
	if err := handler.users.UpdateDoubanUserID(c.Request.Context(), auth.UserID(c), ""); err != nil {
		user, _ := handler.users.FindByID(c.Request.Context(), auth.UserID(c))
		handler.renderSettings(c, user, "解绑失败，请稍后重试")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=douban_unbind")
}

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

func (handler *Handler) renderSettings(c *gin.Context, user *identity.User, message string) {
	c.HTML(http.StatusOK, "settings.html", platformweb.NewData(c, handler.config,
		platformweb.Metadata{Title: "账号设置 - " + handler.config.SiteName},
		gin.H{"User": user, "UserInfo": user, "Error": message, "DoubanJob": nil}))
}

var doubanUserIDPattern = regexp.MustCompile(`(?:people|user)/(\d+)`)

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
