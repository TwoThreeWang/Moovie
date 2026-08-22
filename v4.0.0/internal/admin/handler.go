// Package admin 是管理后台，只有 role=admin 的账号能访问。
//
// 本包自己不建表，全部通过其他包的接口读写：用户、资源网、版权/分类过滤词、
// 任务队列、运行指标、资源匹配复核、资源退役。
//
// 页面接口返回 HTML，操作接口统一返回 {code, message, data, success} 的 JSON。
package admin

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/feedback"
	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/operations"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
)

// UserStore 是后台需要的账号读写接口。
type UserStore interface {
	ListUsers(ctx context.Context) ([]identity.User, error)
	UpdateRole(ctx context.Context, userID int, role string) error
	Delete(ctx context.Context, userID int) error
}

// SearchStore 是后台需要的资源网和过滤词接口。
type SearchStore interface {
	ListSites(ctx context.Context) ([]search.Site, error)
	GetSite(ctx context.Context, id uint) (*search.Site, error)
	CreateSite(ctx context.Context, site search.Site) (*search.Site, error)
	UpdateSite(ctx context.Context, site search.Site) error
	DeleteSite(ctx context.Context, id uint) error
	DeleteInactive(ctx context.Context, days int) (int, error)
	ListCopyrightFilters(ctx context.Context) ([]search.Filter, error)
	CreateCopyrightFilter(ctx context.Context, keyword string) (*search.Filter, error)
	UpdateCopyrightFilter(ctx context.Context, id uint, keyword string) error
	DeleteCopyrightFilter(ctx context.Context, id uint) error
	ListCategoryFilters(ctx context.Context) ([]search.Filter, error)
	CreateCategoryFilter(ctx context.Context, keyword string) (*search.Filter, error)
	DeleteCategoryFilter(ctx context.Context, id uint) error
	SummaryHealthSince(ctx context.Context, since time.Time) (map[string]*search.HealthSummary, error)
}

// CircuitState 用于在资源网列表上显示熔断状态。
type CircuitState interface {
	TrippedUntil(siteKey string) time.Time
}

// MovieCounter 用于首页统计影片数量。
type MovieCounter interface {
	Count(ctx context.Context) (int, error)
}

// FeedbackCounter 用于首页统计待处理反馈数量。
type FeedbackCounter interface {
	CountPending(ctx context.Context) (int, error)
}

// JobRetrier 由 workqueue.PostgresStore 实现。两个方法都返回实际恢复的任务数，
// 因为「没恢复」是正常结果而不是错误：任务可能已经不是失败状态，
// 也可能同一对象已经有新任务排在队列里。
type JobRetrier interface {
	RetryJob(ctx context.Context, jobID int) (int, error)
	RetryFailed(ctx context.Context, taskType string, limit int) (int, error)
}

// Handler 是后台的全部接口。metrics 和 jobs 是可选的，没注入时相关页面返回 503。
type Handler struct {
	config   config.Config
	users    UserStore
	search   SearchStore
	movies   MovieCounter
	feedback FeedbackCounter
	crawler  search.SourceCrawler
	health   CircuitState
	metrics  operations.MetricsReader
	jobs     JobRetrier
}

// HandlerOption 用于注入可选依赖。
type HandlerOption func(*Handler)

// WithMetricsReader 注入运行指标读取器。
func WithMetricsReader(reader operations.MetricsReader) HandlerOption {
	return func(handler *Handler) { handler.metrics = reader }
}

// WithJobRetrier 注入任务重试能力。
func WithJobRetrier(retrier JobRetrier) HandlerOption {
	return func(handler *Handler) { handler.jobs = retrier }
}

