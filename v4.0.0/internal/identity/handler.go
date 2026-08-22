package identity

import (
	"context"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// Handler 提供登录注册页面和用户中心。
// 四个匿名接口字段都是可选依赖，只为在用户中心显示计数，没注入就显示 0。
type Handler struct {
	config         config.Config
	store          Store
	now            func() time.Time
	historyCounter interface {
		CountByUser(ctx context.Context, userID int) (int, error)
	}
	libraryCounter interface {
		CountByUser(ctx context.Context, userID int, status string) (int, error)
	}
	monthlyReports interface {
		LatestForDashboard(ctx context.Context, userID int) (any, error)
	}
	feedbackCounter interface {
		CountByUser(ctx context.Context, userID int) (int, error)
	}
}

// HandlerOption 用于注入可选的计数来源。
type HandlerOption func(*Handler)

// WithHistoryCounter 注入观看记录计数。
func WithHistoryCounter(counter interface {
	CountByUser(ctx context.Context, userID int) (int, error)
}) HandlerOption {
	return func(handler *Handler) { handler.historyCounter = counter }
}

// WithLibraryCounter 注入片单计数。
func WithLibraryCounter(counter interface {
	CountByUser(ctx context.Context, userID int, status string) (int, error)
}) HandlerOption {
	return func(handler *Handler) { handler.libraryCounter = counter }
}

// WithMonthlyReportReader 注入最新月报。
func WithMonthlyReportReader(reader interface {
	LatestForDashboard(ctx context.Context, userID int) (any, error)
}) HandlerOption {
	return func(handler *Handler) { handler.monthlyReports = reader }
}

// WithFeedbackCounter 注入反馈计数。
func WithFeedbackCounter(counter interface {
	CountByUser(ctx context.Context, userID int) (int, error)
}) HandlerOption {
	return func(handler *Handler) { handler.feedbackCounter = counter }
}

// NewHandler 创建账号处理器。
func NewHandler(cfg config.Config, store Store, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, store: store, now: time.Now}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Register 注册登录注册路由（无需登录）和用户中心路由（必须登录）。
func (handler *Handler) Register(router *gin.Engine) {
	router.GET("/auth/login", handler.loginPage)
	router.POST("/auth/login", handler.login)
	router.GET("/auth/register", handler.registerPage)
	router.POST("/auth/register", handler.register)
	router.GET("/auth/logout", handler.logout)
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.GET("/dashboard", require, handler.dashboard)
	router.GET("/dashboard/settings", require, handler.settings)
	router.POST("/dashboard/settings/email", require, handler.updateEmail)
	router.POST("/dashboard/settings/username", require, handler.updateUsername)
	router.POST("/dashboard/settings/password", require, handler.updatePassword)
	router.POST("/dashboard/settings/share", require, handler.updateShare)
	router.POST("/dashboard/settings/avatar", require, handler.updateAvatar)
}

// loginPage 渲染登录页。
func (handler *Handler) loginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "登录 - " + handler.config.SiteName}, gin.H{"Redirect": c.Query("redirect")}))
}

// login 校验邮箱密码并下发登录 Cookie。
// 账号不存在和密码错误返回同一句提示，避免暴露哪些邮箱已注册。
func (handler *Handler) login(c *gin.Context) {
	email, password := c.PostForm("email"), c.PostForm("password")
	redirect := safeRedirect(c.PostForm("redirect"))
	user, err := handler.store.FindByEmail(c.Request.Context(), email)
	if err != nil || user == nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		handler.renderLoginError(c, http.StatusOK, "邮箱或密码错误")
		return
	}
	if err := handler.issueToken(c, user); err != nil {
		handler.renderLoginError(c, http.StatusInternalServerError, "登录失败，请重试")
		return
	}
	c.Redirect(http.StatusFound, redirect)
}

