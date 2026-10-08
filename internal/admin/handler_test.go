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
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
)

func TestAdminPagesAndMutationsRequireRoleAndPreserveMainFlows(t *testing.T) {
	router, users, searchStore, feedbackStore, _, adminToken, userToken := adminTestRouter(t)
	guest := request(router, http.MethodGet, "/admin", "", true)
	if guest.Code != http.StatusFound || guest.Header().Get("Location") != "/auth/login?redirect=/admin" {
		t.Fatalf("guest = %d/%s", guest.Code, guest.Header().Get("Location"))
	}
	forbidden := request(router, http.MethodGet, "/admin", userToken, false)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "需要管理员权限") {
		t.Fatalf("forbidden = %d/%s", forbidden.Code, forbidden.Body.String())
	}
	for _, path := range []string{"/admin", "/admin/users", "/admin/sites", "/admin/data", "/admin/jobs", "/admin/matches", "/admin/filters"} {
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

	copyright := formRequest(router, http.MethodPost, "/admin/filters", url.Values{"keyword": {"漫威"}, "copyright_restricted": {"on"}}, adminToken)
	sensitive := formRequest(router, http.MethodPost, "/admin/filters", url.Values{"keyword": {"写真"}, "block_ingest": {"on"}, "sensitive": {"on"}}, adminToken)
	if copyright.Code != http.StatusOK || sensitive.Code != http.StatusOK {
		t.Fatalf("filter create = %d/%d", copyright.Code, sensitive.Code)
	}
	filters, _ := searchStore.ListContentFilters(t.Context())
	if len(filters) != 2 || filters[0].Keyword != "漫威" || !filters[0].CopyrightRestricted ||
		filters[1].Keyword != "写真" || !filters[1].BlockIngest || !filters[1].Sensitive || searchStore.trendInvalidations != 2 {
		t.Fatalf("content filters = %+v, invalidations=%d", filters, searchStore.trendInvalidations)
	}
	updatedFilter := formRequest(router, http.MethodPut, "/admin/filters/1", url.Values{"keyword": {"漫威电影"}, "copyright_restricted": {"on"}, "sensitive": {"on"}}, adminToken)
	if updatedFilter.Code != http.StatusOK || searchStore.filters[0].Keyword != "漫威电影" || !searchStore.filters[0].Sensitive {
		t.Fatalf("filter update = %d/%s filters=%+v", updatedFilter.Code, updatedFilter.Body.String(), searchStore.filters)
	}

	allUsers, _ := users.ListUsers(t.Context())
	otherID := 0
	for _, user := range allUsers {
		if user.ID != 1 {
			otherID = user.ID
			break
		}
	}
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

	_ = searchStore.Upsert(t.Context(), search.VodItem{SourceKey: "demo", VodId: "old", VodName: "旧数据", VodPlayUrl: "正片$https://video.example/old.m3u8", LastVisitedAt: time.Now().Add(-8 * 24 * time.Hour)})
	_ = searchStore.Upsert(t.Context(), search.VodItem{SourceKey: "demo", VodId: "new", VodName: "新数据", VodPlayUrl: "正片$https://video.example/new.m3u8", LastVisitedAt: time.Now()})
	// Upsert 会把活跃时间刷新为 NOW()，这里显式构造一条超过清理窗口的资源。
	if _, err := testdb.Pool(t).Exec(t.Context(), `UPDATE vod_items
SET last_seen_at = CASE WHEN vod_id='old' THEN NOW() - INTERVAL '8 days' ELSE NOW() END
WHERE source_key = 'demo'`); err != nil {
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

func TestAdminUsersPagination(t *testing.T) {
	router, _, _, _, _, adminToken, _ := adminTestRouter(t)
	_, err := testdb.Pool(t).Exec(t.Context(), `INSERT INTO users (email, username, password_hash)
SELECT 'member-' || n || '@example.com', 'member-' || n, '' FROM generate_series(1, 21) AS n`)
	if err != nil {
		t.Fatal(err)
	}
	first := request(router, http.MethodGet, "/admin/users", adminToken, false)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "23 位用户") ||
		!strings.Contains(first.Body.String(), "member-21@example.com") ||
		strings.Contains(first.Body.String(), "admin@example.com") ||
		!strings.Contains(first.Body.String(), `/admin/users?page=2`) {
		t.Fatalf("first page = %d/%s", first.Code, first.Body.String())
	}
	second := request(router, http.MethodGet, "/admin/users?page=2", adminToken, false)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "admin@example.com") ||
		strings.Contains(second.Body.String(), "member-21@example.com") ||
		!strings.Contains(second.Body.String(), `/admin/users?page=1`) {
		t.Fatalf("second page = %d/%s", second.Code, second.Body.String())
	}
	last := request(router, http.MethodGet, "/admin/users?page=999", adminToken, false)
	if last.Code != http.StatusOK || !strings.Contains(last.Body.String(), "第 2 / 2 页") {
		t.Fatalf("out of range page = %d/%s", last.Code, last.Body.String())
	}
}

