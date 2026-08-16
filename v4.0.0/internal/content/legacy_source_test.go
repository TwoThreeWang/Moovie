package content

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestTemplateInventoryMatchesLegacySource(t *testing.T) {
	newRoot := filepath.Join("..", "..", "web", "templates")
	legacyRoot := filepath.Join("..", "..", "..", "web", "templates")
	for _, directory := range []string{"pages", "partials"} {
		newFiles := relativeFiles(t, filepath.Join(newRoot, directory))
		legacyFiles := relativeFiles(t, filepath.Join(legacyRoot, directory))
		if directory == "pages" {
			legacyFiles = removeStrings(legacyFiles, "square.html")
			legacyFiles = append(legacyFiles, "admin_jobs.html", "admin_matches.html", "watch.html")
			legacyFiles = append(legacyFiles, "cinema.html")
			sort.Strings(legacyFiles)
		} else if directory == "partials" {
			legacyFiles = removeStrings(legacyFiles, "search_results.html", "square_activity.html", "square_grid.html", "square_leaderboard.html")
			legacyFiles = append(legacyFiles, "unified_search_results.html")
			// 追剧更新时间是新系统独有能力，旧站没有对应 partial 可比对。
			legacyFiles = append(legacyFiles, "air_schedule.html", "today_updates.html")
			sort.Strings(legacyFiles)
		}
		if strings.Join(newFiles, "\n") != strings.Join(legacyFiles, "\n") {
			t.Fatalf("%s template inventory drift:\nlegacy=%v\nnew=%v", directory, legacyFiles, newFiles)
		}
	}
}

func TestStaticAssetInventoryOnlyAddsReviewedCSRFRuntime(t *testing.T) {
	newRoot := filepath.Join("..", "..", "web", "static")
	legacyRoot := filepath.Join("..", "..", "..", "web", "static")
	newFiles := relativeFiles(t, newRoot)
	legacyFiles := relativeFiles(t, legacyRoot)
	legacyFiles = removeStrings(legacyFiles, ".DS_Store")
	wanted := append(append([]string(nil), legacyFiles...), "js/csrf.js")
	sort.Strings(wanted)
	if strings.Join(newFiles, "\n") != strings.Join(wanted, "\n") {
		t.Fatalf("static asset inventory drift:\nwant=%v\nnew=%v", wanted, newFiles)
	}
}