// registerPage 渲染注册页。
func (handler *Handler) registerPage(c *gin.Context) {
	c.HTML(http.StatusOK, "register.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "注册 - " + handler.config.SiteName}, nil))
}

// register 注册新账号：校验邮箱格式、两次密码一致、长度至少 6 位，
// 默认用户名取邮箱 @ 前的部分，注册成功直接登录。
func (handler *Handler) register(c *gin.Context) {
	email, password, confirmation := c.PostForm("email"), c.PostForm("password"), c.PostForm("confirm_password")
	if !validEmail(email) {
		handler.renderRegisterError(c, http.StatusOK, "请输入有效的邮箱地址")
		return
	}
	if password != confirmation {
		handler.renderRegisterError(c, http.StatusOK, "两次输入的密码不一致")
		return
	}
	if len(password) < 6 {
		handler.renderRegisterError(c, http.StatusOK, "密码至少需要 6 个字符")
		return
	}
	if existing, _ := handler.store.FindByEmail(c.Request.Context(), email); existing != nil {
		handler.renderRegisterError(c, http.StatusOK, "该邮箱已被注册")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		handler.renderRegisterError(c, http.StatusInternalServerError, "注册失败，请重试")
		return
	}
	username := strings.Split(email, "@")[0]
	user, err := handler.store.Create(c.Request.Context(), User{Email: email, Username: username, PasswordHash: string(hash), Role: "user", Avatar: "🎬", CreatedAt: handler.now()})
	if err != nil {
		handler.renderRegisterError(c, http.StatusInternalServerError, "注册失败，请重试")
		return
	}
	if err := handler.issueToken(c, user); err != nil {
		handler.renderRegisterError(c, http.StatusInternalServerError, "注册失败，请重试")
		return
	}
	c.Redirect(http.StatusFound, "/")
}

// logout 清除登录 Cookie。
func (handler *Handler) logout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{Name: "token", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: handler.config.Env == "production", SameSite: http.SameSiteLaxMode})
	c.Redirect(http.StatusFound, "/")
}

// dashboard 渲染用户中心。
func (handler *Handler) dashboard(c *gin.Context) {
	user := handler.currentUser(c)
	if user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	// 这些计数失败时页面照常渲染，但错误不能静默吞掉：曾经有一个 SQL 少传参数的
	// bug，因为 err 被丢给 _，仪表盘只是一直显示 0，没有任何线索。
	countError := func(name string, err error) {
		if err != nil {
			slog.Warn("dashboard counter failed", "counter", name, "user_id", user.ID, "error", err)
		}
	}
	historyCount := 0
	if handler.historyCounter != nil {
		count, err := handler.historyCounter.CountByUser(c.Request.Context(), user.ID)
		countError("history", err)
		historyCount = count
	}
	favoriteCount, watchedCount := 0, 0
	if handler.libraryCounter != nil {
		wish, err := handler.libraryCounter.CountByUser(c.Request.Context(), user.ID, "wish")
		countError("wish", err)
		favoriteCount = wish
		watched, err := handler.libraryCounter.CountByUser(c.Request.Context(), user.ID, "watched")
		countError("watched", err)
		watchedCount = watched
	}
	var monthlyReport any
	if handler.monthlyReports != nil {
		monthlyReport, _ = handler.monthlyReports.LatestForDashboard(c.Request.Context(), user.ID)
	}
	feedbackCount := 0
	if handler.feedbackCounter != nil {
		feedbackCount, _ = handler.feedbackCounter.CountByUser(c.Request.Context(), user.ID)
	}
	c.HTML(http.StatusOK, "dashboard.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "用户中心 - " + handler.config.SiteName}, gin.H{
		"User": user, "UserInfo": user, "FavoriteCount": favoriteCount, "WatchedCount": watchedCount,
		"HistoryCount": historyCount, "FeedbackCount": feedbackCount, "MonthlyReport": monthlyReport,
	}))
}

// settings 渲染账号设置页。
func (handler *Handler) settings(c *gin.Context) {
	user := handler.currentUser(c)
	if user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	handler.renderSettings(c, user, c.Query("success"), "")
}

