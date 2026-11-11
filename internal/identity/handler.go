package identity

import (
	"context"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/pow"
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
	// humanCheck 是登录、注册表单的工作量证明校验；没注入时使用默认难度的独立签发器。
	humanCheck *pow.Issuer
	// mailDomains 判断邮箱域名能否收信；没注入时不检查（测试和离线环境）。
	mailDomains EmailDomainChecker
}

// EmailDomainChecker 判断邮箱地址的域名能否收信，mailcheck.Checker 满足它。
type EmailDomainChecker interface {
	CanReceive(ctx context.Context, address string) bool
}

// 表单 scope：注册页签发的挑战不能拿去登录，反之亦然。
const (
	scopeLogin    = "login"
	scopeRegister = "register"
)

// 面向用户的提示。人机校验失败统一一句话，不告诉脚本具体哪一项没过。
const (
	messageHumanCheckFailed = "安全校验失败，请稍候重新提交"
	messageDomainCannotMail = "该邮箱域名无法接收邮件，请检查是否拼写错误"
	// 注册在没有邮箱验证的前提下无法完全隐藏「邮箱已注册」：成功会直接登录，失败总要给个理由。
	// 这里不说「已被注册」，引导去登录；真正的防枚举成本来自前面的人机校验。
	messageRegisterUnavailable = "无法使用该邮箱注册，如果你已有账号，请直接登录"
	messageEmailUnavailable    = "该邮箱不可用，请换一个邮箱"
)

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

// WithHumanCheck 注入共享的工作量证明签发器。
func WithHumanCheck(issuer *pow.Issuer) HandlerOption {
	return func(handler *Handler) { handler.humanCheck = issuer }
}

// WithEmailDomainChecker 注入邮箱域名 MX 检查。
func WithEmailDomainChecker(checker EmailDomainChecker) HandlerOption {
	return func(handler *Handler) { handler.mailDomains = checker }
}

// NewHandler 创建账号处理器。
func NewHandler(cfg config.Config, store Store, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, store: store, now: time.Now}
	for _, option := range options {
		option(handler)
	}
	if handler.humanCheck == nil {
		handler.humanCheck = pow.MustNewIssuer(pow.DefaultBits)
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
	router.POST("/dashboard/settings/ad-skip", require, handler.updateAdSkip)
}

// authForm 是登录、注册页重新渲染时要带回去的内容。
type authForm struct {
	Error    string
	Email    string
	Redirect string
	IsPublic bool
}

// loginPage 渲染登录页。
func (handler *Handler) loginPage(c *gin.Context) {
	handler.renderAuthPage(c, scopeLogin, http.StatusOK, authForm{Redirect: c.Query("redirect")})
}

// login 校验邮箱密码并下发登录 Cookie。
// 先做人机校验，再查库比对密码。账号不存在和密码错误返回同一句提示，
// 账号不存在时也跑一次 bcrypt，让两种情况的耗时一致，避免靠响应时间判断邮箱是否已注册。
func (handler *Handler) login(c *gin.Context) {
	email, password := c.PostForm("email"), c.PostForm("password")
	redirect := safeRedirect(c.PostForm("redirect"))
	form := authForm{Email: email, Redirect: redirect}
	if !handler.passHumanCheck(c, scopeLogin) {
		form.Error = messageHumanCheckFailed
		handler.renderAuthPage(c, scopeLogin, http.StatusForbidden, form)
		return
	}
	user, err := handler.store.FindByEmail(c.Request.Context(), email)
	passwordHash := dummyPasswordHash()
	if err == nil && user != nil {
		passwordHash = []byte(user.PasswordHash)
	}
	if bcrypt.CompareHashAndPassword(passwordHash, []byte(password)) != nil || err != nil || user == nil {
		form.Error = "邮箱或密码错误"
		handler.renderAuthPage(c, scopeLogin, http.StatusOK, form)
		return
	}
	if err := handler.issueToken(c, user); err != nil {
		form.Error = "登录失败，请重试"
		handler.renderAuthPage(c, scopeLogin, http.StatusInternalServerError, form)
		return
	}
	c.Redirect(http.StatusFound, redirect)
}

// registerPage 渲染注册页。
func (handler *Handler) registerPage(c *gin.Context) {
	handler.renderAuthPage(c, scopeRegister, http.StatusOK, authForm{IsPublic: true})
}

