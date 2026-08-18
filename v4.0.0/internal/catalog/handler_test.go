package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestMoviePagePreservesIndexedSEOJSONLDAndUserSignals(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	summary := strings.Repeat("剧", 151)
	if err := store.Upsert(t.Context(), Movie{
		DoubanID: "1292052", Title: "肖申克的救赎", OriginalTitle: "The Shawshank Redemption",
		Year: "1994", Poster: "https://img3.doubanio.com/view/photo.jpg", Rating: 9.7,
		Genres: "剧情,犯罪", Countries: "美国", Directors: `[{"id":"1","name":"弗兰克·德拉邦特"}]`,
		Actors: `[{"id":"2","name":"蒂姆·罗宾斯"}]`, Summary: summary, Duration: "142分钟", IMDbID: "tt0111161",
		EmbeddingContent: "标题：这段向量输入不应作为推荐语展示",
	}); err != nil {
		t.Fatal(err)
	}
	userMovies := library.NewPostgresStore(testdb.Pool(t))
	testdb.User(t, testdb.Pool(t), 7, 8)
	_ = userMovies.Upsert(t.Context(), library.Record{UserID: 7, MovieID: "1292052", Status: library.StatusWatched})
	_ = userMovies.Upsert(t.Context(), library.Record{UserID: 8, MovieID: "1292052", Status: library.StatusWish})
	router := catalogTestRouter(t, store, userMovies)
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: 7, Email: "user@example.com", Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/movie/1292052?title=%E8%82%96%E7%94%B3%E5%85%8B", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", recorder.Code, body)
	}
	for _, expected := range []string{
		"《肖申克的救赎》 (1994) - 剧情介绍/演职员表 - Moovie影牛",
		`<link rel="canonical" href="https://moovie.example/movie/1292052">`,
		`content="` + strings.Repeat("剧", 150) + `..."`,
		`"@type": "Movie"`, `"name": "弗兰克·德拉邦特"`, "已看过", "1 人看过", "1 人想看",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("movie page missing %q", expected)
		}
	}
	if strings.Contains(body, `<meta name="robots" content="noindex`) {
		t.Fatal("indexed movie unexpectedly became noindex")
	}
	if strings.Contains(body, "向量输入不应作为推荐语展示") || strings.Contains(body, "Moovie 推荐语") {
		t.Fatal("embedding input leaked into the user-facing movie page")
	}
}

func TestMoviePageOnlyShowsDirectPlayForIndexedPlayableResources(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克", Year: "1994", EmbeddingContent: "ready"})
	resources := &staticResourceLister{playable: true}
	router := catalogTestRouterWithOptions(t, store, nil, WithResourceLister(resources))

	ready := httptest.NewRecorder()
	router.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/movie/1292052", nil))
	readyBody := ready.Body.String()
	if ready.Code != http.StatusOK || !strings.Contains(readyBody, `href="/watch/1292052"`) || !strings.Contains(readyBody, "立即播放") || !strings.Contains(readyBody, "查看选集与线路") {
		t.Fatalf("playable movie page = %d/%s", ready.Code, readyBody)
	}
	if strings.Contains(readyBody, `hx-get="/api/htmx/search`) {
		t.Fatal("playable movie still launched the generic resource search")
	}

	resources.playable = false
	fallback := httptest.NewRecorder()
	router.ServeHTTP(fallback, httptest.NewRequest(http.MethodGet, "/movie/1292052", nil))
	fallbackBody := fallback.Body.String()
	if fallback.Code != http.StatusOK || strings.Contains(fallbackBody, "立即播放") || !strings.Contains(fallbackBody, "查找在线播放资源") || !strings.Contains(fallbackBody, `hx-get="/api/htmx/search`) {
		t.Fatalf("resource fallback page = %d/%s", fallback.Code, fallbackBody)
	}
}

