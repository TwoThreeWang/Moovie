package recommendation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
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

func TestForYouColdCacheRequestsAreCoalesced(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	personalizer := &concurrentPersonalizer{}
	for index := 1; index <= 26; index++ {
		personalizer.movies = append(personalizer.movies, catalog.Movie{ID: index, DoubanID: strconv.Itoa(index), Title: fmt.Sprintf("影片%02d", index)})
	}
	handler := NewHandler(config.Config{}, NewService(catalog.NewPostgresStore(testdb.Pool(t)), WithPersonalizer(personalizer)))
	var group sync.WaitGroup
	for index := 0; index < 20; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if data := handler.loadForYou(t.Context(), 7); data.HeroMovie == nil {
				t.Error("coalesced result has no hero movie")
			}
		}()
	}
	group.Wait()
	if calls := personalizer.calls.Load(); calls != 1 {
		t.Fatalf("personalizer calls = %d, want 1", calls)
	}
}

func TestForYouCacheIsBoundedExpiresAndDropsNonRenderFields(t *testing.T) {
	now := time.Unix(100, 0)
	handler := NewHandler(config.Config{}, nil)
	handler.cacheCapacity = 2
	handler.now = func() time.Time { return now }
	rich := catalog.Movie{
		ID: 1, DoubanID: "1", Title: "影片", Summary: strings.Repeat("摘", 700),
		ReviewsJSON: strings.Repeat("review", 1000), EmbeddingContent: strings.Repeat("vector", 1000),
		Embedding: make([]float32, 768), Backdrops: strings.Repeat("backdrop", 1000),
	}
	for userID := 1; userID <= 3; userID++ {
		handler.storeForYou(userID, forYouData{Personalized: []catalog.Movie{rich}, HeroMovie: &rich})
	}
	if len(handler.cache) != 2 {
		t.Fatalf("cache size = %d, want 2", len(handler.cache))
	}
	for _, entry := range handler.cache {
		movie := entry.data.Personalized[0]
		if movie.ReviewsJSON != "" || movie.EmbeddingContent != "" || movie.Embedding != nil || movie.Backdrops != "" {
			t.Fatalf("non-render fields retained: %+v", movie)
		}
		if len([]rune(movie.Summary)) != 600 {
			t.Fatalf("summary length = %d, want 600", len([]rune(movie.Summary)))
		}
	}
	now = now.Add(2 * time.Hour)
	for userID := range handler.cache {
		if _, ok := handler.cachedForYou(userID); ok {
			t.Fatalf("expired user %d remained available", userID)
		}
	}
	if len(handler.cache) != 0 {
		t.Fatalf("expired cache size = %d, want 0", len(handler.cache))
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
	NewHandler(cfg, NewService(store)).Register(router)
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
	cfg := config.Config{SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"foryou", "recommendations", "404"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, NewService(store, WithPersonalizer(personalizer))).Register(router)
	return router, personalizer
}

type fakePersonalizer struct {
	personalized, relive, recent                  []catalog.Movie
	recommendationCalls, reliveCalls, recentCalls int
}

type concurrentPersonalizer struct {
	movies []catalog.Movie
	calls  atomic.Int32
}

func (personalizer *concurrentPersonalizer) UserRecommendations(context.Context, int, int) ([]catalog.Movie, error) {
	personalizer.calls.Add(1)
	time.Sleep(20 * time.Millisecond)
	return personalizer.movies, nil
}
func (*concurrentPersonalizer) ReliveClassics(context.Context, int, int) ([]catalog.Movie, error) {
	return nil, nil
}
func (*concurrentPersonalizer) RecentSimilar(context.Context, int, int) ([]catalog.Movie, string, error) {
	return nil, "", nil
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