func TestAdminMediaManagementSearchesAndQueuesRecoveryTasks(t *testing.T) {
	router, _, _, _, mediaManager, adminToken, userToken := adminTestRouter(t)
	pool := testdb.Pool(t)
	var mediaID int
	if err := pool.QueryRow(t.Context(), `UPDATE media SET metadata_status='ready', completeness_score=90,
semantic_hash='semantic-v1', embedding_content='old semantic',
embedding=('[' || repeat('0,', 767) || '0]')::vector
WHERE douban_id='1292052' RETURNING id`).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	blank := request(router, http.MethodGet, "/admin/data?q=%20%20", adminToken, false)
	if blank.Code != http.StatusOK || mediaManager.searches != 0 || strings.Contains(blank.Body.String(), "<strong>电影</strong>") {
		t.Fatalf("blank media search = status:%d searches:%d", blank.Code, mediaManager.searches)
	}

	page := request(router, http.MethodGet, "/admin/data?q=电影&media_id="+strconv.Itoa(mediaID), adminToken, false)
	if mediaManager.searches != 1 {
		t.Fatalf("media searches = %d", mediaManager.searches)
	}
	for _, expected := range []string{"Media 运维", "电影", "完整度 90", "语义文本 已生成", "重新生成向量"} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), expected) {
			t.Fatalf("media page missing %q: %d/%s", expected, page.Code, page.Body.String())
		}
	}

	forbidden := formRequest(router, http.MethodPost, "/admin/data/media-task",
		url.Values{"media_id": {strconv.Itoa(mediaID)}, "action": {"semantic"}}, userToken)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin media task = %d", forbidden.Code)
	}

	semantic := formRequest(router, http.MethodPost, "/admin/data/media-task",
		url.Values{"media_id": {strconv.Itoa(mediaID)}, "action": {"semantic"}}, adminToken)
	if semantic.Code != http.StatusOK || !strings.Contains(semantic.Body.String(), `"job_id"`) {
		t.Fatalf("semantic regeneration = %d/%s", semantic.Code, semantic.Body.String())
	}
	var content string
	var hasVector bool
	if err := pool.QueryRow(t.Context(), `SELECT embedding_content, embedding IS NOT NULL FROM media WHERE id=$1`, mediaID).Scan(&content, &hasVector); err != nil {
		t.Fatal(err)
	}
	if content != "" || !hasVector {
		t.Fatalf("semantic invalidation = content:%q vector:%t", content, hasVector)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE media SET embedding_content='fresh semantic',
embedding=('[' || repeat('0,', 767) || '0]')::vector WHERE id=$1`, mediaID); err != nil {
		t.Fatal(err)
	}
	embedding := formRequest(router, http.MethodPost, "/admin/data/media-task",
		url.Values{"media_id": {strconv.Itoa(mediaID)}, "action": {"embedding"}}, adminToken)
	if embedding.Code != http.StatusOK {
		t.Fatalf("embedding regeneration = %d/%s", embedding.Code, embedding.Body.String())
	}
	if err := pool.QueryRow(t.Context(), `SELECT embedding IS NOT NULL FROM media WHERE id=$1`, mediaID).Scan(&hasVector); err != nil || hasVector {
		t.Fatalf("embedding invalidation = %t/%v", hasVector, err)
	}

	douban := formRequest(router, http.MethodPost, "/admin/data/media-task",
		url.Values{"media_id": {strconv.Itoa(mediaID)}, "action": {"douban"}}, adminToken)
	if douban.Code != http.StatusOK {
		t.Fatalf("manual douban refresh = %d/%s", douban.Code, douban.Body.String())
	}
	var jobs int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM worker_jobs WHERE subject_key='1292052'
AND task_type IN ('douban_metadata','semantic_content','embedding') AND status='pending'`).Scan(&jobs); err != nil || jobs != 3 {
		t.Fatalf("media jobs = %d/%v", jobs, err)
	}
}