func TestDoubanCardCombinesSeasonsAndQueuesMissingMetadata(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "first", Title: "末日地堡 第一季", Year: "2023", Rating: 0})
	_ = store.Upsert(t.Context(), Movie{DoubanID: "second", Title: "末日地堡 第二季", Year: "2024", Rating: 7.5})
	queue := &recordingRefreshQueue{jobID: 43}
	suggester := staticExternalSuggester{{ID: "first", Title: "末日地堡 第一季", Year: "2023"}, {ID: "12345678", Title: "末日地堡 第三季", Year: "2025"}}
	router := catalogTestRouterWithOptions(t, store, nil, WithSuggester(suggester), WithRefreshQueue(queue))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/htmx/douban-card?kw=%E6%9C%AB%E6%97%A5%E5%9C%B0%E5%A0%A1", nil))
	body := response.Body.String()
	firstIndex, secondIndex, thirdIndex := strings.Index(body, "末日地堡 第一季"), strings.Index(body, "末日地堡 第二季"), strings.Index(body, "末日地堡 第三季")
	if response.Code != http.StatusOK || firstIndex < 0 || secondIndex <= firstIndex || thirdIndex <= secondIndex || !strings.Contains(body, "3 部相关作品") || !strings.Contains(body, "资料补全中") {
		t.Fatalf("multi-season Douban card = %d/%s", response.Code, body)
	}
	if !reflect.DeepEqual(queue.doubanIDs, []string{"12345678"}) || !reflect.DeepEqual(queue.reasons, []string{"search_discovery"}) {
		t.Fatalf("queued discovered metadata = %#v/%#v", queue.doubanIDs, queue.reasons)
	}
}

func TestMoviePageRendersSixRelatedMoviesFromRecommendationPort(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "源电影", Year: "2026"})
	router := catalogTestRouterWithOptions(t, store, nil, WithSimilarFinder(staticSimilarFinder{{DoubanID: "target", Title: "相关推荐", Poster: "poster"}}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/movie/1292052", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "相关推荐") || !strings.Contains(recorder.Body.String(), "/similar/1292052") {
		t.Fatalf("related movie page = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestMoviePageRendersSeriesNavigationAndExcludesSeasonsFromRecommendations(t *testing.T) {
	store := &seriesStoreStub{
		Store: NewPostgresStore(testdb.Pool(t)),
		seasons: []SeriesSeason{
			{DoubanID: "35468745", Title: "末日地堡 第一季", Year: "2023", Rating: 7.8, SeasonNumber: 1},
			{DoubanID: "36444323", Title: "末日地堡 第二季", Year: "2024", Rating: 7.6, SeasonNumber: 2},
			{DoubanID: "36857449", Title: "末日地堡 第三季", Year: "2026", SeasonNumber: 3, Current: true},
		},
	}
	_ = store.Store.Upsert(t.Context(), Movie{DoubanID: "36857449", Title: "末日地堡 第三季", Year: "2026", EmbeddingContent: "ready"})
	finder := staticSimilarFinder{
		{DoubanID: "35468745", Title: "末日地堡 第一季"},
		{DoubanID: "36993427", Title: "末日地堡 第四季"},
		{DoubanID: "other", Title: "羊毛战记"},
	}
	router := catalogTestRouterWithOptions(t, store, nil, WithSimilarFinder(finder))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/movie/36857449", nil))
	body := recorder.Body.String()
	for _, expected := range []string{`aria-label="本剧季度"`, `href="/movie/35468745"`, `href="/movie/36444323"`, `aria-current="page"`, "羊毛战记"} {
		if recorder.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("series page missing %q: %d/%s", expected, recorder.Code, body)
		}
	}
	if strings.Count(body, "末日地堡 第一季") != 1 {
		t.Fatalf("same-series season leaked into recommendations: %s", body)
	}
	if strings.Contains(body, "末日地堡 第四季") {
		t.Fatalf("unlinked same-series season leaked into recommendations: %s", body)
	}
}

func TestSimilarRecommendationsCoalesceAndCache(t *testing.T) {
	finder := &countingSimilarFinder{movies: []Movie{{DoubanID: "target", Title: "相关推荐", Summary: "不进入卡片缓存"}}}
	handler := &Handler{similar: finder}
	first := handler.findSimilar(t.Context(), "1292052", 6)
	second := handler.findSimilar(t.Context(), "1292052", 6)
	if len(first) != 1 || len(second) != 1 || finder.calls != 1 || first[0].Summary != "" {
		t.Fatalf("similar results/calls = %d/%d/%d, want one result per call and one backend call", len(first), len(second), finder.calls)
	}
}

func TestMoviePageQueuesMissingEmbeddingOnce(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "源电影", Year: "2026"})
	runner := &queuedRunner{}
	enricher := &recordingVectorEnricher{}
	router := catalogTestRouterWithOptions(t, store, nil, WithBackgroundRunner(runner), WithVectorEnricher(enricher))
	for range 2 {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/movie/1292052", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("movie status = %d", recorder.Code)
		}
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("embedding tasks = %d, want 1", len(runner.tasks))
	}
	runner.tasks[0](t.Context())
	if len(enricher.ids) != 1 || enricher.ids[0] != "1292052" {
		t.Fatalf("embedding enriches = %#v", enricher.ids)
	}
}