func TestEveryPageAndPartialTemplateMatchesLegacySource(t *testing.T) {
	newTemplates := filepath.Join("..", "..", "web", "templates")
	legacyTemplates := filepath.Join("..", "..", "..", "web", "templates")
	for _, directory := range []string{"pages", "partials"} {
		err := filepath.WalkDir(filepath.Join(newTemplates, directory), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
				return nil
			}
			relative, err := filepath.Rel(newTemplates, path)
			if err != nil {
				return err
			}
			if isReviewedHTMXLoadingFile(relative) || relative == "pages/admin_matches.html" || relative == "partials/unified_search_results.html" {
				return nil
			}
			if isReviewedTemplateDrift(relative) {
				return nil
			}
			legacy := readFile(t, filepath.Join(legacyTemplates, relative))
			refactored := normalizeReviewedPlaceholder(readFile(t, path))
			if !bytes.Equal(legacy, refactored) {
				t.Errorf("%s drifted from the frozen legacy source", relative)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestFrozenPublicFilesMatchLegacySource(t *testing.T) {
	newWebRoot := filepath.Join("..", "..", "web")
	legacyWebRoot := filepath.Join("..", "..", "..", "web")
	files := []string{
		"templates/pages/home.html",
		"templates/pages/search.html",
		"templates/pages/trends.html",
		"templates/pages/about.html",
		"templates/pages/advertise.html",
		"templates/pages/changelog.html",
		"templates/pages/dmca.html",
		"templates/pages/copyright_restricted.html",
		"templates/pages/privacy.html",
		"templates/pages/terms.html",
		"templates/pages/404.html",
		"templates/pages/player.html",
		"templates/pages/player_embed.html",
		"templates/pages/iptv.html",
		"templates/pages/tvbox.html",
		"templates/pages/play.html",
		"templates/pages/login.html",
		"templates/pages/register.html",
		"templates/pages/dashboard.html",
		"templates/pages/settings.html",
		"templates/pages/movie.html",
		"templates/pages/fetching.html",
		"templates/pages/recommendations.html",
		"templates/pages/foryou.html",
		"templates/pages/share.html",
		"templates/pages/share_monthly.html",
		"templates/pages/feedback.html",
		"templates/pages/admin_feedback.html",
		"templates/pages/discover.html",
		"templates/pages/admin_dashboard.html",
		"templates/pages/admin_users.html",
		"templates/pages/admin_sites.html",
		"templates/pages/admin_cache.html",
		"templates/pages/admin_copyright.html",
		"templates/pages/admin_category.html",
		"templates/partials/play_disclaimer.html",
		"templates/partials/play_watched_button.html",
		"templates/partials/user_movie_buttons.html",
		"templates/partials/user_movie_edit_form.html",
		"templates/partials/user_movie_mark_watched_form.html",
		"templates/partials/dashboard_wish.html",
		"templates/partials/dashboard_wish_grid.html",
		"templates/partials/dashboard_watched.html",
		"templates/partials/dashboard_watched_grid.html",
		"templates/partials/dashboard_watched_item.html",
		"templates/partials/dashboard_history.html",
		"templates/partials/dashboard_history_grid.html",
		"templates/partials/movie_backdrops.html",
		"templates/partials/reviews.html",
		"templates/partials/similar_movies.html",
		"templates/partials/similar_movies_with_reasons.html",
		"templates/partials/foryou_movies.html",
		"templates/partials/foryou_movies_grid.html",
		"templates/partials/douban_sync_status.html",
		"templates/partials/public_wish_grid.html",
		"templates/partials/public_watched_grid.html",
		"templates/partials/movie_user_comments.html",
		"templates/partials/comment_like_button.html",
		"templates/partials/comment_replies.html",
		"templates/partials/feedback_list.html",
		"templates/partials/dashboard_feedback.html",
		"templates/partials/discover_grid.html",
		"templates/partials/douban_card.html",
		"static/css/style.css",
		"static/img/logo.png",
		"static/img/moovie-app.png",
		"static/img/placeholder.svg",
		"static/js/app.js",
		"static/js/hls.min.js",
		"static/js/htmx.min.js",
		"static/js/player.js",
		"static/manifest.json",
		"static/sw.js",
	}

	for _, relativePath := range files {
		t.Run(relativePath, func(t *testing.T) {
			if isReviewedHTMXLoadingFile(relativePath) || isReviewedTemplateDrift(relativePath) {
				return
			}
			legacy := readFile(t, filepath.Join(legacyWebRoot, relativePath))
			refactored := normalizeReviewedPlaceholder(readFile(t, filepath.Join(newWebRoot, relativePath)))
			if !bytes.Equal(legacy, refactored) {
				t.Fatalf("%s drifted from the frozen legacy source", relativePath)
			}
		})
	}
}

var reviewedHTMXLoadingFiles = map[string]bool{
	"pages/home.html":                            true,
	"pages/discover.html":                        true,
	"pages/foryou.html":                          true,
	"pages/login.html":                           true,
	"pages/movie.html":                           true,
	"pages/play.html":                            true,
	"pages/register.html":                        true,
	"pages/recommendations.html":                 true,
	"pages/trends.html":                          true,
	"pages/watch.html":                           true,
	"partials/dashboard_history.html":            true,
	"partials/dashboard_history_grid.html":       true,
	"partials/dashboard_watched.html":            true,
	"partials/dashboard_watched_grid.html":       true,
	"partials/dashboard_wish.html":               true,
	"partials/dashboard_wish_grid.html":          true,
	"partials/discover_grid.html":                true,
	"partials/douban_card.html":                  true,
	"partials/foryou_movies.html":                true,
	"partials/foryou_movies_grid.html":           true,
	"partials/movie_backdrops.html":              true,
	"partials/public_watched_grid.html":          true,
	"partials/public_wish_grid.html":             true,
	"partials/similar_movies.html":               true,
	"partials/user_movie_edit_form.html":         true,
	"partials/user_movie_mark_watched_form.html": true,
	"pages/dashboard.html":                       true,
	"pages/feedback.html":                        true,
	"pages/admin_feedback.html":                  true,
	"pages/search.html":                          true,
	"pages/settings.html":                        true,
	"pages/cinema.html":                          true,
	"pages/admin_dashboard.html":                 true,
	"pages/admin_users.html":                     true,
	"pages/admin_sites.html":                     true,
	"pages/admin_cache.html":                     true,
	"pages/admin_copyright.html":                 true,
	"pages/admin_category.html":                  true,
	"pages/admin_jobs.html":                      true,
	"pages/advertise.html":                       true,
	"pages/iptv.html":                            true,
	"static/css/style.css":                       true,
	"static/js/app.js":                           true,
	"static/js/player.js":                        true,
}

func TestEveryAdminTabBarLinksOperationalPages(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "web", "templates", "pages", "admin_*.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		contents := string(readFile(t, path))
		start := strings.Index(contents, `<nav class="admin-tabs">`)
		if start < 0 {
			continue
		}
		end := strings.Index(contents[start:], `</nav>`)
		if end < 0 {
			t.Errorf("%s has an unclosed admin tab bar", filepath.Base(path))
			continue
		}
		tabs := contents[start : start+end]
		if !strings.Contains(tabs, `href="/admin/matches"`) || !strings.Contains(tabs, `href="/admin/jobs"`) {
			t.Errorf("%s admin tabs missing operational links", filepath.Base(path))
		}
	}
}

func TestEveryTemplateImageHasPlaceholderFallback(t *testing.T) {
	const fallback = `onerror="this.onerror=null;this.src='/static/img/placeholder.svg'"`
	imageTag := regexp.MustCompile(`(?s)<img\b[^>]*>`)
	root := filepath.Join("..", "..", "web", "templates")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			return nil
		}
		for _, tag := range imageTag.FindAllString(string(readFile(t, path)), -1) {
			if !strings.Contains(tag, fallback) {
				t.Errorf("%s image missing placeholder fallback: %s", path, tag)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDynamicImagesHavePlaceholderFallback(t *testing.T) {
	webRoot := filepath.Join("..", "..", "web")
	wants := map[string][]string{
		"templates/pages/home.html":               {`image.onerror = function()`},
		"templates/pages/iptv.html":               {`img.onerror = function()`, `nowLogo.onerror = function()`},
		"templates/pages/share_monthly.html":      {`img.onerror = function()`},
		"templates/partials/movie_backdrops.html": {`im.onerror = function()`, `img.onerror = function()`},
		"static/js/app.js":                        {`image.onerror = function()`},
	}
	for path, snippets := range wants {
		contents := string(readFile(t, filepath.Join(webRoot, path)))
		for _, snippet := range snippets {
			if !strings.Contains(contents, snippet) {
				t.Errorf("%s dynamic image missing fallback %q", path, snippet)
			}
		}
	}
}

func isReviewedHTMXLoadingFile(relativePath string) bool {
	normalized := strings.TrimPrefix(filepath.ToSlash(relativePath), "templates/")
	return reviewedHTMXLoadingFiles[normalized]
}

// reviewedTemplateDrift 列出经过审阅、允许与冻结源不一致的模板。
// changelog.html 承载发布公告（v4.0.0 条目），内容更新本就该与旧站分叉，
// 不属于重构漂移；air_schedule 与 today_updates 是追剧更新时间的新增 partial，
// 旧站不存在同名文件，没有可比对的冻结源。
var reviewedTemplateDrift = map[string]bool{
	"pages/changelog.html":        true,
	"partials/air_schedule.html":  true,
	"partials/today_updates.html": true,
}

func isReviewedTemplateDrift(relativePath string) bool {
	normalized := strings.TrimPrefix(filepath.ToSlash(relativePath), "templates/")
	return reviewedTemplateDrift[normalized]
}

// normalizeReviewedPlaceholder 把海报加载失败时回退到占位图的增强还原成旧站写法。
// 旧站是直接隐藏图片，新站显示占位图；这与 base.html 中同源的 onerror 例外是
// 同一次可访问性审阅的产物，因此在比对前统一抹平而不是逐个文件豁免。
func normalizeReviewedPlaceholder(contents []byte) []byte {
	normalized := strings.ReplaceAll(string(contents),
		`onerror="this.onerror=null;this.src='/static/img/placeholder.svg'"`,
		`onerror="this.style.display='none'"`)
	normalized = strings.ReplaceAll(normalized,
		"            img.onerror = function() { this.onerror = null; this.src = '/static/img/placeholder.svg'; };\n", "")
	return []byte(normalized)
}

func TestReviewedHTMXLoadingEnhancements(t *testing.T) {
	webRoot := filepath.Join("..", "..", "web")
	wants := map[string][]string{
		"templates/pages/dashboard.html": {
			`hx-indicator="#dashboard-tab-loading"`,
			`id="dashboard-tab-loading"`,
			`class="htmx-skeleton-grid"`,
		},
		"templates/pages/cinema.html": {
			`<h1>片场</h1>`,
			`本周放映单`,
			`值得停一下的短评`,
			`片友雷达`,
		},
		"templates/pages/feedback.html": {
			`hx-indicator="#feedback-list-loading"`,
			`id="feedback-list-loading"`,
			`hx-disabled-elt="find button[type='submit']"`,
		},
		"templates/pages/search.html": {
			`hx-indicator="#douban-card-loading"`,
			`id="douban-card-loading"`,
			`class="htmx-skeleton-poster"`,
			`hx-get="/api/htmx/search?q=`,
		},
		"templates/pages/watch.html": {
			`auto_failover: {{ if .AutoFailoverEnabled }}true{{ else }}false{{ end }}`,
			`poster: '{{ proxyImg .View.Poster }}'`,
		},
		"templates/pages/play.html": {
			`poster: '{{ proxyImg .View.Poster }}'`,
		},
		"templates/pages/admin_dashboard.html": {
			`href="/admin/matches"`,
			`<span>匹配复核</span>`,
		},
		"static/css/style.css": {
			`.htmx-panel-indicator.htmx-request`,
			`@keyframes htmx-skeleton-shimmer`,
			`@media (prefers-reduced-motion: reduce)`,
			`.load-more-container`,
		},
		"static/js/player.js": {
			`function failoverToHealthyEpisode(options)`,
			`var MAX_AUTOMATIC_FAILOVERS = 2`,
			`var MIN_AUTOMATIC_MAPPING_CONFIDENCE = 0.90`,
			`payload.auto_failover_enabled !== true`,
			`Number(payload.unit_id) !== Number(options.media_unit_id)`,
			`/api/v2/media-units/`,
			`media_unit_id: context.media_unit_id`,
			`candidate_session_id: context.candidate_session_id || ''`,
			`sessionStorage.removeItem('moovie_failover:unit:'`,
			`event_type: eventType`,
		},
		"static/js/app.js": {
			`container.replaceChildren(...rows)`,
			`title.textContent = item.title || ''`,
			`container.replaceChildren(...tags)`,
		},
		"templates/pages/home.html": {
			`container.replaceChildren(...cards)`,
			`title.textContent = item.title || '未知'`,
		},
		"templates/partials/movie_backdrops.html": {
			`img.removeAttribute('src')`,
		},
		"templates/pages/admin_sites.html": {
			`cell.textContent = value || '-'`,
			`tbody.replaceChildren(tr)`,
		},
		"templates/pages/iptv.html": {
			`error.replaceChildren(summary)`,
			`pre.textContent = detail`,
		},
		"templates/pages/admin_feedback.html": {
			`class="feedback-modal-overlay" role="dialog"`,
			`aria-labelledby="reply-dialog-title"`,
			`replyDialogTrigger = document.activeElement`,
		},
	}

	for relativePath, snippets := range wants {
		contents := string(readFile(t, filepath.Join(webRoot, relativePath)))
		for _, snippet := range snippets {
			if !strings.Contains(contents, snippet) {
				t.Errorf("%s is missing reviewed HTMX loading contract %q", relativePath, snippet)
			}
		}
	}
}

func TestLayoutDiffIsLimitedToReviewedRuntimeExtensionPoints(t *testing.T) {
	newLayout := string(readFile(t, filepath.Join("..", "..", "web", "templates", "layouts", "base.html")))
	legacyLayout := string(readFile(t, filepath.Join("..", "..", "..", "web", "templates", "layouts", "base.html")))

	normalized := strings.Replace(newLayout,
		`<meta name="robots" content="{{ if .Robots }}{{ .Robots }}{{ else }}index, follow{{ end }}">`,
		`<meta name="robots" content="index, follow">`, 1)
	normalized = strings.Replace(normalized,
		`    {{ range .JSONLD }}<script type="application/ld+json">{{ . }}</script>{{ end }}
`, "", 1)
	normalized = stripMarkedBlock(normalized, "    <!-- csrf-runtime:start -->", "    <!-- csrf-runtime:end -->")
	normalized = strings.Replace(normalized,
		`<body hx-ext="preload" data-user-id="{{ if .UserInfo }}{{ .UserInfo.ID }}{{ end }}">`,
		`<body hx-ext="preload">`, 1)
	normalized = strings.Replace(normalized,
		`<script src="/static/js/app.js?v=0.6" defer></script>`,
		`<script src="/static/js/app.js?v=0.4" defer></script>`, 1)
	normalized = strings.Replace(normalized,
		`<link rel="stylesheet" href="/static/css/style.css?v=3.2">`,
		`<link rel="stylesheet" href="/static/css/style.css?v=2.8">`, 1)
	normalized = strings.Replace(normalized,
		`<a href="/cinema" class="nav-item {{ if eq .ActiveMenu "cinema" }}active{{ end }}">`,
		`<a href="/square" class="nav-item {{ if eq .ActiveMenu "square" }}active{{ end }}">`, 1)
	normalized = strings.Replace(normalized, `<span>片场</span>`, `<span>广场</span>`, 1)
	normalized = normalizeReviewedLayoutAccessibility(normalized)
	normalized = strings.ReplaceAll(normalized, ` onerror="this.onerror=null;this.src='/static/img/placeholder.svg'"`, "")
	// 页脚版本号随发布走，新旧两站各自准确即可，不要求同步。
	normalized = strings.ReplaceAll(normalized, "Moovie 影牛(v4.0.0)", "Moovie 影牛(v3.4.0)")
	if normalized != legacyLayout {
		t.Fatal("shared layout contains changes beyond the reviewed SEO, CSRF, history-sync and accessibility extension points")
	}
}

func normalizeReviewedLayoutAccessibility(layout string) string {
	replacements := [][2]string{
		{
			`<button type="button" class="nav-item theme-toggle" onclick="toggleTheme()" title="切换主题" aria-label="切换外观" style="width: 100%; text-align: left;">`,
			`<button class="nav-item theme-toggle" onclick="toggleTheme()" title="切换主题" style="width: 100%; text-align: left;">`,
		},
		{
			`<div class="sidebar-overlay" id="sidebarOverlay" onclick="toggleSidebar(false)" aria-hidden="true"></div>`,
			`<div class="sidebar-overlay" id="sidebarOverlay" onclick="toggleSidebar()"></div>`,
		},
		{
			`<button type="button" class="hamburger" id="mobile-menu-button" onclick="toggleSidebar()" aria-label="打开导航菜单" aria-controls="sidebar" aria-expanded="false">`,
			`<button class="hamburger" onclick="toggleSidebar()">`,
		},
		{
			`<a href="/dashboard" class="mobile-user" title="{{ .UserInfo.Username }}" aria-label="进入 {{ .UserInfo.Username }} 的个人中心">`,
			`<a href="/dashboard" class="mobile-user" title="{{ .UserInfo.Username }}">`,
		},
		{
			`<a href="/auth/login" class="mobile-user" aria-label="登录或注册">`,
			`<a href="/auth/login" class="mobile-user">`,
		},
		{
			`<button type="button" class="mobile-theme-toggle theme-toggle" onclick="toggleTheme()" aria-label="切换外观">`,
			`<button class="mobile-theme-toggle theme-toggle" onclick="toggleTheme()">`,
		},
		{
			`        function toggleSidebar(forceOpen) {
            const sidebar = document.getElementById('sidebar');
            const overlay = document.getElementById('sidebarOverlay');
            const menuButton = document.getElementById('mobile-menu-button');
            const shouldOpen = typeof forceOpen === 'boolean' ? forceOpen : !sidebar.classList.contains('open');
            sidebar.classList.toggle('open', shouldOpen);
            overlay.classList.toggle('open', shouldOpen);
            document.body.classList.toggle('sidebar-open', shouldOpen);
            menuButton.setAttribute('aria-expanded', shouldOpen ? 'true' : 'false');
            menuButton.setAttribute('aria-label', shouldOpen ? '关闭导航菜单' : '打开导航菜单');
        }`,
			`        function toggleSidebar() {
            document.getElementById('sidebar').classList.toggle('open');
            document.getElementById('sidebarOverlay').classList.toggle('open');
            document.body.classList.toggle('sidebar-open');
        }`,
		},
		{
			`            document.querySelectorAll('.theme-toggle').forEach(button => {
                button.setAttribute('aria-label', theme === 'dark' ? '切换为浅色外观' : '切换为深色外观');
                button.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
            });
`,
			``,
		},
		{
			`        document.addEventListener('keydown', event => {
            if (event.key === 'Escape' && document.getElementById('sidebar').classList.contains('open')) {
                toggleSidebar(false);
                document.getElementById('mobile-menu-button').focus();
            }
        });

`,
			``,
		},
	}
	for _, replacement := range replacements {
		layout = strings.Replace(layout, replacement[0], replacement[1], 1)
	}
	return layout
}

func TestReviewedFrontendQualityEnhancements(t *testing.T) {
	webRoot := filepath.Join("..", "..", "web")
	wants := map[string][]string{
		"templates/layouts/base.html": {
			`aria-controls="sidebar"`,
			`aria-label="登录或注册"`,
			`event.key === 'Escape'`,
		},
		"templates/pages/discover.html": {
			`class="tab-nav discover-tab-nav"`,
			`hx-get="/discover/{{ .CurrentType }}"`,
			`aria-current="page"`,
			`document.addEventListener('htmx:historyRestore', syncDiscoverActiveTab)`,
		},
		"templates/pages/login.html": {
			`autocomplete="email" inputmode="email"`,
			`autocomplete="current-password"`,
		},
		"templates/pages/register.html": {
			`autocomplete="email" inputmode="email"`,
			`autocomplete="new-password"`,
		},
		"templates/pages/movie.html": {
			`<details class="movie-cast-details">`,
			`class="rating-num rating-num-refresh"`,
		},
		"templates/pages/iptv.html": {
			`id="iptv-playback-status"`,
			`item.type = 'button'`,
			`id="iptv-retry-btn"`,
			`onPlaybackError: () => setPlaybackStatus`,
		},
		"templates/partials/user_movie_edit_form.html": {
			`role="dialog" aria-modal="true"`,
			`role="group" aria-labelledby="edit-rating-label"`,
			`aria-pressed=`,
		},
		"templates/partials/user_movie_mark_watched_form.html": {
			`role="dialog" aria-modal="true"`,
			`role="group" aria-labelledby="rating-label"`,
			`aria-pressed="false"`,
		},
		"static/js/player.js": {
			`typeof options.onPlaybackReady === 'function'`,
			`typeof options.onPlaybackError === 'function'`,
			`Number(options.loadTimeoutMs) || 30000`,
		},
		"static/js/app.js": {
			`var syncTimer = typeof syncTimer === 'undefined' ? null : syncTimer`,
			`window.__moovieAppGlobalListenersBound`,
			`document.addEventListener('htmx:historyRestore', initializeMoovieApp)`,
		},
	}
	for relativePath, snippets := range wants {
		contents := string(readFile(t, filepath.Join(webRoot, relativePath)))
		for _, snippet := range snippets {
			if !strings.Contains(contents, snippet) {
				t.Errorf("%s is missing reviewed frontend quality contract %q", relativePath, snippet)
			}
		}
	}

	forbidden := map[string][]string{
		"templates/pages/admin_feedback.html":    {`class="modal-overlay"`, `\n.modal-overlay {`, `\n.modal-content {`, `\n.modal-header {`},
		"templates/pages/discover.html":          {`<style>`, `href="javascript:void(0)"`},
		"templates/pages/foryou.html":            {`<center>`},
		"templates/pages/movie.html":             {`<style>`, `EmbeddingContent`, `Moovie 推荐语`},
		"templates/pages/play.html":              {`<center>`},
		"templates/pages/recommendations.html":   {`\n.page-title {`, `\n.page-subtitle {`, `\n.section-title {`},
		"templates/pages/search.html":            {`<style>`, `<center>`},
		"templates/partials/foryou_movies.html":  {`\n.section-title {`, `\n.section-header {`, `\n.htmx-indicator {`, `\n.btn-outline {`},
		"templates/partials/similar_movies.html": {`<style>`, `.similar-movies-grid {`},
		"static/js/app.js":                       {`console.log('[搜索建议]`, `const HISTORY_KEY`, `let syncTimer`},
	}
	for relativePath, snippets := range forbidden {
		contents := string(readFile(t, filepath.Join(webRoot, relativePath)))
		for _, snippet := range snippets {
			if strings.Contains(contents, snippet) {
				t.Errorf("%s still contains redundant or unsafe frontend fragment %q", relativePath, snippet)
			}
		}
	}

	css := string(readFile(t, filepath.Join(webRoot, "static", "css", "style.css")))
	if count := strings.Count(css, "@keyframes spin"); count != 1 {
		t.Errorf("style.css defines @keyframes spin %d times; want exactly one", count)
	}
	for _, selector := range []string{".admin-header {", ".stat-icon {"} {
		if count := strings.Count(css, "\n"+selector); count != 1 {
			t.Errorf("style.css defines %s %d times; want exactly one", selector, count)
		}
	}
	for _, forbiddenToken := range []string{
		"\n.modal-overlay {",
		"\n.modal-header {",
		"var(--bg-card)",
		"var(--accent)",
		"var(--bg-primary)",
		"var(--primary-rgb)",
		"var(--bg-hover)",
		"var(--shadow-sm)",
		"var(--warning-alpha-30)",
		"var(--warning-dark)",
	} {
		if strings.Contains(css, forbiddenToken) {
			t.Errorf("style.css still contains conflicting or undefined token %q", forbiddenToken)
		}
	}
}

func TestCSRFRuntimeLoadsBeforePageScriptsCanSendUnsafeRequests(t *testing.T) {
	layout := string(readFile(t, filepath.Join("..", "..", "web", "templates", "layouts", "base.html")))
	if !strings.Contains(layout, `<script src="/static/js/csrf.js?v=0.1"></script>`) {
		t.Fatal("CSRF fetch wrapper must execute during head parsing before playback scripts emit telemetry")
	}
	if strings.Contains(layout, `<script src="/static/js/csrf.js?v=0.1" defer`) {
		t.Fatal("deferred CSRF runtime races non-deferred page scripts")
	}
}

func TestHomePageDropsLegacyGamblingPromotion(t *testing.T) {
	home := string(readFile(t, filepath.Join("..", "..", "web", "templates", "pages", "home.html")))
	for _, forbidden := range []string{"新葡京", "澳门威尼斯人"} {
		if strings.Contains(home, forbidden) {
			t.Fatalf("home page still contains legacy gambling promotion %q", forbidden)
		}
	}
	if !strings.Contains(home, "电影、剧集、综艺与动漫，一站搜索") {
		t.Fatal("home page is missing the reviewed neutral search scope copy")
	}
}

func stripMarkedBlock(value, start, end string) string {
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		return value
	}
	endIndex := strings.Index(value[startIndex:], end)
	if endIndex < 0 {
		return value
	}
	endIndex += startIndex + len(end)
	if endIndex < len(value) && value[endIndex] == '\n' {
		endIndex++
	}
	return value[:startIndex] + value[endIndex:]
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return contents
}

func relativeFiles(t *testing.T, root string) []string {
	t.Helper()
	files := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func removeStrings(values []string, removed ...string) []string {
	blocked := make(map[string]bool, len(removed))
	for _, value := range removed {
		blocked[value] = true
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !blocked[value] {
			result = append(result, value)
		}
	}
	return result
}
