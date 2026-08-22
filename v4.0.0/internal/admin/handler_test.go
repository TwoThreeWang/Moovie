package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/feedback"
	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/operations"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestAdminPagesAndMutationsRequireRoleAndPreserveMainFlows(t *testing.T) {
	router, users, searchStore, feedbackStore, adminToken, userToken := adminTestRouter(t)
	guest := request(router, http.MethodGet, "/admin", "", true)
	if guest.Code != http.StatusFound || guest.Header().Get("Location") != "/auth/login?redirect=/admin" {
		t.Fatalf("guest = %d/%s", guest.Code, guest.Header().Get("Location"))
	}
	forbidden := request(router, http.MethodGet, "/admin", userToken, false)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "需要管理员权限") {
		t.Fatalf("forbidden = %d/%s", forbidden.Code, forbidden.Body.String())
	}
	for _, path := range []string{"/admin", "/admin/users", "/admin/sites", "/admin/data", "/admin/jobs", "/admin/matches", "/admin/copyright", "/admin/category"} {
		response := request(router, http.MethodGet, path, adminToken, false)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s = %d/%s", path, response.Code, response.Body.String())
		}
	}
	jobs := request(router, http.MethodGet, "/admin/jobs?status=running", adminToken, false)
	for _, expected := range []string{"统一 Worker 队列", "豆瓣精彩短评", "worker-test", "下一页"} {
		if jobs.Code != http.StatusOK || !strings.Contains(jobs.Body.String(), expected) {
			t.Fatalf("job queue missing %q: %d/%s", expected, jobs.Code, jobs.Body.String())
		}
	}
	pendingJobs := request(router, http.MethodGet, "/admin/jobs?status=pending", adminToken, false)
	if pendingJobs.Code != http.StatusOK || !strings.Contains(pendingJobs.Body.String(), "豆瓣账号同步") || strings.Contains(pendingJobs.Body.String(), "worker-test") {
		t.Fatalf("pending job queue = %d/%s", pendingJobs.Code, pendingJobs.Body.String())
	}
	metrics := request(router, http.MethodGet, "/api/v2/admin/metrics", adminToken, false)
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), `"window_hours":24`) || metrics.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("metrics = %d/%s", metrics.Code, metrics.Body.String())
	}
	metricsForbidden := request(router, http.MethodGet, "/api/v2/admin/metrics", userToken, false)
	if metricsForbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin metrics = %d/%s", metricsForbidden.Code, metricsForbidden.Body.String())
	}

	created := formRequest(router, http.MethodPost, "/admin/sites", url.Values{"key": {"demo"}, "base_url": {"https://source.example/api"}, "enabled": {"on"}}, adminToken)
	if created.Code != http.StatusOK || !strings.Contains(created.Body.String(), `"success":true`) {
		t.Fatalf("create site = %d/%s", created.Code, created.Body.String())
	}
	sites, _ := searchStore.ListSites(t.Context())
	if len(sites) != 1 || sites[0].ID == 0 {
		t.Fatalf("sites = %+v", sites)
	}
	_ = searchStore.AddHealthStats(t.Context(), []search.HealthStat{{SiteKey: "demo", Bucket: time.Now(), OKCount: 3, EmptyCount: 1, TotalMs: 400}})
	healthPage := request(router, http.MethodGet, "/admin/sites", adminToken, false)
	for _, expected := range []string{"75%", "25%", "100ms"} {
		if healthPage.Code != http.StatusOK || !strings.Contains(healthPage.Body.String(), expected) {
			t.Fatalf("health page missing %q: %d/%s", expected, healthPage.Code, healthPage.Body.String())
		}
	}
	tested := request(router, http.MethodGet, "/admin/sites/1/test?keyword=测试", adminToken, false)
	if tested.Code != http.StatusOK || !strings.Contains(tested.Body.String(), `"count":1`) || !strings.Contains(tested.Body.String(), "测试") {
		t.Fatalf("site test = %d/%s", tested.Code, tested.Body.String())
	}
	for _, secret := range []string{"vod_play_url", "signed-playback-token", "vod_content", "source_key"} {
		if strings.Contains(tested.Body.String(), secret) {
			t.Fatalf("site test leaked %q: %s", secret, tested.Body.String())
		}
	}
	updated := formRequest(router, http.MethodPut, "/admin/sites/1", url.Values{"enabled": {"false"}}, adminToken)
	if updated.Code != http.StatusOK {
		t.Fatalf("update site = %d/%s", updated.Code, updated.Body.String())
	}
	site, _ := searchStore.GetSite(t.Context(), 1)
	if site == nil || site.Enabled || site.Key != "demo" || site.BaseURL != "https://source.example/api" {
		t.Fatalf("updated site = %+v", site)
	}

	copyright := formRequest(router, http.MethodPost, "/admin/copyright", url.Values{"keyword": {"漫威"}}, adminToken)
	category := formRequest(router, http.MethodPost, "/admin/category", url.Values{"keyword": {"写真"}}, adminToken)
	if copyright.Code != http.StatusOK || category.Code != http.StatusOK {
		t.Fatalf("filter create = %d/%d", copyright.Code, category.Code)
	}
	keywords, _ := searchStore.CopyrightKeywords(t.Context())
	categories, _ := searchStore.CategoryKeywords(t.Context())
	if len(keywords) != 1 || keywords[0] != "漫威" || len(categories) != 1 || categories[0] != "写真" {
		t.Fatalf("filter keywords = %v/%v", keywords, categories)
	}

	allUsers, _ := users.ListUsers(t.Context())
	otherID := allUsers[1].ID
	role := formRequest(router, http.MethodPut, "/admin/users/"+itoa(otherID)+"/role", url.Values{"role": {"admin"}}, adminToken)
	if role.Code != http.StatusOK {
		t.Fatalf("role = %d/%s", role.Code, role.Body.String())
	}
	selfDelete := request(router, http.MethodDelete, "/admin/users/1", adminToken, false)
	if selfDelete.Code != http.StatusBadRequest || !strings.Contains(selfDelete.Body.String(), "不能删除自己的账号") {
		t.Fatalf("self delete = %d/%s", selfDelete.Code, selfDelete.Body.String())
	}
	deleted := request(router, http.MethodDelete, "/admin/users/2", adminToken, false)
	if deleted.Code != http.StatusOK {
		t.Fatalf("delete user = %d/%s", deleted.Code, deleted.Body.String())
	}

	_ = searchStore.Upsert(t.Context(), search.VodItem{SourceKey: "demo", VodId: "old", VodName: "旧数据", LastVisitedAt: time.Now().Add(-8 * 24 * time.Hour)})
	_ = searchStore.Upsert(t.Context(), search.VodItem{SourceKey: "demo", VodId: "new", VodName: "新数据", LastVisitedAt: time.Now()})
	// Upsert 默认把 last_seen_at 设成 NOW()；清理判断以 last_seen_at 优先，
	// 必须让测试数据里的 last_seen_at 也落到过期窗口。
	if _, err := testdb.Pool(t).Exec(t.Context(), `UPDATE vod_items SET last_seen_at = last_visited_at WHERE source_key = 'demo'`); err != nil {
		t.Fatal(err)
	}
	cleaned := request(router, http.MethodPost, "/admin/data/clean", adminToken, false)
	if cleaned.Code != http.StatusOK || !strings.Contains(cleaned.Body.String(), `"affected":1`) {
		t.Fatalf("clean = %d/%s", cleaned.Code, cleaned.Body.String())
	}
	if pending, _ := feedbackStore.CountPending(t.Context()); pending != 1 {
		t.Fatalf("pending feedback = %d", pending)
	}
}