func TestMoviePageQueuesDuePartialMetadataThroughWorker(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "1292052", Title: "只有标题", MetadataStatus: "partial",
		CompletenessScore: 15, EmbeddingContent: "already-enriched",
	})
	queue := &recordingRefreshQueue{jobID: 43}
	router := catalogTestRouterWithOptions(t, store, nil, WithRefreshQueue(queue))

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/movie/1292052", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("movie status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !reflect.DeepEqual(queue.providers, []string{RefreshProviderDouban}) || !reflect.DeepEqual(queue.reasons, []string{"partial_metadata"}) {
		t.Fatalf("queued providers/reasons = %#v/%#v", queue.providers, queue.reasons)
	}
}

func TestMetadataRefreshRespectsCompletenessAndNextRefresh(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, test := range []struct {
		name  string
		movie *Movie
		want  bool
	}{
		{name: "legacy partial", movie: &Movie{DoubanID: "1", MetadataStatus: "partial", CompletenessScore: 15}, want: true},
		{name: "due partial", movie: &Movie{DoubanID: "1", MetadataStatus: "partial", CompletenessScore: 15, NextRefreshAt: &past}, want: true},
		{name: "scheduled partial", movie: &Movie{DoubanID: "1", MetadataStatus: "partial", CompletenessScore: 15, NextRefreshAt: &future}},
		{name: "complete", movie: &Movie{DoubanID: "1", MetadataStatus: "ready", CompletenessScore: 70}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := needsMetadataRefresh(test.movie, now); got != test.want {
				t.Fatalf("needs refresh = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMissingAndDirtyMovieKeepFetchingPageAndDeduplicateWork(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "dirty", Title: ""})
	runner := &queuedRunner{}
	fetcher := &recordingFetcher{}
	router := catalogTestRouterWithOptions(t, store, nil, WithFetcher(fetcher, runner))

	for _, path := range []string{"/movie/missing?title=%E6%B5%8B%E8%AF%95", "/movie/missing?title=%E6%B5%8B%E8%AF%95"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "正在获取《测试》") || !strings.Contains(recorder.Body.String(), "豆瓣ID:</strong> missing") {
			t.Fatalf("fetching page = %d/%s", recorder.Code, recorder.Body.String())
		}
	}
	if len(runner.tasks) != 1 {
		t.Fatalf("queued fetches = %d, want 1", len(runner.tasks))
	}
	runner.tasks[0](t.Context())
	if len(fetcher.ids) != 1 || fetcher.ids[0] != "missing" {
		t.Fatalf("fetches = %#v", fetcher.ids)
	}

	dirty := httptest.NewRecorder()
	router.ServeHTTP(dirty, httptest.NewRequest(http.MethodGet, "/movie/dirty", nil))
	stored, _ := store.FindByDoubanID(t.Context(), "dirty")
	if dirty.Code != http.StatusOK || stored != nil {
		t.Fatalf("dirty movie status/stored = %d/%+v", dirty.Code, stored)
	}
}

func TestReviewsAndBackdropsUseStoredDataAndQueueStaleRefresh(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	reviewsJSON := `[{"title":"值得一看","author":"用户甲","link":"https://movie.douban.com/review/1","published":"2026-07-01","summary":"摘要"}]`
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "1292052", Title: "肖申克", ReviewsJSON: reviewsJSON,
		ReviewsUpdatedAt: time.Now().Add(-73 * time.Hour),
		Backdrops:        "https://image.tmdb.org/t/p/a.jpg,https://image.tmdb.org/t/p/b.jpg",
	})
	runner := &queuedRunner{}
	reviewFetcher := &recordingReviewFetcher{}
	router := catalogTestRouterWithOptions(t, store, nil, WithBackgroundRunner(runner), WithReviewFetcher(reviewFetcher))

	reviews := httptest.NewRecorder()
	router.ServeHTTP(reviews, httptest.NewRequest(http.MethodGet, "/api/htmx/reviews?douban_id=1292052", nil))
	if reviews.Code != http.StatusOK || !strings.Contains(reviews.Body.String(), "值得一看") || !strings.Contains(reviews.Body.String(), "用户甲") || len(runner.tasks) != 1 {
		t.Fatalf("reviews status/tasks/body = %d/%d/%s", reviews.Code, len(runner.tasks), reviews.Body.String())
	}
	runner.tasks[0](t.Context())
	if len(reviewFetcher.ids) != 1 || reviewFetcher.ids[0] != "1292052" {
		t.Fatalf("review refreshes = %#v", reviewFetcher.ids)
	}

	backdrops := httptest.NewRecorder()
	router.ServeHTTP(backdrops, httptest.NewRequest(http.MethodGet, "/api/htmx/movie-backdrops?douban_id=1292052", nil))
	if backdrops.Code != http.StatusOK || strings.Count(backdrops.Body.String(), `class="backdrop-item"`) != 2 || !strings.Contains(backdrops.Body.String(), "/api/proxy/image/r76RqSIVvUryzx") {
		t.Fatalf("backdrops = %d/%s", backdrops.Code, backdrops.Body.String())
	}
}

func TestReviewsAndBackdropsPreserveEmptyAndCollectingResponses(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克"})
	runner := &queuedRunner{}
	backdropSyncer := &recordingBackdropSyncer{}
	router := catalogTestRouterWithOptions(t, store, nil, WithBackgroundRunner(runner), WithBackdropSyncer(backdropSyncer))

	missingID := httptest.NewRecorder()
	router.ServeHTTP(missingID, httptest.NewRequest(http.MethodGet, "/api/htmx/reviews", nil))
	if missingID.Code != http.StatusBadRequest || missingID.Body.String() != "豆瓣 ID 不能为空" {
		t.Fatalf("missing review id = %d/%q", missingID.Code, missingID.Body.String())
	}
	collecting := httptest.NewRecorder()
	router.ServeHTTP(collecting, httptest.NewRequest(http.MethodGet, "/api/htmx/movie-backdrops?douban_id=1292052", nil))
	if collecting.Code != http.StatusOK || collecting.Body.String() != `<div class="reviews-empty">正在后台采集精彩剧照...</div>` || len(runner.tasks) != 1 {
		t.Fatalf("collecting backdrops = %d/%d/%q", collecting.Code, len(runner.tasks), collecting.Body.String())
	}
	runner.tasks[0](t.Context())
	if len(backdropSyncer.ids) != 1 || backdropSyncer.ids[0] != "1292052" {
		t.Fatalf("backdrop syncs = %#v", backdropSyncer.ids)
	}
}

func TestPageTriggeredCatalogWorkUsesPersistentRefreshQueue(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克"})
	// metadata_status/completeness_score 由刷新流水线维护，Upsert 不写；
	// 直接落库到「已就绪」状态，否则详情页会认为元数据不全而重复排队 douban_metadata。
	if _, err := testdb.Pool(t).Exec(t.Context(),
		`UPDATE media SET metadata_status = 'ready', completeness_score = 70 WHERE douban_id = '1292052'`); err != nil {
		t.Fatal(err)
	}
	queue := &recordingRefreshQueue{jobID: 43}
	runner := &queuedRunner{}
	router := catalogTestRouterWithOptions(t, store, nil,
		WithRefreshQueue(queue), WithBackgroundRunner(runner),
		WithReviewFetcher(&recordingReviewFetcher{}), WithBackdropSyncer(&recordingBackdropSyncer{}),
		WithVectorEnricher(&recordingVectorEnricher{}), WithFetcher(&recordingFetcher{}, runner))

	for _, path := range []string{
		"/api/htmx/reviews?douban_id=1292052",
		"/api/htmx/movie-backdrops?douban_id=1292052",
		"/movie/1292052",
		"/movie/1292053",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d: %s", path, recorder.Code, recorder.Body.String())
		}
	}
	wantProviders := []string{RefreshProviderReviews, RefreshProviderTMDB, RefreshProviderEmbedding, RefreshProviderDouban}
	if !reflect.DeepEqual(queue.providers, wantProviders) || len(runner.tasks) != 0 {
		t.Fatalf("queued providers/tasks = %#v/%d, want %#v/0", queue.providers, len(runner.tasks), wantProviders)
	}
}

