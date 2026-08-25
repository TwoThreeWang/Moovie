package recommendation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"github.com/gin-gonic/gin"
)

func TestRecommendationsPagePreservesPathSEOJSONLDAndReasons(t *testing.T) {
	router, sourceID := recommendationTestRouter(t)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/similar/1292052", nil))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status/body = %d/%s", recorder.Code, body)
	}
	for _, expected := range []string{
		"类似《肖申克的救赎》的电影推荐_和《肖申克的救赎》差不多的电影 - Moovie影牛",
		`<link rel="canonical" href="https://moovie.example/similar/1292052">`,
		`"@type": "ItemList"`, `"@type": "BreadcrumbList"`, "目标电影", "推荐",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("page missing %q", expected)
		}
	}

	partial := httptest.NewRecorder()
	router.ServeHTTP(partial, httptest.NewRequest(http.MethodGet, "/api/htmx/similar?id="+strconv.Itoa(sourceID), nil))
	if partial.Code != http.StatusOK || !strings.Contains(partial.Body.String(), "目标电影") || !strings.Contains(partial.Body.String(), "/similar/1292052") {
		t.Fatalf("similar partial = %d/%s", partial.Code, partial.Body.String())
	}
	reasoned := httptest.NewRecorder()
	router.ServeHTTP(reasoned, httptest.NewRequest(http.MethodGet, "/api/htmx/similar-with-reason/1292052", nil))
	if reasoned.Code != http.StatusOK || !strings.Contains(reasoned.Body.String(), "目标电影") || !strings.Contains(reasoned.Body.String(), "reason-tag-") {
		t.Fatalf("reasoned partial = %d/%s", reasoned.Code, reasoned.Body.String())
	}
}

func TestRecommendationsMissingSourceIsReal404(t *testing.T) {
	router, _ := recommendationTestRouter(t)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/similar/missing", nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "电影未找到") {
		t.Fatalf("missing status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestForYouPagesAndGuestStatePreserveLegacyRoutes(t *testing.T) {
	router, _ := forYouTestRouter(t)
	for _, path := range []string{"/foryou", "/recommend"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "为你推荐 - Moovie影牛") || !strings.Contains(recorder.Body.String(), `hx-get="/api/htmx/foryou"`) {
			t.Fatalf("page %s = %d/%s", path, recorder.Code, recorder.Body.String())
		}
	}
	guest := httptest.NewRecorder()
	router.ServeHTTP(guest, httptest.NewRequest(http.MethodGet, "/api/htmx/foryou", nil))
	if guest.Code != http.StatusOK || !strings.Contains(guest.Body.String(), "开启你的个性化影院") || !strings.Contains(guest.Body.String(), "/auth/login?redirect=/foryou") {
		t.Fatalf("guest = %d/%s", guest.Code, guest.Body.String())
	}
}

func TestForYouHeroPaginationSectionsAndCache(t *testing.T) {
	router, personalizer := forYouTestRouter(t)
	token := recommendationToken(t, 7)
	first := authenticatedGet(router, "/api/htmx/foryou?page=1", token)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "影片01") || strings.Count(first.Body.String(), `class="movie-card"`) < 12 || !strings.Contains(first.Body.String(), "page=2") || !strings.Contains(first.Body.String(), "最近你看了《最近观看》") || !strings.Contains(first.Body.String(), "回味经典") {
		t.Fatalf("first page = %d/%s", first.Code, first.Body.String())
	}
	second := authenticatedGet(router, "/api/htmx/foryou?page=2", token)
	if second.Code != http.StatusOK || strings.Count(second.Body.String(), `class="movie-card"`) != 12 || strings.Contains(second.Body.String(), "foryou-hero") || !strings.Contains(second.Body.String(), "page=3") {
		t.Fatalf("second page = %d cards=%d/%s", second.Code, strings.Count(second.Body.String(), `class="movie-card"`), second.Body.String())
	}
	third := authenticatedGet(router, "/api/htmx/foryou?page=3", token)
	if third.Code != http.StatusOK || strings.Count(third.Body.String(), `class="movie-card"`) != 1 {
		t.Fatalf("third page = %d cards=%d/%s", third.Code, strings.Count(third.Body.String(), `class="movie-card"`), third.Body.String())
	}
	if personalizer.recommendationCalls != 1 || personalizer.reliveCalls != 1 || personalizer.recentCalls != 1 {
		t.Fatalf("cache calls = %+v", personalizer)
	}
}

