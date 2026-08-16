package contract

// InputLocation 标识输入位于 HTTP 请求的哪个外部可见位置。
// FormOrQuery 用于页面表单与 HTMX 查询共用的 Handler。
type InputLocation string

const (
	InputQuery       InputLocation = "query"
	InputForm        InputLocation = "form"
	InputFormOrQuery InputLocation = "form_or_query"
	InputJSON        InputLocation = "json"
	InputHeader      InputLocation = "header"
)

// RequestInput 独立于方法和路径清单，固化请求字段名与默认值。
// JSON 数组成员在字段名中使用 [] 表示。
type RequestInput struct {
	Method   string
	Path     string
	Name     string
	Location InputLocation
	Default  string
}

// RequestInputs 记录最终系统实际读取的非路径输入。
// 路径参数仍以 Routes 中的定义为准。
var RequestInputs = []RequestInput{
	{Method: "GET", Path: "/search", Name: "kw", Location: InputQuery},
	{Method: "GET", Path: "/search", Name: "doubanId", Location: InputQuery},
	{Method: "GET", Path: "/search", Name: "bypass", Location: InputQuery},
	{Method: "GET", Path: "/movie/:id", Name: "title", Location: InputQuery},
	{Method: "GET", Path: "/play/:source_key/:vod_id", Name: "douban_id", Location: InputQuery},
	{Method: "GET", Path: "/play/:source_key/:vod_id", Name: "ep", Location: InputQuery},
	{Method: "GET", Path: "/play/:source_key/:vod_id", Name: "source", Location: InputQuery},
	{Method: "GET", Path: "/player", Name: "url", Location: InputQuery},
	{Method: "GET", Path: "/player", Name: "embed", Location: InputQuery},
	{Method: "GET", Path: "/copyright-restricted", Name: "title", Location: InputQuery},
	{Method: "GET", Path: "/discover", Name: "HX-Request", Location: InputHeader},
	{Method: "GET", Path: "/discover/:type", Name: "HX-Request", Location: InputHeader},

	{Method: "GET", Path: "/auth/login", Name: "redirect", Location: InputQuery},
	{Method: "POST", Path: "/auth/login", Name: "email", Location: InputForm},
	{Method: "POST", Path: "/auth/login", Name: "password", Location: InputForm},
	{Method: "POST", Path: "/auth/login", Name: "redirect", Location: InputForm, Default: "/"},
	{Method: "POST", Path: "/auth/register", Name: "email", Location: InputForm},
	{Method: "POST", Path: "/auth/register", Name: "password", Location: InputForm},
	{Method: "POST", Path: "/auth/register", Name: "confirm_password", Location: InputForm},

	{Method: "GET", Path: "/dashboard/settings", Name: "success", Location: InputQuery},
	{Method: "POST", Path: "/dashboard/settings/email", Name: "email", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/username", Name: "username", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/password", Name: "current_password", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/password", Name: "new_password", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/password", Name: "confirm_password", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/share", Name: "is_public", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/avatar", Name: "avatar", Location: InputForm},
	{Method: "POST", Path: "/dashboard/settings/douban/bind", Name: "douban_user_id", Location: InputForm},

	{Method: "GET", Path: "/api/vod", Name: "ac", Location: InputQuery},
	{Method: "GET", Path: "/api/vod", Name: "ids", Location: InputQuery},
	{Method: "GET", Path: "/api/vod", Name: "wd", Location: InputQuery},
	{Method: "GET", Path: "/api/vod", Name: "t", Location: InputQuery},
	{Method: "GET", Path: "/api/vod", Name: "pg", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/tvbox.json", Name: "Host", Location: InputHeader},
	{Method: "GET", Path: "/api/tvbox.json", Name: "X-Forwarded-Proto", Location: InputHeader},
	{Method: "GET", Path: "/api/vod", Name: "Host", Location: InputHeader},
	{Method: "GET", Path: "/api/vod", Name: "X-Forwarded-Proto", Location: InputHeader},

	{Method: "POST", Path: "/api/user-movies/:id/wish", Name: "title", Location: InputQuery},
	{Method: "POST", Path: "/api/user-movies/:id/wish", Name: "poster", Location: InputQuery},
	{Method: "POST", Path: "/api/user-movies/:id/wish", Name: "year", Location: InputQuery},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Name: "title", Location: InputFormOrQuery},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Name: "poster", Location: InputFormOrQuery},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Name: "year", Location: InputFormOrQuery},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Name: "rating", Location: InputFormOrQuery, Default: "0"},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Name: "comment", Location: InputFormOrQuery},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Name: "variant", Location: InputFormOrQuery},
	{Method: "DELETE", Path: "/api/user-movies/:id", Name: "title", Location: InputQuery},
	{Method: "DELETE", Path: "/api/user-movies/:id", Name: "poster", Location: InputQuery},
	{Method: "DELETE", Path: "/api/user-movies/:id", Name: "year", Location: InputQuery},
	{Method: "DELETE", Path: "/api/user-movies/:id", Name: "source", Location: InputQuery},
	{Method: "DELETE", Path: "/api/user-movies/:id", Name: "variant", Location: InputFormOrQuery},
	{Method: "PATCH", Path: "/api/user-movies/:id", Name: "rating", Location: InputForm, Default: "0"},
	{Method: "PATCH", Path: "/api/user-movies/:id", Name: "comment", Location: InputForm},
	{Method: "POST", Path: "/api/feedback", Name: "content", Location: InputForm},
	{Method: "POST", Path: "/api/feedback", Name: "type", Location: InputForm},
	{Method: "POST", Path: "/api/feedback", Name: "movie_url", Location: InputForm},
	{Method: "DELETE", Path: "/api/v2/history/:id", Name: "HX-Request", Location: InputHeader},

	{Method: "POST", Path: "/api/v2/history/sync", Name: "device_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "cursor", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].operation_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].device_seq", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].type", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].history_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].media_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].media_unit_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].douban_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].vod_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].source_key", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].title", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].poster", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].episode", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].season_number", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].episode_key", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].position_seconds", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].duration_seconds", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].progress_percent", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/history/sync", Name: "operations[].occurred_at", Location: InputJSON},

	{Method: "GET", Path: "/api/v2/media/suggest", Name: "q", Location: InputQuery},
	{Method: "GET", Path: "/api/proxy/image/:url", Name: "Referer", Location: InputHeader},
	{Method: "GET", Path: "/api/htmx/search", Name: "q", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/search", Name: "bypass", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/search", Name: "year", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/search", Name: "type", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/search", Name: "limit", Location: InputQuery, Default: "20"},
	{Method: "GET", Path: "/api/v2/search", Name: "q", Location: InputQuery},
	{Method: "GET", Path: "/api/v2/search", Name: "bypass", Location: InputQuery},
	{Method: "GET", Path: "/api/v2/search", Name: "year", Location: InputQuery},
	{Method: "GET", Path: "/api/v2/search", Name: "type", Location: InputQuery},
	{Method: "GET", Path: "/api/v2/search", Name: "limit", Location: InputQuery, Default: "20"},
	{Method: "GET", Path: "/api/htmx/similar", Name: "douban_id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/similar", Name: "id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/foryou", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/reviews", Name: "douban_id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/movie-comments", Name: "douban_id", Location: InputQuery},
	{Method: "POST", Path: "/api/comments/:id/replies", Name: "content", Location: InputForm},
	{Method: "GET", Path: "/api/htmx/movie-backdrops", Name: "douban_id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/edit", Name: "id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/mark-watched", Name: "douban_id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/mark-watched", Name: "title", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/mark-watched", Name: "poster", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/mark-watched", Name: "year", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/mark-watched", Name: "variant", Location: InputFormOrQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Name: "douban_id", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Name: "title", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Name: "poster", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Name: "year", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Name: "variant", Location: InputFormOrQuery},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Name: "redirect", Location: InputQuery},
	{Method: "GET", Path: "/api/htmx/feedback-list", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/feedback-list", Name: "type", Location: InputQuery},

	{Method: "GET", Path: "/api/htmx/dashboard/wish", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/dashboard/watched", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/dashboard/history", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/dashboard/feedback", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/public/:user_id/wish", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/public/:user_id/watched", Name: "page", Location: InputQuery, Default: "1"},
	{Method: "GET", Path: "/api/htmx/douban-card", Name: "kw", Location: InputQuery},

	{Method: "GET", Path: "/api/danmaku", Name: "title", Location: InputQuery},
	{Method: "GET", Path: "/api/danmaku", Name: "episode", Location: InputQuery},
	{Method: "POST", Path: "/api/danmaku", Name: "title", Location: InputJSON},
	{Method: "POST", Path: "/api/danmaku", Name: "episode", Location: InputJSON},
	{Method: "POST", Path: "/api/danmaku", Name: "text", Location: InputJSON},
	{Method: "POST", Path: "/api/danmaku", Name: "time", Location: InputJSON},
	{Method: "POST", Path: "/api/danmaku", Name: "mode", Location: InputJSON},
	{Method: "POST", Path: "/api/danmaku", Name: "color", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "attempt_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "candidate_session_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "event_type", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "candidate_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "media_unit_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "source_key", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "vod_id", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "elapsed_ms", Location: InputJSON},
	{Method: "POST", Path: "/api/v2/playback/events", Name: "reason", Location: InputJSON},

	{Method: "PUT", Path: "/admin/users/:id/role", Name: "role", Location: InputForm},
	{Method: "POST", Path: "/admin/sites", Name: "key", Location: InputForm},
	{Method: "POST", Path: "/admin/sites", Name: "base_url", Location: InputForm},
	{Method: "POST", Path: "/admin/sites", Name: "enabled", Location: InputForm},
	{Method: "PUT", Path: "/admin/sites/:id", Name: "key", Location: InputForm},
	{Method: "PUT", Path: "/admin/sites/:id", Name: "base_url", Location: InputForm},
	{Method: "PUT", Path: "/admin/sites/:id", Name: "enabled", Location: InputForm},
	{Method: "GET", Path: "/admin/sites/:id/test", Name: "keyword", Location: InputQuery, Default: "肖申克的救赎"},
	{Method: "GET", Path: "/admin/feedback", Name: "status", Location: InputQuery},
	{Method: "PUT", Path: "/admin/feedback/:id/status", Name: "status", Location: InputForm},
	{Method: "PUT", Path: "/admin/feedback/:id/reply", Name: "reply", Location: InputForm},
	{Method: "POST", Path: "/admin/monthly-report/generate", Name: "user_id", Location: InputForm},
	{Method: "POST", Path: "/admin/monthly-report/generate", Name: "year_month", Location: InputForm, Default: "previous month"},
	{Method: "POST", Path: "/admin/copyright", Name: "keyword", Location: InputForm},
	{Method: "PUT", Path: "/admin/copyright/:id", Name: "keyword", Location: InputForm},
	{Method: "POST", Path: "/admin/category", Name: "keyword", Location: InputForm},
}
