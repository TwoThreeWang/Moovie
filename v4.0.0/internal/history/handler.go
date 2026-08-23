package history

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	"github.com/gin-gonic/gin"
)

// NSFWKeywordReader 提供 NSFW 标签关键词。
type NSFWKeywordReader interface {
	NSFWKeywords(ctx context.Context) ([]string, error)
}

// Handler 提供观看记录的接口和首页/仪表盘的 HTMX 片段。
type Handler struct {
	store             Store
	secret            string
	now               func() time.Time
	todayUpdateReader TodayUpdateReader
	episodeReader     mediaidentity.EpisodeReader
	timeZone          string
	nsfwReader        NSFWKeywordReader
}

// HandlerOption 用于注入可选依赖。
type HandlerOption func(*Handler)

// WithTodayUpdateReader 启用首页"今日更新"。timeZone 决定"今天"按哪个日历判断，
// 传空时回退到东八区——追剧更新时间必须按用户本地日历显示，UTC 会整体差一天。
func WithTodayUpdateReader(reader TodayUpdateReader, timeZone string) HandlerOption {
	return func(handler *Handler) { handler.todayUpdateReader, handler.timeZone = reader, timeZone }
}

// WithEpisodeReader 让今日更新只把确实有当集候选的卡片链到 /watch。
func WithEpisodeReader(reader mediaidentity.EpisodeReader) HandlerOption {
	return func(handler *Handler) { handler.episodeReader = reader }
}

// WithNSFWKeywordReader 启用首页继续观看的 NSFW 海报模糊化。
func WithNSFWKeywordReader(reader NSFWKeywordReader) HandlerOption {
	return func(handler *Handler) { handler.nsfwReader = reader }
}

// NewHandler 创建观看记录处理器。
func NewHandler(store Store, secret string, options ...HandlerOption) *Handler {
	handler := &Handler{store: store, secret: secret, now: time.Now}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Register 注册路由。全部用 Optional 鉴权：未登录时返回空内容而不是 401，
// 这样首页的 HTMX 片段对游客也能正常渲染。
func (handler *Handler) Register(router *gin.Engine) {
	optionalAuth := auth.Optional(handler.secret)
	router.POST("/api/v2/history/sync", optionalAuth, handler.syncV2)
	router.DELETE("/api/v2/history/:id", optionalAuth, handler.remove)
	router.GET("/api/htmx/dashboard/history", optionalAuth, handler.dashboard)
	router.GET("/api/htmx/history/recent", optionalAuth, handler.recent)
	router.GET("/api/htmx/history/today-updates", optionalAuth, handler.todayUpdates)
}

// syncV2 是多设备同步入口：校验参数 → 逐条应用操作 → 返回增量变更和冲突。
func (handler *Handler) syncV2(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	var request SyncV2Request
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求数据"})
		return
	}
	now := handler.now().UTC()
	if err := normalizeSyncRequest(&request, now); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": strings.TrimSpace(err.Error())})
		return
	}
	result, err := handler.store.SyncV2(c.Request.Context(), userID, request, now)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("history v2 sync failed",
			"user_id", userID, "device_id", request.DeviceID, "operation_count", len(request.Operations), "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "同步观看记录失败"})
		return
	}
	c.JSON(http.StatusOK, result)
}

// dashboard 渲染个人中心的观看记录（每页 24 条，翻页返回纯网格片段）。
func (handler *Handler) dashboard(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusOK, "未登录")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	const pageSize = 24
	offset := (page - 1) * pageSize
	records, count, _ := handler.continueRecords(c, userID, pageSize, offset)
	partial := "partials/dashboard_history.html"
	if page > 1 {
		partial = "partials/dashboard_history_grid.html"
	}
	c.HTML(http.StatusOK, partial, gin.H{
		"History": records, "HasMore": offset+len(records) < count,
		"NextPage": page + 1, "IsFirstPage": page == 1,
	})
}