// updateUsername 修改用户名（2-20 字符）。
func (handler *Handler) updateUsername(c *gin.Context) {
	username := strings.TrimSpace(c.PostForm("username"))
	if len(username) < 2 || len(username) > 20 {
		handler.settingsError(c, "用户名应在 2-20 个字符之间")
		return
	}
	if err := handler.store.UpdateUsername(c.Request.Context(), auth.UserID(c), username); err != nil {
		handler.settingsError(c, "用户名更新失败")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=username")
}

// updateEmail 修改邮箱，需检查未被其他账号占用。
func (handler *Handler) updateEmail(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	if email == "" || !strings.Contains(email, "@") {
		handler.settingsError(c, "请输入有效的邮箱地址")
		return
	}
	userID := auth.UserID(c)
	if existing, _ := handler.store.FindByEmail(c.Request.Context(), email); existing != nil && existing.ID != userID {
		handler.settingsError(c, "该邮箱已被其他账号使用")
		return
	}
	if err := handler.store.UpdateEmail(c.Request.Context(), userID, email); err != nil {
		handler.settingsError(c, "邮箱更新失败")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=email")
}

// updatePassword 修改密码，必须先验证当前密码。
func (handler *Handler) updatePassword(c *gin.Context) {
	user := handler.currentUser(c)
	if user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(c.PostForm("current_password"))) != nil {
		handler.renderSettings(c, user, "", "当前密码错误")
		return
	}
	password := c.PostForm("new_password")
	if password != c.PostForm("confirm_password") {
		handler.renderSettings(c, user, "", "两次输入的新密码不一致")
		return
	}
	if len(password) < 6 {
		handler.renderSettings(c, user, "", "新密码至少需要 6 个字符")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil || handler.store.UpdatePassword(c.Request.Context(), user.ID, string(hash)) != nil {
		handler.renderSettings(c, user, "", "密码更新失败")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=password")
}

// updateShare 切换个人主页是否公开。
func (handler *Handler) updateShare(c *gin.Context) {
	if err := handler.store.UpdateIsPublic(c.Request.Context(), auth.UserID(c), c.PostForm("is_public") == "on"); err != nil {
		handler.settingsError(c, "分享设置更新失败")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=share")
}

// updateAvatar 修改头像（一个 emoji）。
func (handler *Handler) updateAvatar(c *gin.Context) {
	avatar := strings.TrimSpace(c.PostForm("avatar"))
	if avatar == "" {
		handler.settingsError(c, "请选择或输入一个 emoji 作为头像")
		return
	}
	if len([]rune(avatar)) > 4 {
		handler.settingsError(c, "头像最多支持 4 个 emoji 字符")
		return
	}
	if err := handler.store.UpdateAvatar(c.Request.Context(), auth.UserID(c), avatar); err != nil {
		handler.settingsError(c, "头像更新失败")
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=avatar")
}

// currentUser 从库中读取当前登录用户。
func (handler *Handler) currentUser(c *gin.Context) *User {
	user, _ := handler.store.FindByID(c.Request.Context(), auth.UserID(c))
	return user
}

// settingsError 带错误信息重新渲染设置页。
func (handler *Handler) settingsError(c *gin.Context, message string) {
	user := handler.currentUser(c)
	handler.renderSettings(c, user, "", message)
}

// renderSettings 渲染设置页。
func (handler *Handler) renderSettings(c *gin.Context, user *User, success, message string) {
	c.HTML(http.StatusOK, "settings.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "账号设置 - " + handler.config.SiteName}, gin.H{
		"User": user, "UserInfo": user, "Success": success, "Error": message, "DoubanJob": nil,
	}))
}

// issueToken 签发登录 JWT 并写入 Cookie。
func (handler *Handler) issueToken(c *gin.Context, user *User) error {
	now := handler.now()
	token, err := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: now.Unix(), Expiry: now.Add(handler.config.JWTExpiry).Unix()}, handler.config.AppSecret)
	if err != nil {
		return err
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "token", Value: token, Path: "/", MaxAge: int(handler.config.JWTExpiry.Seconds()), HttpOnly: true, Secure: handler.config.Env == "production", SameSite: http.SameSiteLaxMode})
	return nil
}

// renderLoginError 带错误信息重新渲染登录页。
func (handler *Handler) renderLoginError(c *gin.Context, status int, message string) {
	c.HTML(status, "login.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "登录 - Moovie影牛"}, gin.H{"Error": message}))
}

// renderRegisterError 带错误信息重新渲染注册页。
func (handler *Handler) renderRegisterError(c *gin.Context, status int, message string) {
	c.HTML(status, "register.html", platformweb.NewData(c, handler.config, platformweb.Metadata{Title: "注册 - Moovie影牛"}, gin.H{"Error": message}))
}

// safeRedirect 只允许站内相对路径跳转，防止被构造成钓鱼跳转到外站。
func safeRedirect(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	return value
}

// validEmail 校验邮箱格式。
func validEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && strings.Contains(value, "@")
}