// NewHandler 创建后台处理器。
func NewHandler(cfg config.Config, users UserStore, searchStore SearchStore, movies MovieCounter, feedbackStore feedback.Store, crawler search.SourceCrawler, health CircuitState, options ...HandlerOption) *Handler {
	handler := &Handler{config: cfg, users: users, search: searchStore, movies: movies, feedback: feedbackStore, crawler: crawler, health: health}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Register 注册后台路由，所有路由都先过登录校验再过管理员校验。
func (handler *Handler) Register(router *gin.Engine) {
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	middleware := []gin.HandlerFunc{require, requireAdmin}
	router.GET("/admin", append(middleware, handler.dashboard)...)
	router.GET("/admin/users", append(middleware, handler.userList)...)
	router.PUT("/admin/users/:id/role", append(middleware, handler.userRole)...)
	router.DELETE("/admin/users/:id", append(middleware, handler.userDelete)...)
	router.GET("/admin/sites", append(middleware, handler.siteList)...)
	router.POST("/admin/sites", append(middleware, handler.siteCreate)...)
	router.PUT("/admin/sites/:id", append(middleware, handler.siteUpdate)...)
	router.DELETE("/admin/sites/:id", append(middleware, handler.siteDelete)...)
	router.GET("/admin/sites/:id/test", append(middleware, handler.siteTest)...)
	router.GET("/admin/data", append(middleware, handler.dataPage)...)
	router.GET("/admin/jobs", append(middleware, handler.jobQueuePage)...)
	router.POST("/admin/jobs/retry", append(middleware, handler.jobRetry)...)
	router.POST("/admin/jobs/retry-failed", append(middleware, handler.jobRetryFailed)...)
	router.GET("/admin/matches", append(middleware, handler.matchReviewPage)...)
	router.POST("/admin/matches/decision", append(middleware, handler.matchReviewDecision)...)
	router.GET("/api/v2/admin/media-matches", append(middleware, handler.matchReviewAPIList)...)
	router.POST("/api/v2/admin/media-matches/:id/resolve", append(middleware, handler.matchReviewAPIResolve)...)
	router.GET("/api/v2/admin/metrics", append(middleware, handler.metricsSnapshot)...)
	router.POST("/admin/data/clean", append(middleware, handler.dataClean)...)
	router.GET("/admin/copyright", append(middleware, handler.copyrightList)...)
	router.POST("/admin/copyright", append(middleware, handler.copyrightCreate)...)
	router.PUT("/admin/copyright/:id", append(middleware, handler.copyrightUpdate)...)
	router.DELETE("/admin/copyright/:id", append(middleware, handler.copyrightDelete)...)
	router.GET("/admin/category", append(middleware, handler.categoryList)...)
	router.POST("/admin/category", append(middleware, handler.categoryCreate)...)
	router.DELETE("/admin/category/:id", append(middleware, handler.categoryDelete)...)
}

// jobQueuePage 渲染任务队列页，按状态筛选、按游标翻页。
func (handler *Handler) jobQueuePage(c *gin.Context) {
	reader, ok := handler.metrics.(operations.JobQueueReader)
	if !ok {
		apiError(c, http.StatusServiceUnavailable, "任务队列暂不可用")
		return
	}
	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", "all")))
	if status != "all" && status != "pending" && status != "running" && status != "completed" && status != "failed" {
		status = "all"
	}
	direction := strings.ToLower(strings.TrimSpace(c.DefaultQuery("direction", "next")))
	if direction != "prev" {
		direction = "next"
	}
	cursor, err := strconv.ParseInt(c.DefaultQuery("cursor", "0"), 10, 64)
	if err != nil || cursor < 0 {
		cursor = 0
	}
	queryStatus := status
	if queryStatus == "all" {
		queryStatus = ""
	}
	snapshot, err := reader.JobQueue(c.Request.Context(), operations.JobQueueQuery{
		Status: queryStatus, Direction: direction, Cursor: cursor, Limit: 50,
	})
	if err != nil {
		apiError(c, http.StatusInternalServerError, "读取任务队列失败")
		return
	}
	handler.page(c, "admin_jobs.html", "任务队列 - Moovie影牛", gin.H{
		"Queue": snapshot, "Status": status,
	})
}

// jobRetry 重试单个失败任务。任务 ID 走表单而不是路径参数，
// 是为了避免 /admin/jobs/:id/retry 与 /admin/jobs/retry-failed 在路由树上冲突。
func (handler *Handler) jobRetry(c *gin.Context) {
	if handler.jobs == nil {
		apiError(c, http.StatusServiceUnavailable, "任务队列暂不可用")
		return
	}
	jobID, err := positiveInt(c.PostForm("job_id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "任务 ID 无效")
		return
	}
	retried, err := handler.jobs.RetryJob(c.Request.Context(), jobID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "重试失败")
		return
	}
	if retried == 0 {
		apiError(c, http.StatusConflict, "该任务已不是失败状态，或同一对象已有任务在队列中")
		return
	}
	apiSuccess(c, gin.H{"job_id": jobID, "retried": retried})
}

// jobRetryFailed 按类型批量重试。带上限是因为这些任务多半是被同一个上游拒绝的，
// 一次性全放回去只会再被拒一遍。
func (handler *Handler) jobRetryFailed(c *gin.Context) {
	if handler.jobs == nil {
		apiError(c, http.StatusServiceUnavailable, "任务队列暂不可用")
		return
	}
	taskType := strings.TrimSpace(c.PostForm("task_type"))
	if taskType != "" && !taskTypePattern.MatchString(taskType) {
		apiError(c, http.StatusBadRequest, "任务类型无效")
		return
	}
	limit := 500
	if raw := strings.TrimSpace(c.PostForm("limit")); raw != "" {
		parsed, err := positiveInt(raw)
		if err != nil || parsed > 2000 {
			apiError(c, http.StatusBadRequest, "单次重试上限必须在 1 到 2000 之间")
			return
		}
		limit = parsed
	}
	retried, err := handler.jobs.RetryFailed(c.Request.Context(), taskType, limit)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "批量重试失败")
		return
	}
	apiSuccess(c, gin.H{"task_type": taskType, "retried": retried, "limit": limit})
}