func TestAdminMatchReviewRequiresReasonAndRecordsOneDecision(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 7, 8)
	router, _, searchStore, _, token, userToken := adminTestRouter(t)
	item := search.VodItem{SourceKey: "demo", VodId: "review-1", VodName: "待复核资源", VodYear: "2026"}
	if err := searchStore.Upsert(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := mediaidentity.NewPostgresStore(testdb.Pool(t)).RecordMatchCandidate(t.Context(), "demo", "review-1", 7, 0.72, "title_year"); err != nil {
		t.Fatal(err)
	}

	page := request(router, http.MethodGet, "/admin/matches", token, false)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "待复核资源") || !strings.Contains(page.Body.String(), "0.72") {
		t.Fatalf("match review page = %d/%s", page.Code, page.Body.String())
	}
	missingReason := formRequest(router, http.MethodPost, "/admin/matches/decision", url.Values{
		"source_key": {"demo"}, "vod_id": {"review-1"}, "media_id": {"7"}, "decision": {"verified"},
	}, token)
	if missingReason.Code != http.StatusBadRequest {
		t.Fatalf("missing reason = %d/%s", missingReason.Code, missingReason.Body.String())
	}
	verified := formRequest(router, http.MethodPost, "/admin/matches/decision", url.Values{
		"source_key": {"demo"}, "vod_id": {"review-1"}, "media_id": {"7"},
		"decision": {"verified"}, "reason": {"片名和年份一致，人工确认"},
	}, token)
	if verified.Code != http.StatusOK || !strings.Contains(verified.Body.String(), `"decision":"verified"`) {
		t.Fatalf("verified = %d/%s", verified.Code, verified.Body.String())
	}
	stored, _ := searchStore.FindBySourceID(t.Context(), "demo", "review-1")
	if stored == nil || stored.MediaID != 7 || stored.MediaConfidence != 1 || stored.MediaMatch != "manual" {
		t.Fatalf("verified item = %+v", stored)
	}
	decided, err := searchStore.ListMatchCandidates(t.Context(), search.MatchStatusVerified, 10)
	if err != nil || len(decided) != 1 {
		t.Fatalf("verified candidates = %+v/%v", decided, err)
	}
	repeated := formRequest(router, http.MethodPost, "/admin/matches/decision", url.Values{
		"source_key": {"demo"}, "vod_id": {"review-1"}, "media_id": {"7"},
		"decision": {"rejected"}, "reason": {"尝试覆盖已有结论"},
	}, token)
	if repeated.Code != http.StatusConflict {
		t.Fatalf("repeated decision = %d/%s", repeated.Code, repeated.Body.String())
	}

	_ = searchStore.Upsert(t.Context(), search.VodItem{SourceKey: "demo", VodId: "review-2", VodName: "API 待复核资源"})
	_ = mediaidentity.NewPostgresStore(testdb.Pool(t)).RecordDetailedMatchCandidate(t.Context(), "demo", "review-2", 8, 0.74, "weighted_features", search.MatchStatusReview, `{"features":{"title":{"score":0.4}}}`)
	forbiddenAPI := request(router, http.MethodGet, "/api/v2/admin/media-matches", userToken, false)
	if forbiddenAPI.Code != http.StatusForbidden {
		t.Fatalf("match API user access = %d/%s", forbiddenAPI.Code, forbiddenAPI.Body.String())
	}
	listAPI := request(router, http.MethodGet, "/api/v2/admin/media-matches?status=review&limit=10", token, false)
	if listAPI.Code != http.StatusOK || !strings.Contains(listAPI.Body.String(), `"resource_title":"API 待复核资源"`) || !strings.Contains(listAPI.Body.String(), `"id":2`) || !strings.Contains(listAPI.Body.String(), `"reason":{"features"`) {
		t.Fatalf("match API list = %d/%s", listAPI.Code, listAPI.Body.String())
	}
	invalidLimit := request(router, http.MethodGet, "/api/v2/admin/media-matches?limit=101", token, false)
	if invalidLimit.Code != http.StatusBadRequest || !strings.Contains(invalidLimit.Body.String(), `"code":"invalid_limit"`) {
		t.Fatalf("match API limit = %d/%s", invalidLimit.Code, invalidLimit.Body.String())
	}
	resolvedAPI := jsonRequest(router, http.MethodPost, "/api/v2/admin/media-matches/2/resolve", `{"decision":"verified","media_id":9,"reason":"选择更准确的媒体实体"}`, token)
	if resolvedAPI.Code != http.StatusOK || !strings.Contains(resolvedAPI.Body.String(), `"candidate_id":2`) || !strings.Contains(resolvedAPI.Body.String(), `"resolved_media_id":9`) {
		t.Fatalf("match API resolve = %d/%s", resolvedAPI.Code, resolvedAPI.Body.String())
	}
	resolvedItem, _ := searchStore.FindBySourceID(t.Context(), "demo", "review-2")
	resolvedCandidates, _ := searchStore.ListMatchCandidates(t.Context(), search.MatchStatusVerified, 10)
	resolved := false
	for _, candidate := range resolvedCandidates {
		if candidate.ResolvedMediaID == 9 {
			resolved = true
			break
		}
	}
	if resolvedItem == nil || resolvedItem.MediaID != 9 || len(resolvedCandidates) != 2 || !resolved {
		t.Fatalf("alternative media resolution = item:%+v candidates:%+v", resolvedItem, resolvedCandidates)
	}
}