func TestAdminMatchReviewRequiresReasonAndRecordsOneDecision(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 7, 8, 9)
	router, _, searchStore, _, _, token, userToken := adminTestRouter(t)
	item := search.VodItem{SourceKey: "demo", VodId: "review-1", VodName: "待复核资源", VodPlayUrl: "正片$https://video.example/main.m3u8", VodYear: "2026"}
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

	_ = searchStore.Upsert(t.Context(), search.VodItem{SourceKey: "demo", VodId: "review-2", VodName: "API 待复核资源", VodPlayUrl: "正片$https://video.example/review2.m3u8"})
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
	router, _, _, _, _, token, _ := adminTestRouter(t)
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
	long := formRequest(router, http.MethodPost, "/admin/filters", url.Values{"keyword": {strings.Repeat("长", 101)}, "block_ingest": {"on"}}, token)
	if long.Code != http.StatusBadRequest {
		t.Fatalf("long keyword = %d/%s", long.Code, long.Body.String())
	}
	short := formRequest(router, http.MethodPost, "/admin/filters", url.Values{"keyword": {"性"}, "sensitive": {"on"}}, token)
	missingAction := formRequest(router, http.MethodPost, "/admin/filters", url.Values{"keyword": {"测试"}}, token)
	if short.Code != http.StatusBadRequest || missingAction.Code != http.StatusBadRequest {
		t.Fatalf("filter validation = short:%d missing-action:%d", short.Code, missingAction.Code)
	}
}

func adminTestRouter(t *testing.T) (*gin.Engine, *identity.PostgresStore, *adminSearchStoreStub, *feedback.PostgresStore, *adminMediaManagerSpy, string, string) {
	testdb.Media(t, testdb.Pool(t), 7, 8, 9)
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewPostgresStore(testdb.Pool(t))
	_, _ = users.Create(t.Context(), identity.User{Email: "admin@example.com", Username: "admin", Role: "admin", CreatedAt: time.Now()})
	_, _ = users.Create(t.Context(), identity.User{Email: "user@example.com", Username: "user", Role: "user", CreatedAt: time.Now()})
	searchStore := &adminSearchStoreStub{PostgresStore: search.NewPostgresStore(testdb.Pool(t))}
	movies := catalog.NewPostgresStore(testdb.Pool(t))
	mediaManager := &adminMediaManagerSpy{PostgresStore: movies}
	_ = movies.Upsert(t.Context(), catalog.Movie{DoubanID: "1292052", Title: "电影"})
	feedbackStore := feedback.NewPostgresStore(testdb.Pool(t))
	_, _ = feedbackStore.Create(t.Context(), feedback.Feedback{Type: "bug", Content: "问题"})
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	pages := []string{"admin_dashboard", "admin_users", "admin_sites", "admin_cache", "admin_filters", "admin_matches", "admin_jobs"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), pages)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, users, searchStore, movies, feedbackStore, crawlerStub{}, nil,
		WithMetricsReader(adminMetricsStub{}), WithMediaManager(mediaManager), WithTrendCacheInvalidator(searchStore)).Register(router)
	now := time.Now()
	adminToken, _ := auth.Sign(auth.Claims{UserID: 1, Role: "admin", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	userToken, _ := auth.Sign(auth.Claims{UserID: 2, Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, users, searchStore, feedbackStore, mediaManager, adminToken, userToken
}

type adminMediaManagerSpy struct {
	*catalog.PostgresStore
	searches int
}

func (manager *adminMediaManagerSpy) SearchAdminMedia(ctx context.Context, keyword string, limit int) ([]catalog.AdminMedia, error) {
	manager.searches++
	return manager.PostgresStore.SearchAdminMedia(ctx, keyword, limit)
}

type adminSearchStoreStub struct {
	*search.PostgresStore
	filters            []search.ContentFilter
	trendInvalidations int
}

func (store *adminSearchStoreStub) ListContentFilters(context.Context) ([]search.ContentFilter, error) {
	return append([]search.ContentFilter(nil), store.filters...), nil
}

func (store *adminSearchStoreStub) CreateContentFilter(_ context.Context, filter search.ContentFilter) (*search.ContentFilter, error) {
	filter.ID = uint(len(store.filters) + 1)
	filter.CreatedAt, filter.UpdatedAt = time.Now(), time.Now()
	store.filters = append(store.filters, filter)
	return &filter, nil
}

func (store *adminSearchStoreStub) UpdateContentFilter(_ context.Context, filter search.ContentFilter) error {
	for index := range store.filters {
		if store.filters[index].ID == filter.ID {
			filter.CreatedAt = store.filters[index].CreatedAt
			filter.UpdatedAt = time.Now()
			store.filters[index] = filter
			return nil
		}
	}
	return nil
}

func (store *adminSearchStoreStub) DeleteContentFilter(_ context.Context, id uint) error {
	for index := range store.filters {
		if store.filters[index].ID == id {
			store.filters = append(store.filters[:index], store.filters[index+1:]...)
			break
		}
	}
	return nil
}

func (store *adminSearchStoreStub) InvalidateTrendCache() { store.trendInvalidations++ }

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