// register 注册新账号：人机校验、邮箱格式、两次密码一致、长度至少 6 位、邮箱域名能收信，
// 默认用户名取邮箱 @ 前的部分，主页默认公开，注册成功直接登录。
func (handler *Handler) register(c *gin.Context) {
	email, password, confirmation := c.PostForm("email"), c.PostForm("password"), c.PostForm("confirm_password")
	form := authForm{Email: email, IsPublic: c.PostForm("is_public") == "on"}
	fail := func(status int, message string) {
		form.Error = message
		handler.renderAuthPage(c, scopeRegister, status, form)
	}
	if !handler.passHumanCheck(c, scopeRegister) {
		fail(http.StatusForbidden, messageHumanCheckFailed)
		return
	}
	if !validEmail(email) {
		fail(http.StatusOK, "请输入有效的邮箱地址")
		return
	}
	if password != confirmation {
		fail(http.StatusOK, "两次输入的密码不一致")
		return
	}
	if len(password) < 6 {
		fail(http.StatusOK, "密码至少需要 6 个字符")
		return
	}
	if !handler.canReceiveMail(c, email) {
		fail(http.StatusOK, messageDomainCannotMail)
		return
	}
	if existing, _ := handler.store.FindByEmail(c.Request.Context(), email); existing != nil {
		fail(http.StatusOK, messageRegisterUnavailable)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fail(http.StatusInternalServerError, "注册失败，请重试")
		return
	}
	username := strings.Split(email, "@")[0]
	// 主页默认公开：片场的精选短评和片友推荐都只认 is_public=true 的用户，
	// 默认关着等于社区从第一天起就没有公共内容。注册表单里的开关默认勾选，
	// 想安静看片的人可以在这里或设置页关掉。
	user, err := handler.store.Create(c.Request.Context(), User{Email: email, Username: username, PasswordHash: string(hash), Role: "user", Avatar: "🎬", IsPublic: form.IsPublic, CreatedAt: handler.now()})
	if err != nil {
		fail(http.StatusInternalServerError, "注册失败，请重试")
		return
	}
	if err := handler.issueToken(c, user); err != nil {
		fail(http.StatusInternalServerError, "注册失败，请重试")
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

// updateEmail 修改邮箱：必须输入当前密码，新邮箱格式有效、域名能收信、未被其他账号占用。
// 先验密码再查占用，没有密码就没法拿这个接口逐个试探哪些邮箱已注册。
func (handler *Handler) updateEmail(c *gin.Context) {
	user := handler.currentUser(c)
	if user == nil {
		c.Redirect(http.StatusFound, "/auth/login")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(c.PostForm("current_password"))) != nil {
		handler.renderSettings(c, user, "", "当前密码错误")
		return
	}
	email := strings.TrimSpace(c.PostForm("email"))
	if !validEmail(email) {
		handler.renderSettings(c, user, "", "请输入有效的邮箱地址")
		return
	}
	if !handler.canReceiveMail(c, email) {
		handler.renderSettings(c, user, "", messageDomainCannotMail)
		return
	}
	if existing, _ := handler.store.FindByEmail(c.Request.Context(), email); existing != nil && existing.ID != user.ID {
		handler.renderSettings(c, user, "", messageEmailUnavailable)
		return
	}
	if err := handler.store.UpdateEmail(c.Request.Context(), user.ID, email); err != nil {
		handler.renderSettings(c, user, "", "邮箱更新失败")
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

// updateAdSkip 切换智能跳过广告。
func (handler *Handler) updateAdSkip(c *gin.Context) {
	enabled := c.PostForm("ad_skip_enabled") == "on"
	if err := handler.store.UpdateAdSkipEnabled(c.Request.Context(), auth.UserID(c), enabled); err != nil {
		handler.settingsError(c, "广告跳过设置更新失败")
		return
	}
	if c.GetHeader("HX-Request") != "" {
		user := handler.currentUser(c)
		c.HTML(http.StatusOK, "partials/ad_skip_toggle.html", gin.H{"User": user, "UserInfo": user, "LoggedIn": true})
		return
	}
	c.Redirect(http.StatusFound, "/dashboard/settings?success=ad_skip")
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

// renderAuthPage 是登录、注册页唯一的渲染入口：每次渲染都签发新挑战，
// 出错重新渲染时带回用户已填的邮箱，避免某条分支忘了签发挑战导致表单再也提交不了。
func (handler *Handler) renderAuthPage(c *gin.Context, scope string, status int, form authForm) {
	page, title := "login.html", "登录 - "+handler.config.SiteName
	if scope == scopeRegister {
		page, title = "register.html", "注册 - "+handler.config.SiteName
	}
	challenge, err := handler.humanCheck.Issue(scope)
	if err != nil {
		slog.Error("issue human check challenge failed", "scope", scope, "error", err)
	}
	// 带挑战的页面不能被浏览器或 CDN 缓存，否则多人会拿到同一个挑战。
	c.Header("Cache-Control", "no-store")
	c.HTML(status, page, platformweb.NewData(c, handler.config, platformweb.Metadata{Title: title}, gin.H{
		"Error": form.Error, "Email": form.Email, "Redirect": form.Redirect, "IsPublic": form.IsPublic, "PowToken": challenge,
	}))
}

// passHumanCheck 校验陷阱字段和工作量证明，失败只记日志不告诉用户原因。
func (handler *Handler) passHumanCheck(c *gin.Context, scope string) bool {
	if err := handler.humanCheck.CheckForm(c.PostForm, scope); err != nil {
		slog.Info("human check rejected", "scope", scope, "reason", err.Error(), "client_ip", c.ClientIP())
		return false
	}
	return true
}

// canReceiveMail 检查邮箱域名能否收信；没注入检查器时放行。
func (handler *Handler) canReceiveMail(c *gin.Context, email string) bool {
	return handler.mailDomains == nil || handler.mailDomains.CanReceive(c.Request.Context(), email)
}

// dummyPasswordHash 返回一个固定的 bcrypt 哈希，账号不存在时拿它比对，
// 让「账号不存在」和「密码错误」花的时间一样。首次使用时生成，代价与真实哈希相同。
var dummyPasswordHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("moovie-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return hash
})

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
