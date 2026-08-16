package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestDiscoverRoutesPreserveSEOHTMXAndDoubanCard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
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
	NewHandler(cfg, store, WithPopularProvider(popularStub{})).Register(router)

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
	card := httptest.NewRecorder()
	router.ServeHTTP(card, httptest.NewRequest(http.MethodGet, "/api/htmx/douban-card?kw=肖申克", nil))
	if card.Code != http.StatusOK || !strings.Contains(card.Body.String(), "肖申克的救赎") || !strings.Contains(card.Body.String(), "弗兰克·德拉邦特") || !strings.Contains(card.Body.String(), "/movie/1292052") {
		t.Fatalf("Douban card = %d/%s", card.Code, card.Body.String())
	}
}

type popularStub struct{}

func (popularStub) Popular(_ context.Context, movieType string) ([]PopularSubject, error) {
	titles := map[string]string{"movie": "热门电影", "tv": "热门剧集", "show": "热门综艺", "cartoon": "热门动画"}
	return []PopularSubject{{ID: "1", Title: titles[movieType], Rate: "9.0", Cover: "/poster.jpg"}}, nil
}

func TestPopularSubjectHasRating(t *testing.T) {
	for value, want := range map[string]bool{"9.7": true, "0.0": false, "0": false, "": false, "暂无": false} {
		if got := (PopularSubject{Rate: value}).HasRating(); got != want {
			t.Fatalf("HasRating(%q) = %t, want %t", value, got, want)
		}
	}
}