func TestShedBackgroundTaskReturnsBusyAndClearsDeduplicationKey(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克"})
	runner := &rejectingRunner{}
	router := catalogTestRouterWithOptions(t, store, nil,
		WithBackgroundRunner(runner), WithBackdropSyncer(&recordingBackdropSyncer{}), WithFetcher(&recordingFetcher{}, runner))

	for index := 0; index < 2; index++ {
		backdrops := httptest.NewRecorder()
		router.ServeHTTP(backdrops, httptest.NewRequest(http.MethodGet, "/api/htmx/movie-backdrops?douban_id=1292052", nil))
		if backdrops.Code != http.StatusServiceUnavailable {
			t.Fatalf("shed backdrop %d status = %d", index, backdrops.Code)
		}
	}
	if runner.attempts != 2 {
		t.Fatalf("runner attempts = %d, want two backdrop retries", runner.attempts)
	}
}

func TestRefreshMediaQueuesCanonicalID(t *testing.T) {
	queue := &recordingRefreshQueue{jobID: 43}
	router := catalogTestRouterWithOptions(t, NewPostgresStore(testdb.Pool(t)), nil, WithRefreshQueue(queue))
	request := httptest.NewRequest(http.MethodPost, "/api/v2/media/9/refresh", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"job_id":43`) || len(queue.mediaIDs) != 1 || queue.mediaIDs[0] != 9 {
		t.Fatalf("media refresh = %d/%s/%#v", recorder.Code, recorder.Body.String(), queue.mediaIDs)
	}
}

func TestMediaSuggestPreservesAPIEnvelopeAndValidation(t *testing.T) {
	router := catalogTestRouterWithOptions(t, NewPostgresStore(testdb.Pool(t)), nil, WithSuggester(staticSuggester{{ID: "1292052", Title: "肖申克"}}))
	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/v2/media/suggest", nil))
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "搜索关键词不能为空") {
		t.Fatalf("missing keyword = %d/%s", missing.Code, missing.Body.String())
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, "/api/v2/media/suggest?q=%E8%82%96%E7%94%B3%E5%85%8B", nil))
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"success":true`) || !strings.Contains(result.Body.String(), `"id":"1292052"`) {
		t.Fatalf("suggest result = %d/%s", result.Code, result.Body.String())
	}
}