// metricsSnapshot 返回运行指标快照 JSON，前端定时刷新。
func (handler *Handler) metricsSnapshot(c *gin.Context) {
	if handler.metrics == nil {
		apiError(c, http.StatusServiceUnavailable, "运行指标暂不可用")
		return
	}
	snapshot, err := handler.metrics.Snapshot(c.Request.Context())
	if err != nil {
		apiError(c, http.StatusInternalServerError, "读取运行指标失败")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"success": true, "data": snapshot})
}

// matchReviewPage 渲染资源匹配复核页：机器拿不准的「资源属于哪部片」由人来定。
func (handler *Handler) matchReviewPage(c *gin.Context) {
	store, ok := handler.search.(search.MatchReviewStore)
	if !ok {
		apiError(c, http.StatusNotImplemented, "当前存储不支持资源匹配复核")
		return
	}
	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", search.MatchStatusReview)))
	if status != search.MatchStatusReview && status != search.MatchStatusVerified && status != search.MatchStatusRejected {
		status = search.MatchStatusReview
	}
	candidates, err := store.ListMatchCandidates(c.Request.Context(), status, 100)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "读取待复核资源失败")
		return
	}
	handler.page(c, "admin_matches.html", "资源匹配复核 - Moovie影牛", gin.H{"Candidates": candidates, "Status": status})
}