func TestForYouMissingSnapshotEnqueuesRefreshWithoutRealtimeCalculation(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.User(t, pool, 7)
	catalogStore := catalog.NewPostgresStore(pool)
	if err := catalogStore.Upsert(t.Context(), catalog.Movie{DoubanID: "popular", Title: "热门兜底", Rating: 9.2}); err != nil {
		t.Fatal(err)
	}
	popular, err := catalogStore.FindByDoubanID(t.Context(), "popular")
	if err != nil {
		t.Fatal(err)
	}
	var runID int64
	if err := pool.QueryRow(t.Context(), `INSERT INTO popularity_snapshot_runs
(media_type, status, item_count, generated_at, expires_at) VALUES ('movie', 'ready', 1, NOW(), NOW() + INTERVAL '1 hour') RETURNING id`).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO popularity_snapshots
(run_id, media_id, rank, subject_payload) VALUES ($1, $2, 1, '{}'::jsonb)`, runID, popular.ID); err != nil {
		t.Fatal(err)
	}
	personalizer := &fakePersonalizer{personalized: []catalog.Movie{{ID: 1, Title: "不应实时查询"}}}
	queue := &recordingRecommendationQueue{}
	handler := NewHandler(config.Config{}, NewService(catalogStore, WithPersonalizer(personalizer)), NewSnapshotStore(pool)).WithRefreshQueue(queue)
	data := handler.loadForYou(t.Context(), 7)
	if data.HeroMovie == nil || data.HeroMovie.Title != "热门兜底" {
		t.Fatalf("fallback hero = %+v", data.HeroMovie)
	}
	if personalizer.recommendationCalls != 0 {
		t.Fatalf("realtime recommendation calls = %d, want 0", personalizer.recommendationCalls)
	}
	if len(queue.specs) != 1 || queue.specs[0].TaskType != TaskRefresh || queue.specs[0].SubjectKey != "7" {
		t.Fatalf("queued specs = %+v", queue.specs)
	}
}

func TestForYouSnapshotPersistsExpiresAndDropsNonRenderFields(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.User(t, pool, 7)
	store := NewSnapshotStore(pool)
	rich := catalog.Movie{
		ID: 1, DoubanID: "1", Title: "影片", Summary: strings.Repeat("摘", 700),
		ReviewsJSON: strings.Repeat("review", 1000), EmbeddingContent: strings.Repeat("vector", 1000),
		Embedding: make([]float32, 768), Backdrops: strings.Repeat("backdrop", 1000),
	}
	if err := store.Save(t.Context(), 7, forYouData{Personalized: []catalog.Movie{rich}, HeroMovie: &rich}); err != nil {
		t.Fatal(err)
	}
	data, fresh, found, err := NewSnapshotStore(pool).Load(t.Context(), 7)
	if err != nil || !found || !fresh {
		t.Fatalf("load snapshot found/fresh/error = %v/%v/%v", found, fresh, err)
	}
	movie := data.Personalized[0]
	if movie.ReviewsJSON != "" || movie.EmbeddingContent != "" || movie.Embedding != nil || movie.Backdrops != "" {
		t.Fatalf("non-render fields retained: %+v", movie)
	}
	if len([]rune(movie.Summary)) != 600 {
		t.Fatalf("summary length = %d, want 600", len([]rune(movie.Summary)))
	}
	if _, err := pool.Exec(t.Context(), `UPDATE user_recommendation_snapshots SET expires_at = NOW() - INTERVAL '1 second' WHERE user_id = 7`); err != nil {
		t.Fatal(err)
	}
	_, fresh, found, err = store.Load(t.Context(), 7)
	if err != nil || !found || fresh {
		t.Fatalf("expired snapshot found/fresh/error = %v/%v/%v", found, fresh, err)
	}
}

func recommendationTestRouter(t *testing.T) (*gin.Engine, int) {
	testdb.User(t, testdb.Pool(t), 7)
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := catalog.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), catalog.Movie{DoubanID: "1292052", Title: "肖申克的救赎", Year: "1994", Genres: "剧情,犯罪", Directors: `[{"name":"弗兰克"}]`, Rating: 9.7, Poster: "https://img3.doubanio.com/source.jpg"})
	_ = store.Upsert(t.Context(), catalog.Movie{DoubanID: "target", Title: "目标电影", Year: "1995", Genres: "剧情", Directors: `[{"name":"弗兰克"}]`, Rating: 9.1, Poster: "https://img3.doubanio.com/target.jpg"})
	seedEmbedding(t, store, "1292052")
	seedEmbedding(t, store, "target")
	source, _ := store.FindByDoubanID(t.Context(), "1292052")
	cfg := config.Config{SiteName: "Moovie影牛", SiteURL: "https://moovie.example"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"recommendations", "404"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, NewService(store), nil).Register(router)
	return router, source.ID
}

func forYouTestRouter(t *testing.T) (*gin.Engine, *fakePersonalizer) {
	testdb.User(t, testdb.Pool(t), 7)
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := catalog.NewPostgresStore(testdb.Pool(t))
	personalizer := &fakePersonalizer{}
	for index := 1; index <= 26; index++ {
		personalizer.personalized = append(personalizer.personalized, catalog.Movie{ID: index, DoubanID: strconv.Itoa(index), Title: fmt.Sprintf("影片%02d", index), Rating: 9, Poster: "poster"})
	}
	personalizer.relive = []catalog.Movie{{ID: 101, DoubanID: "classic", Title: "经典电影", Rating: 9}}
	personalizer.recent = []catalog.Movie{{ID: 102, DoubanID: "recent", Title: "关联电影", Rating: 8}}
	service := NewService(store, WithPersonalizer(personalizer))
	snapshots := NewSnapshotStore(testdb.Pool(t))
	if err := NewRefresher(snapshots, service).Refresh(t.Context(), 7); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"foryou", "recommendations", "404"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, service, snapshots).Register(router)
	return router, personalizer
}

type fakePersonalizer struct {
	personalized, relive, recent                  []catalog.Movie
	recommendationCalls, reliveCalls, recentCalls int
}

type recordingRecommendationQueue struct{ specs []workqueue.Spec }

func (queue *recordingRecommendationQueue) Enqueue(_ context.Context, spec workqueue.Spec) (int, error) {
	queue.specs = append(queue.specs, spec)
	return len(queue.specs), nil
}

func (fake *fakePersonalizer) UserRecommendations(context.Context, int, int) ([]catalog.Movie, error) {
	fake.recommendationCalls++
	return fake.personalized, nil
}
func (fake *fakePersonalizer) ReliveClassics(context.Context, int, int) ([]catalog.Movie, error) {
	fake.reliveCalls++
	return fake.relive, nil
}
func (fake *fakePersonalizer) RecentSimilar(context.Context, int, int) ([]catalog.Movie, string, error) {
	fake.recentCalls++
	return fake.recent, "最近观看", nil
}

// seedEmbedding 给影片写一条向量。相似推荐走 pgvector 距离，
// 没有 embedding 的影片永远不会出现在 FindSimilar 里。
func seedEmbedding(t *testing.T, store *catalog.PostgresStore, doubanID string) {
	t.Helper()
	vector := make([]float32, 768)
	vector[len(doubanID)%768] = 0.5
	if err := store.UpdateEmbedding(t.Context(), doubanID, "seed", "seed-hash", vector); err != nil {
		t.Fatalf("seed embedding %s: %v", doubanID, err)
	}
}

func recommendationToken(t *testing.T, userID int) string {
	t.Helper()
	now := time.Now()
	token, err := auth.Sign(auth.Claims{UserID: userID, Email: "u@example.com", Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return token
}
func authenticatedGet(router http.Handler, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