func catalogTestRouter(t *testing.T, store Store, userMovies UserMovies) *gin.Engine {
	return catalogTestRouterWithOptions(t, store, userMovies)
}

func catalogTestRouterWithOptions(t *testing.T, store Store, userMovies UserMovies, options ...HandlerOption) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	if userMovies != nil {
		options = append(options, WithUserMovies(userMovies))
	}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"movie", "fetching"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, store, options...).Register(router)
	return router
}

type queuedRunner struct{ tasks []func(context.Context) }

func (runner *queuedRunner) Run(task func(context.Context)) {
	runner.tasks = append(runner.tasks, task)
}

type rejectingRunner struct{ attempts int }

func (runner *rejectingRunner) Run(func(context.Context)) { panic("TryRun should be used") }

func (runner *rejectingRunner) TryRun(func(context.Context)) bool {
	runner.attempts++
	return false
}

type recordingFetcher struct{ ids []string }

func (fetcher *recordingFetcher) Fetch(_ context.Context, doubanID string, _ bool) error {
	fetcher.ids = append(fetcher.ids, doubanID)
	return nil
}

type recordingRefreshQueue struct {
	mediaIDs  []int
	doubanIDs []string
	providers []string
	reasons   []string
	jobID     int
}

func (queue *recordingRefreshQueue) EnqueueMediaRefresh(_ context.Context, mediaID int, _ string, _ int) (int, error) {
	queue.mediaIDs = append(queue.mediaIDs, mediaID)
	return queue.jobID, nil
}