// recent 渲染首页的最近观看（固定 12 条）。
func (handler *Handler) recent(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.HTML(http.StatusOK, "partials/dashboard_history.html", gin.H{"History": nil})
		return
	}
	records, _, err := handler.continueRecords(c, userID, 12, 0)
	if err != nil {
		c.HTML(http.StatusOK, "partials/dashboard_history.html", gin.H{"History": nil})
		return
	}
	handler.markNSFW(c.Request.Context(), records)
	c.HTML(http.StatusOK, "partials/dashboard_history.html", gin.H{"History": records, "HasMore": false, "BlurNSFW": true})
}

// markNSFW 根据 NSFW 关键词标记记录。
func (handler *Handler) markNSFW(ctx context.Context, records []Record) {
	if handler.nsfwReader == nil || len(records) == 0 {
		return
	}
	keywords, err := handler.nsfwReader.NSFWKeywords(ctx)
	if err != nil || len(keywords) == 0 {
		return
	}
	lowerKW := make([]string, len(keywords))
	for i, kw := range keywords {
		lowerKW[i] = strings.ToLower(kw)
	}

	var needVod []VodKey
	for _, r := range records {
		if r.Genres == "" && r.Source != "" && r.VodID != "" {
			needVod = append(needVod, VodKey{SourceKey: r.Source, VodID: r.VodID})
		}
	}
	var vodTags map[VodKey]string
	if len(needVod) > 0 {
		vodTags, _ = handler.store.VodTags(ctx, needVod)
	}

	for i := range records {
		tags := strings.ToLower(records[i].Genres)
		if tags == "" && vodTags != nil {
			tags = strings.ToLower(vodTags[VodKey{SourceKey: records[i].Source, VodID: records[i].VodID}])
		}
		if tags == "" {
			continue
		}
		for _, kw := range lowerKW {
			if strings.Contains(tags, kw) {
				records[i].NSFW = true
				break
			}
		}
	}
}

// continueRecords 取「继续观看」并做合并去重，再在内存里分页。
func (handler *Handler) continueRecords(c *gin.Context, userID, limit, offset int) ([]Record, int, error) {
	// 浏览器最多保存 100 条本地记录，因此此有界读取足以覆盖正常用户数据；
	// 首页和仪表盘使用同一合并规则，超过上限时 HasMore 仍为 true。
	const mergeWindow = 1000
	all, err := handler.store.ListContinue(c.Request.Context(), userID, mergeWindow, 0)
	if err != nil {
		return nil, 0, err
	}
	merged := MergeContinue(all)
	count := len(merged)
	if len(all) == mergeWindow {
		count++
	}
	if offset >= len(merged) {
		return []Record{}, count, nil
	}
	end := offset + limit
	if end > len(merged) {
		end = len(merged)
	}
	return merged[offset:end], count, nil
}

// remove 删除一条记录。它走的也是同步接口（构造一条 delete 操作），
// 这样删除动作同样会进事件账本，其他设备下次同步就能收到。
func (handler *Handler) remove(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	userID := auth.UserID(c)
	if userID == 0 {
		respond(c, http.StatusUnauthorized, "未登录", nil, false)
		return
	}
	if id <= 0 {
		respond(c, http.StatusBadRequest, "无效的记录 ID", nil, false)
		return
	}
	now := handler.now().UTC()
	_, err := handler.store.SyncV2(c.Request.Context(), userID, SyncV2Request{
		DeviceID: "server-dashboard-delete",
		Cursor:   int64(^uint64(0) >> 1),
		Operations: []SyncOperation{{
			OperationID: "dashboard-delete-" + itoa(userID) + "-" + itoa(id) + "-" + strconv.FormatInt(now.UnixNano(), 10),
			Type:        "delete", HistoryID: id, Season: 1, EpisodeKey: "S01E01", OccurredAt: now, force: true,
		}},
	}, now)
	if err != nil {
		requestmeta.Logger(c.Request.Context()).Warn("history delete failed", "user_id", userID, "history_id", id, "error", err)
		respond(c, http.StatusInternalServerError, "删除失败", nil, false)
		return
	}
	if c.GetHeader("HX-Request") == "true" {
		c.Status(http.StatusOK)
		return
	}
	respond(c, http.StatusOK, "success", nil, true)
}

// respond 统一的接口返回格式。
func respond(c *gin.Context, status int, message string, data any, success bool) {
	c.JSON(status, gin.H{"code": status, "message": message, "data": data, "success": success})
}