// matchReviewDecision 处理页面表单提交的复核结果。
func (handler *Handler) matchReviewDecision(c *gin.Context) {
	store, ok := handler.search.(search.MatchReviewStore)
	if !ok {
		apiError(c, http.StatusNotImplemented, "当前存储不支持资源匹配复核")
		return
	}
	sourceKey, vodID := strings.TrimSpace(c.PostForm("source_key")), strings.TrimSpace(c.PostForm("vod_id"))
	mediaID, err := positiveInt(c.PostForm("media_id"))
	decision, reason := strings.ToLower(strings.TrimSpace(c.PostForm("decision"))), strings.TrimSpace(c.PostForm("reason"))
	if err != nil || sourceKey == "" || vodID == "" || len(sourceKey) > 64 || len(vodID) > 128 ||
		(decision != search.MatchStatusVerified && decision != search.MatchStatusRejected) || reason == "" || len([]rune(reason)) > 500 {
		apiError(c, http.StatusBadRequest, "复核参数不完整或无效")
		return
	}
	if err := store.ReviewMatchCandidate(c.Request.Context(), sourceKey, vodID, mediaID, auth.UserID(c), decision, reason); err != nil {
		apiError(c, http.StatusConflict, "复核失败: "+err.Error())
		return
	}
	apiSuccess(c, gin.H{"source_key": sourceKey, "vod_id": vodID, "media_id": mediaID, "decision": decision})
}

// matchReviewAPIList 是复核列表的 JSON 接口。
func (handler *Handler) matchReviewAPIList(c *gin.Context) {
	store, ok := handler.search.(search.MatchReviewStore)
	if !ok {
		matchAPIError(c, http.StatusNotImplemented, "match_review_unavailable", "资源匹配复核尚未启用")
		return
	}
	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", search.MatchStatusReview)))
	if status != search.MatchStatusReview && status != search.MatchStatusVerified && status != search.MatchStatusRejected {
		matchAPIError(c, http.StatusBadRequest, "invalid_status", "status 参数无效")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 {
		matchAPIError(c, http.StatusBadRequest, "invalid_limit", "limit 必须在 1 到 100 之间")
		return
	}
	candidates, err := store.ListMatchCandidates(c.Request.Context(), status, limit)
	if err != nil {
		matchAPIError(c, http.StatusInternalServerError, "match_review_read_failed", "读取匹配候选失败")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"matches": candidates, "status": status, "limit": limit}})
}

// matchResolveRequest 是复核接口的请求体。
type matchResolveRequest struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	MediaID  int    `json:"media_id,omitempty"`
}

