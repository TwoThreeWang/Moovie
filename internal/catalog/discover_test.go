package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestDiscoverRoutesPreserveSEOAndHTMX(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "1292052", Title: "肖申克的救赎", OriginalTitle: "The Shawshank Redemption", Year: "1994",
		Rating: 9.7, Genres: "剧情,犯罪", Countries: "美国", Directors: `[{"name":"弗兰克·德拉邦特"}]`,
		Actors: `[{"name":"蒂姆·罗宾斯"}]`, Summary: strings.Repeat("经典剧情", 30), UpdatedAt: time.Now(),
	})
	cfg := config.Config{SiteName: "Moovie影牛", SiteURL: "https://moovie.example"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"discover"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, store, WithPopularProvider(popularStub{}), WithSiteTrending(trendingStub{})).Register(router)

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/discover/tv", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "2026近期热门电视剧排行榜") || !strings.Contains(page.Body.String(), `href="https://moovie.example/discover/tv"`) {
		t.Fatalf("discover page = %d/%s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), `hx-get="/discover/tv"`) {
		t.Fatalf("discover page did not preserve its initial category: %s", page.Body.String())
	}
	htmxRequest := httptest.NewRequest(http.MethodGet, "/discover/cartoon", nil)
	htmxRequest.Header.Set("HX-Request", "true")
	htmx := httptest.NewRecorder()
	router.ServeHTTP(htmx, htmxRequest)
	if htmx.Code != http.StatusOK || !strings.Contains(htmx.Body.String(), "热门动画") || strings.Contains(htmx.Body.String(), "page-title") {
		t.Fatalf("discover HTMX = %d/%s", htmx.Code, htmx.Body.String())
	}
	queryRequest := httptest.NewRequest(http.MethodGet, "/discover?type=tv", nil)
	queryRequest.Header.Set("HX-Request", "true")
	query := httptest.NewRecorder()
	router.ServeHTTP(query, queryRequest)
	if query.Code != http.StatusOK || !strings.Contains(query.Body.String(), "热门剧集") || strings.Contains(query.Body.String(), "热门电影") {
		t.Fatalf("discover HTMX query category = %d/%s", query.Code, query.Body.String())
	}
	trendingRequest := httptest.NewRequest(http.MethodGet, "/discover/trending", nil)
	trendingRequest.Header.Set("HX-Request", "true")
	trending := httptest.NewRecorder()
	router.ServeHTTP(trending, trendingRequest)
	if trending.Code != http.StatusOK || !strings.Contains(trending.Body.String(), "本站热播片") {
		t.Fatalf("discover trending HTMX = %d/%s", trending.Code, trending.Body.String())
	}

	trendingPage := httptest.NewRecorder()
	router.ServeHTTP(trendingPage, httptest.NewRequest(http.MethodGet, "/discover/trending", nil))
	if trendingPage.Code != http.StatusOK || !strings.Contains(trendingPage.Body.String(), "本站热播排行榜") {
		t.Fatalf("discover trending page = %d/%s", trendingPage.Code, trendingPage.Body.String())
	}

	failedRouter := gin.New()
	failedRouter.HTMLRender = renderer
	NewHandler(cfg, store, WithPopularProvider(errorPopularStub{})).Register(failedRouter)
	failedRequest := httptest.NewRequest(http.MethodGet, "/discover/movie", nil)
	failedRequest.Header.Set("HX-Request", "true")
	failed := httptest.NewRecorder()
	failedRouter.ServeHTTP(failed, failedRequest)
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed discover status = %d, want %d", failed.Code, http.StatusServiceUnavailable)
	}

}

type popularStub struct{}

func (popularStub) Popular(_ context.Context, movieType string) ([]PopularSubject, error) {
	titles := map[string]string{"movie": "热门电影", "tv": "热门剧集", "show": "热门综艺", "cartoon": "热门动画"}
	return []PopularSubject{{ID: "1", Title: titles[movieType], Rate: "9.0", Cover: "/poster.jpg"}}, nil
}

type trendingStub struct{}

func (trendingStub) Popular(context.Context, string) ([]PopularSubject, error) {
	return []PopularSubject{{ID: "99", Title: "本站热播片", Rate: "8.5", Cover: "/trending.jpg"}}, nil
}

type errorPopularStub struct{}

func (errorPopularStub) Popular(context.Context, string) ([]PopularSubject, error) {
	return nil, errors.New("snapshot unavailable")
}

func TestPopularSubjectHasRating(t *testing.T) {
	for value, want := range map[string]bool{"9.7": true, "0.0": false, "0": false, "": false, "暂无": false} {
		if got := (PopularSubject{Rate: value}).HasRating(); got != want {
			t.Fatalf("HasRating(%q) = %t, want %t", value, got, want)
		}
	}
}