func (queue *recordingRefreshQueue) EnqueueRefresh(_ context.Context, doubanID, provider, reason string, _ int) (int, error) {
	queue.doubanIDs = append(queue.doubanIDs, doubanID)
	queue.providers = append(queue.providers, provider)
	queue.reasons = append(queue.reasons, reason)
	return queue.jobID, nil
}

type recordingReviewFetcher struct{ ids []string }

func (fetcher *recordingReviewFetcher) FetchReviews(_ context.Context, doubanID string) error {
	fetcher.ids = append(fetcher.ids, doubanID)
	return nil
}

type recordingBackdropSyncer struct{ ids []string }

func (syncer *recordingBackdropSyncer) SyncBackdrops(_ context.Context, doubanID string) error {
	syncer.ids = append(syncer.ids, doubanID)
	return nil
}

type recordingVectorEnricher struct{ ids []string }

func (enricher *recordingVectorEnricher) Enrich(_ context.Context, doubanID string) error {
	enricher.ids = append(enricher.ids, doubanID)
	return nil
}

type staticSuggester []Suggestion

func (suggestions staticSuggester) Suggest(context.Context, string) ([]Suggestion, error) {
	return suggestions, nil
}

type staticExternalSuggester []Suggestion

func (suggestions staticExternalSuggester) Suggest(context.Context, string) ([]Suggestion, error) {
	return suggestions, nil
}

func (suggestions staticExternalSuggester) SuggestExternal(context.Context, string) ([]Suggestion, error) {
	return suggestions, nil
}

type staticResourceLister struct{ playable bool }

func (resources *staticResourceLister) ListResourcesByDoubanID(context.Context, string) ([]LinkedResource, error) {
	return nil, nil
}

func (resources *staticResourceLister) HasPlayableResource(context.Context, int) (bool, error) {
	return resources.playable, nil
}

type staticSimilarFinder []Movie

func (movies staticSimilarFinder) FindSimilar(context.Context, string, int) ([]Movie, error) {
	return movies, nil
}

type seriesStoreStub struct {
	Store
	seasons []SeriesSeason
}

func (store *seriesStoreStub) FindSeriesSeasons(context.Context, string) ([]SeriesSeason, error) {
	return store.seasons, nil
}

type countingSimilarFinder struct {
	movies []Movie
	calls  int
}

func (finder *countingSimilarFinder) FindSimilar(context.Context, string, int) ([]Movie, error) {
	finder.calls++
	return finder.movies, nil
}