// matchReviewAPIResolve 是复核提交的 JSON 接口，按候选 ID 处理。
func (handler *Handler) matchReviewAPIResolve(c *gin.Context) {
	store, ok := handler.search.(search.MatchReviewStore)
	if !ok {
		matchAPIError(c, http.StatusNotImplemented, "match_review_unavailable", "资源匹配复核尚未启用")
		return
	}
	candidateID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var request matchResolveRequest
	if err != nil || candidateID <= 0 || c.ShouldBindJSON(&request) != nil {
		matchAPIError(c, http.StatusBadRequest, "invalid_request", "复核请求无效")
		return
	}
	request.Decision, request.Reason = strings.ToLower(strings.TrimSpace(request.Decision)), strings.TrimSpace(request.Reason)
	if (request.Decision != search.MatchStatusVerified && request.Decision != search.MatchStatusRejected) ||
		request.Reason == "" || len([]rune(request.Reason)) > 500 {
		matchAPIError(c, http.StatusBadRequest, "invalid_decision", "decision 或 reason 无效")
		return
	}
	if request.MediaID < 0 {
		matchAPIError(c, http.StatusBadRequest, "invalid_media_id", "media_id 参数无效")
		return
	}
	if err := store.ResolveMatchCandidateByID(c.Request.Context(), candidateID, request.MediaID, auth.UserID(c), request.Decision, request.Reason); err != nil {
		matchAPIError(c, http.StatusConflict, "match_review_conflict", "候选不存在、已处理或与人工锁定关系冲突")
		return
	}
	data := gin.H{"candidate_id": candidateID, "decision": request.Decision}
	if request.MediaID > 0 {
		data["resolved_media_id"] = request.MediaID
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// matchAPIError 返回带错误码的复核接口错误。
func matchAPIError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

// dashboard 渲染后台首页的几个数量统计。
func (handler *Handler) dashboard(c *gin.Context) {
	users, _ := handler.users.ListUsers(c.Request.Context())
	sites, _ := handler.search.ListSites(c.Request.Context())
	movieCount, _ := handler.movies.Count(c.Request.Context())
	feedbackCount, _ := handler.feedback.CountPending(c.Request.Context())
	handler.page(c, "admin_dashboard.html", "管理后台 - Moovie影牛", gin.H{
		"UserCount": len(users), "SiteCount": len(sites), "MovieCount": movieCount, "FeedbackCount": feedbackCount,
	})
}

// userList 渲染用户管理页。
func (handler *Handler) userList(c *gin.Context) {
	users, err := handler.users.ListUsers(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	handler.page(c, "admin_users.html", "用户管理 - Moovie影牛", gin.H{"Users": users})
}

// userRole 修改用户角色，不允许改自己的，防止把自己降权锁在门外。
func (handler *Handler) userRole(c *gin.Context) {
	id, err := positiveInt(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	role := c.PostForm("role")
	if role != "user" && role != "admin" {
		apiError(c, http.StatusBadRequest, "无效的角色")
		return
	}
	if id == auth.UserID(c) {
		apiError(c, http.StatusBadRequest, "不能修改自己的角色")
		return
	}
	if err := handler.users.UpdateRole(c.Request.Context(), id, role); err != nil {
		apiError(c, http.StatusInternalServerError, "更新失败")
		return
	}
	apiSuccess(c, gin.H{"message": "角色已更新"})
}

// userDelete 删除用户，同样不允许删自己。
func (handler *Handler) userDelete(c *gin.Context) {
	id, err := positiveInt(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	if id == auth.UserID(c) {
		apiError(c, http.StatusBadRequest, "不能删除自己的账号")
		return
	}
	if err := handler.users.Delete(c.Request.Context(), id); err != nil {
		apiError(c, http.StatusInternalServerError, "删除失败")
		return
	}
	apiSuccess(c, gin.H{"message": "用户已删除"})
}

// siteList 渲染资源网列表，附带最近 24 小时的健康统计和熔断状态。
func (handler *Handler) siteList(c *gin.Context) {
	sites, err := handler.search.ListSites(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	stats, err := handler.search.SummaryHealthSince(c.Request.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		stats = map[string]*search.HealthSummary{}
	}
	for _, site := range sites {
		summary := stats[site.Key]
		if summary == nil {
			summary = &search.HealthSummary{SiteKey: site.Key}
			stats[site.Key] = summary
		}
		if handler.health != nil {
			if until := handler.health.TrippedUntil(site.Key); !until.IsZero() {
				summary.Tripped = true
				summary.TrippedUntil = until
			}
		}
	}
	handler.page(c, "admin_sites.html", "资源网管理 - Moovie影牛", gin.H{"Sites": sites, "Stats": stats})
}

// siteCreate 新增资源网。
func (handler *Handler) siteCreate(c *gin.Context) {
	site, ok := parseSite(c, true)
	if !ok {
		return
	}
	created, err := handler.search.CreateSite(c.Request.Context(), site)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("resource site creation failed", "source_key", site.Key, "error", err)
		apiError(c, http.StatusInternalServerError, "创建失败")
		return
	}
	apiSuccess(c, created)
}

// siteUpdate 修改资源网。
func (handler *Handler) siteUpdate(c *gin.Context) {
	id, err := positiveUint(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的 ID")
		return
	}
	site := search.Site{ID: id, Key: strings.TrimSpace(c.PostForm("key")), BaseURL: strings.TrimSpace(c.PostForm("base_url")), Enabled: c.PostForm("enabled") == "on" || c.PostForm("enabled") == "true"}
	if site.Key != "" && !siteKeyPattern.MatchString(site.Key) {
		apiError(c, http.StatusBadRequest, "Key 格式无效")
		return
	}
	if site.BaseURL != "" && !validHTTPURL(site.BaseURL) {
		apiError(c, http.StatusBadRequest, "BaseUrl 无效")
		return
	}
	if err := handler.search.UpdateSite(c.Request.Context(), site); err != nil {
		apiError(c, http.StatusInternalServerError, "更新失败")
		return
	}
	apiSuccess(c, site)
}

// siteDelete 删除资源网。
func (handler *Handler) siteDelete(c *gin.Context) {
	id, err := positiveUint(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的 ID")
		return
	}
	if err := handler.search.DeleteSite(c.Request.Context(), id); err != nil {
		apiError(c, http.StatusInternalServerError, "删除失败")
		return
	}
	apiSuccess(c, nil)
}

// siteTest 用一个关键词实际请求一次资源网，确认接口是否还能用。
func (handler *Handler) siteTest(c *gin.Context) {
	id, err := positiveUint(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的 ID")
		return
	}
	site, err := handler.search.GetSite(c.Request.Context(), id)
	if err != nil || site == nil {
		apiError(c, http.StatusNotFound, "资源网不存在")
		return
	}
	keyword := strings.TrimSpace(c.DefaultQuery("keyword", "肖申克的救赎"))
	items, err := handler.crawler.Search(c.Request.Context(), site.BaseURL, keyword, site.Key, nil)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("resource site test failed", "source_key", site.Key, "error", err)
		apiError(c, http.StatusInternalServerError, "测试失败")
		return
	}
	previews := make([]siteTestPreview, 0, len(items))
	for _, item := range items {
		previews = append(previews, siteTestPreview{Name: item.VodName, Remarks: item.VodRemarks, TypeName: item.TypeName, UpdatedAt: item.VodTime})
	}
	apiSuccess(c, gin.H{"count": len(previews), "keyword": keyword, "items": previews})
}

// siteTestPreview 刻意排除 VodPlayUrl 和其他未使用的上游字段，
// 避免带签名的播放地址进入后台 JSON 响应。
type siteTestPreview struct {
	Name      string `json:"vod_name"`
	Remarks   string `json:"vod_remarks"`
	TypeName  string `json:"type_name"`
	UpdatedAt string `json:"vod_time"`
}

// dataPage 渲染搜索数据管理页。
func (handler *Handler) dataPage(c *gin.Context) {
	handler.page(c, "admin_cache.html", "搜索数据管理 - Moovie影牛", nil)
}

// dataClean 清理 7 天没更新过的资源记录。
func (handler *Handler) dataClean(c *gin.Context) {
	affected, err := handler.search.DeleteInactive(c.Request.Context(), 7)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "清理失败")
		return
	}
	apiSuccess(c, gin.H{"affected": affected, "message": "清理完成"})
}


// copyrightList 渲染版权屏蔽词列表，命中的影片不出现在搜索结果里。
func (handler *Handler) copyrightList(c *gin.Context) {
	filters, err := handler.search.ListCopyrightFilters(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	handler.page(c, "admin_copyright.html", "版權限制管理 - Moovie影牛", gin.H{"Filters": filters})
}

// copyrightCreate 新增版权屏蔽词。
func (handler *Handler) copyrightCreate(c *gin.Context) {
	keyword, ok := keyword(c)
	if !ok {
		return
	}
	filter, err := handler.search.CreateCopyrightFilter(c.Request.Context(), keyword)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "创建失败: "+err.Error())
		return
	}
	apiSuccess(c, filter)
}

// copyrightUpdate 修改版权屏蔽词。
func (handler *Handler) copyrightUpdate(c *gin.Context) {
	id, err := positiveUint(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的 ID")
		return
	}
	value, ok := keyword(c)
	if !ok {
		return
	}
	if err := handler.search.UpdateCopyrightFilter(c.Request.Context(), id, value); err != nil {
		apiError(c, http.StatusInternalServerError, "更新失败")
		return
	}
	apiSuccess(c, search.Filter{ID: id, Keyword: value})
}

// copyrightDelete 删除版权屏蔽词。
func (handler *Handler) copyrightDelete(c *gin.Context) {
	handler.deleteFilter(c, handler.search.DeleteCopyrightFilter)
}

// categoryList 渲染分类屏蔽词列表，用于过滤掉不想收录的资源分类。
func (handler *Handler) categoryList(c *gin.Context) {
	filters, err := handler.search.ListCategoryFilters(c.Request.Context())
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	handler.page(c, "admin_category.html", "分类过滤管理 - Moovie影牛", gin.H{"Filters": filters})
}

// categoryCreate 新增分类屏蔽词。
func (handler *Handler) categoryCreate(c *gin.Context) {
	value, ok := keyword(c)
	if !ok {
		return
	}
	filter, err := handler.search.CreateCategoryFilter(c.Request.Context(), value)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "创建失败: "+err.Error())
		return
	}
	apiSuccess(c, filter)
}