func TestAdminRejectsUnsafeSiteAndKeywordInputs(t *testing.T) {
	router, _, _, _, token, _ := adminTestRouter(t)
	for _, values := range []url.Values{
		{"key": {"bad key"}, "base_url": {"javascript:alert(1)"}},
		{"key": {"private"}, "base_url": {"http://169.254.169.254/latest/meta-data"}},
		{"key": {"local"}, "base_url": {"http://service.internal/api"}},
	} {
		unsafe := formRequest(router, http.MethodPost, "/admin/sites", values, token)
		if unsafe.Code != http.StatusBadRequest {
			t.Fatalf("unsafe site = %d/%s", unsafe.Code, unsafe.Body.String())
		}
	}
	long := formRequest(router, http.MethodPost, "/admin/category", url.Values{"keyword": {strings.Repeat("长", 101)}}, token)
	if long.Code != http.StatusBadRequest {
		t.Fatalf("long keyword = %d/%s", long.Code, long.Body.String())
	}
}

func adminTestRouter(t *testing.T) (*gin.Engine, *identity.PostgresStore, *search.PostgresStore, *feedback.PostgresStore, string, string) {
	testdb.Media(t, testdb.Pool(t), 7, 8, 9)
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewPostgresStore(testdb.Pool(t))
	_, _ = users.Create(t.Context(), identity.User{Email: "admin@example.com", Username: "admin", Role: "admin", CreatedAt: time.Now()})
	_, _ = users.Create(t.Context(), identity.User{Email: "user@example.com", Username: "user", Role: "user", CreatedAt: time.Now()})
	searchStore := search.NewPostgresStore(testdb.Pool(t))
	movies := catalog.NewPostgresStore(testdb.Pool(t))
	_ = movies.Upsert(t.Context(), catalog.Movie{DoubanID: "1292052", Title: "电影"})
	feedbackStore := feedback.NewPostgresStore(testdb.Pool(t))
	_, _ = feedbackStore.Create(t.Context(), feedback.Feedback{Type: "bug", Content: "问题"})
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	pages := []string{"admin_dashboard", "admin_users", "admin_sites", "admin_cache", "admin_copyright", "admin_category", "admin_matches", "admin_jobs"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), pages)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, users, searchStore, movies, feedbackStore, crawlerStub{}, nil,
		WithMetricsReader(adminMetricsStub{})).Register(router)
	now := time.Now()
	adminToken, _ := auth.Sign(auth.Claims{UserID: 1, Role: "admin", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	userToken, _ := auth.Sign(auth.Claims{UserID: 2, Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, users, searchStore, feedbackStore, adminToken, userToken
}

type adminMetricsStub struct{}

func (adminMetricsStub) Snapshot(context.Context) (operations.MetricsSnapshot, error) {
	return operations.MetricsSnapshot{WindowHours: 24}, nil
}

func (adminMetricsStub) JobQueue(_ context.Context, query operations.JobQueueQuery) (operations.JobQueueSnapshot, error) {
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	snapshot := operations.JobQueueSnapshot{
		Counts: operations.JobCounts{Running: 1, Pending: 1},
		Page:   operations.JobQueuePage{HasNext: true, NextCursor: 1},
		Jobs: []operations.WorkerJob{{
			ID: 1, SubjectKey: "1292052", TaskType: "douban_reviews", Reason: "page_reviews_missing",
			Status: "running", AttemptCount: 1, MaxAttempts: 5, AvailableAt: now, LockedBy: "worker-test",
			LockedUntil: &now, StartedAt: &now, CreatedAt: now, UpdatedAt: now,
		}, {
			ID: 2, SubjectKey: "1", TaskType: "douban_sync", Status: "pending", CreatedAt: now, UpdatedAt: now,
		}},
	}
	if query.Status != "" {
		filtered := snapshot.Jobs[:0]
		for _, job := range snapshot.Jobs {
			if job.Status == query.Status {
				filtered = append(filtered, job)
			}
		}
		snapshot.Jobs = filtered
	}
	return snapshot, nil
}

type crawlerStub struct{}

func (crawlerStub) Search(_ context.Context, _, keyword, sourceKey string, _ []string) ([]search.VodItem, error) {
	return []search.VodItem{{SourceKey: sourceKey, VodId: "1", VodName: keyword, VodRemarks: "更新",
		TypeName: "电影", VodTime: "2026-08-04", VodContent: "unused content",
		VodPlayUrl: "https://video.example/movie.m3u8?token=signed-playback-token"}}, nil
}

func request(router http.Handler, method, target, token string, html bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	if html {
		req.Header.Set("Accept", "text/html")
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func formRequest(router http.Handler, method, target string, values url.Values, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func jsonRequest(router http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func itoa(value int) string { return strconv.Itoa(value) }
