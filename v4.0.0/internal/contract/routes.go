// Package contract 记录新系统对外提供的最终 HTTP 契约，包内刻意不包含 Handler 实现。
package contract

type Surface string

const (
	SurfaceOperational      Surface = "operational"
	SurfacePublicPage       Surface = "public_page"
	SurfaceAuth             Surface = "auth"
	SurfaceDashboard        Surface = "dashboard"
	SurfacePublicAPI        Surface = "public_api"
	SurfaceHTMX             Surface = "htmx"
	SurfaceAuthenticatedAPI Surface = "authenticated_api"
	SurfaceAdmin            Surface = "admin"
)

type Route struct {
	Method  string
	Path    string
	Surface Surface
}

// Routes 是上线后唯一允许暴露的路由清单。
// 页面路径继续保留 SEO 与用户习惯，已废弃的客户端 API 不再纳入契约。
var Routes = []Route{
	{Method: "GET", Path: "/health", Surface: SurfaceOperational},
	{Method: "GET", Path: "/ready", Surface: SurfaceOperational},

	{Method: "GET", Path: "/", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/search", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/player", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/iptv", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/trends", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/feedback", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/tvbox", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/about", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/advertise", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/changelog", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/dmca", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/copyright-restricted", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/privacy", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/terms", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/sitemap.xml", Surface: SurfaceOperational},
	{Method: "GET", Path: "/robots.txt", Surface: SurfaceOperational},
	{Method: "GET", Path: "/monoo-verify.txt", Surface: SurfaceOperational},
	{Method: "GET", Path: "/movie/:id", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/play/:source_key/:vod_id", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/watch/:douban_id", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/discover", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/discover/:type", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/foryou", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/recommend", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/cinema", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/similar/:douban_id", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/user/:user_id", Surface: SurfacePublicPage},
	{Method: "GET", Path: "/user/:user_id/monthly/:year_month", Surface: SurfacePublicPage},

	{Method: "GET", Path: "/auth/login", Surface: SurfaceAuth},
	{Method: "POST", Path: "/auth/login", Surface: SurfaceAuth},
	{Method: "GET", Path: "/auth/register", Surface: SurfaceAuth},
	{Method: "POST", Path: "/auth/register", Surface: SurfaceAuth},
	{Method: "GET", Path: "/auth/logout", Surface: SurfaceAuth},

	{Method: "GET", Path: "/dashboard", Surface: SurfaceDashboard},
	{Method: "GET", Path: "/dashboard/settings", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/email", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/username", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/password", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/share", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/avatar", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/douban/bind", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/douban/unbind", Surface: SurfaceDashboard},
	{Method: "POST", Path: "/dashboard/settings/douban/sync", Surface: SurfaceDashboard},

	{Method: "GET", Path: "/api/tvbox.json", Surface: SurfacePublicAPI},
	{Method: "GET", Path: "/api/vod", Surface: SurfacePublicAPI},
	{Method: "POST", Path: "/api/user-movies/:id/wish", Surface: SurfaceHTMX},
	{Method: "POST", Path: "/api/user-movies/:id/watched", Surface: SurfaceHTMX},
	{Method: "DELETE", Path: "/api/user-movies/:id", Surface: SurfaceHTMX},
	{Method: "PATCH", Path: "/api/user-movies/:id", Surface: SurfaceHTMX},
	{Method: "POST", Path: "/api/feedback", Surface: SurfacePublicAPI},
	{Method: "POST", Path: "/api/v2/history/sync", Surface: SurfaceAuthenticatedAPI},
	{Method: "DELETE", Path: "/api/v2/history/:id", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/v2/media/suggest", Surface: SurfacePublicAPI},
	{Method: "POST", Path: "/api/v2/media/:media_id/refresh", Surface: SurfaceAuthenticatedAPI},
	{Method: "GET", Path: "/api/proxy/image/:url", Surface: SurfacePublicAPI},
	{Method: "GET", Path: "/api/htmx/similar-with-reason/:douban_id", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/search", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/v2/search", Surface: SurfacePublicAPI},
	{Method: "GET", Path: "/api/htmx/similar", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/foryou", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/reviews", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/movie-comments", Surface: SurfaceHTMX},
	{Method: "POST", Path: "/api/comments/:id/like", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/comments/:id/replies", Surface: SurfaceHTMX},
	{Method: "POST", Path: "/api/comments/:id/replies", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/movie-backdrops", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/user-movie/edit", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/user-movie/mark-watched", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/user-movie/buttons", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/feedback-list", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/dashboard/wish", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/dashboard/watched", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/dashboard/history", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/history/recent", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/history/today-updates", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/dashboard/feedback", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/public/:user_id/wish", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/public/:user_id/watched", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/douban-card", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/htmx/douban-sync-status", Surface: SurfaceHTMX},
	{Method: "GET", Path: "/api/danmaku", Surface: SurfacePublicAPI},
	{Method: "POST", Path: "/api/danmaku", Surface: SurfaceAuthenticatedAPI},
	{Method: "GET", Path: "/api/watch/resolve", Surface: SurfacePublicAPI},
	{Method: "GET", Path: "/api/v2/media/:id/resources", Surface: SurfacePublicAPI},
	{Method: "GET", Path: "/api/v2/media-units/:unit_id/playback-candidates", Surface: SurfacePublicAPI},
	{Method: "POST", Path: "/api/v2/playback/events", Surface: SurfacePublicAPI},

	{Method: "GET", Path: "/admin", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/users", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/feedback", Surface: SurfaceAdmin},
	{Method: "PUT", Path: "/admin/users/:id/role", Surface: SurfaceAdmin},
	{Method: "DELETE", Path: "/admin/users/:id", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/sites", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/sites", Surface: SurfaceAdmin},
	{Method: "PUT", Path: "/admin/sites/:id", Surface: SurfaceAdmin},
	{Method: "DELETE", Path: "/admin/sites/:id", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/sites/:id/test", Surface: SurfaceAdmin},
	{Method: "PUT", Path: "/admin/feedback/:id/status", Surface: SurfaceAdmin},
	{Method: "PUT", Path: "/admin/feedback/:id/reply", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/data", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/data/clean", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/data/retire-preview", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/data/retire", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/data/restore", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/jobs", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/matches", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/matches/decision", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/api/v2/admin/media-matches", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/api/v2/admin/media-matches/:id/resolve", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/api/v2/admin/metrics", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/monthly-report/generate", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/copyright", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/copyright", Surface: SurfaceAdmin},
	{Method: "PUT", Path: "/admin/copyright/:id", Surface: SurfaceAdmin},
	{Method: "DELETE", Path: "/admin/copyright/:id", Surface: SurfaceAdmin},
	{Method: "GET", Path: "/admin/category", Surface: SurfaceAdmin},
	{Method: "POST", Path: "/admin/category", Surface: SurfaceAdmin},
	{Method: "DELETE", Path: "/admin/category/:id", Surface: SurfaceAdmin},
}