// categoryDelete 删除分类屏蔽词。
func (handler *Handler) categoryDelete(c *gin.Context) {
	handler.deleteFilter(c, handler.search.DeleteCategoryFilter)
}

// deleteFilter 是两类屏蔽词删除的公共实现。
func (handler *Handler) deleteFilter(c *gin.Context, remove func(context.Context, uint) error) {
	id, err := positiveUint(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "无效的 ID")
		return
	}
	if err := remove(c.Request.Context(), id); err != nil {
		apiError(c, http.StatusInternalServerError, "删除失败")
		return
	}
	apiSuccess(c, nil)
}

// page 渲染后台页面。
func (handler *Handler) page(c *gin.Context, templateName, title string, extra gin.H) {
	if extra == nil {
		extra = gin.H{}
	}
	extra["ContentClass"] = "full-width"
	c.HTML(http.StatusOK, templateName, platformweb.NewData(c, handler.config, platformweb.Metadata{Title: title}, extra))
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

// parseSite 解析并校验资源网表单。
func parseSite(c *gin.Context, requireValues bool) (search.Site, bool) {
	site := search.Site{Key: strings.TrimSpace(c.PostForm("key")), BaseURL: strings.TrimSpace(c.PostForm("base_url")), Enabled: c.PostForm("enabled") == "on" || c.PostForm("enabled") == "true"}
	if requireValues && (site.Key == "" || site.BaseURL == "") {
		apiError(c, http.StatusBadRequest, "Key 和 BaseUrl 不能为空")
		return search.Site{}, false
	}
	if !siteKeyPattern.MatchString(site.Key) || !validHTTPURL(site.BaseURL) {
		apiError(c, http.StatusBadRequest, "Key 或 BaseUrl 格式无效")
		return search.Site{}, false
	}
	return site, true
}

// keyword 解析并校验屏蔽词表单。
func keyword(c *gin.Context) (string, bool) {
	value := strings.TrimSpace(c.PostForm("keyword"))
	if value == "" {
		apiError(c, http.StatusBadRequest, "关键词不能为空")
		return "", false
	}
	if len([]rune(value)) > 100 {
		apiError(c, http.StatusBadRequest, "关键词不能超过 100 个字符")
		return "", false
	}
	return value, true
}

// positiveInt 解析正整数。
func positiveInt(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}

// positiveUint 解析正整数（无符号）。
func positiveUint(value string) (uint, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil || id == 0 {
		return 0, strconv.ErrSyntax
	}
	return uint(id), nil
}

// siteKeyPattern 限制资源网 Key 的字符集。
var siteKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// taskTypePattern 限制任务类型的字符集。
var taskTypePattern = regexp.MustCompile(`^[a-z_]{1,40}$`)

// validHTTPURL 校验是公网可达的 http(s) 地址，挡住指向内网的地址。
func validHTTPURL(value string) bool {
	return outbound.ValidatePublicHTTPURL(value) == nil
}
