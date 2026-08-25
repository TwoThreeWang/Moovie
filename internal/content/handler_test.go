package content

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/compat"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/httpserver"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

var testPages = []string{"home", "about", "advertise", "changelog", "dmca", "copyright_restricted", "privacy", "terms", "404"}

func TestPublicPagesPreserveStatusAndSEO(t *testing.T) {
	app := newTestApp(t, nil)
	cases := []struct {
		path  string
		title string
		h1    string
	}{
		{path: "/", title: "Moovie影牛 - 发现你的下一部电影", h1: "Moovie 影牛"},
		{path: "/about", title: "关于 - Moovie影牛", h1: "关于 Moovie 影牛"},
		{path: "/advertise", title: "广告合作 - Moovie影牛", h1: "广告合作"},
		{path: "/changelog", title: "更新记录 - Moovie影牛", h1: "更新记录 (Changelog)"},
		{path: "/dmca", title: "DMCA 声明 - Moovie影牛", h1: "DMCA 声明"},
		{path: "/copyright-restricted?title=%E6%B5%8B%E8%AF%95", title: "版权限制 - Moovie影牛", h1: "版权限制"},
		{path: "/privacy", title: "隐私政策 - Moovie影牛", h1: "隐私政策"},
		{path: "/terms", title: "服务协议 - Moovie影牛", h1: "服务协议"},
	}

	client := app.client
	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			snapshot, err := compat.Fetch(context.Background(), client, app.baseURL, compat.Case{Path: testCase.path, Kind: "html"})
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if snapshot.Status != http.StatusOK {
				t.Fatalf("status = %d, want %d", snapshot.Status, http.StatusOK)
			}
			if snapshot.Title != testCase.title || snapshot.H1 != testCase.h1 {
				t.Fatalf("title/H1 = %q/%q, want %q/%q", snapshot.Title, snapshot.H1, testCase.title, testCase.h1)
			}
			if snapshot.Description != DefaultDescription {
				t.Fatalf("description = %q, want legacy default", snapshot.Description)
			}
			if snapshot.Keywords != DefaultKeywords || snapshot.Robots != "index, follow" {
				t.Fatalf("keywords/robots changed: %q/%q", snapshot.Keywords, snapshot.Robots)
			}
			if snapshot.Canonical != "" {
				t.Fatalf("canonical = %q, legacy static page has no canonical", snapshot.Canonical)
			}
			if snapshot.OGTitle != testCase.title || snapshot.TwitterTitle != testCase.title {
				t.Fatalf("social titles changed: og=%q twitter=%q", snapshot.OGTitle, snapshot.TwitterTitle)
			}
		})
	}
}

func TestNotFoundIsNotSoft404(t *testing.T) {
	app := newTestApp(t, nil)
	snapshot, err := compat.Fetch(context.Background(), app.client, app.baseURL, compat.Case{Path: "/missing-page", Kind: "html"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.Status != http.StatusNotFound || snapshot.Title != "页面未找到 - Moovie影牛" || snapshot.H1 != "404" {
		t.Fatalf("unexpected 404 snapshot: status=%d title=%q h1=%q", snapshot.Status, snapshot.Title, snapshot.H1)
	}
}

func TestRobotsAndVerificationBodiesMatchLegacy(t *testing.T) {
	app := newTestApp(t, nil)
	client := app.client

	robots := getBody(t, client, app.baseURL+"/robots.txt")
	wantRobots := "User-agent: *\nDisallow: /admin/\nDisallow: /auth/\nDisallow: /dashboard/\nDisallow: /api/proxy/image/\nDisallow: /api/\n\nSitemap: https://moovie.example/sitemap.xml\n"
	if robots != wantRobots {
		t.Fatalf("robots body = %q, want %q", robots, wantRobots)
	}
	if got := getBody(t, client, app.baseURL+"/monoo-verify.txt"); got != verificationBody {
		t.Fatalf("verification body = %q, want %q", got, verificationBody)
	}
}

func TestSitemapPreservesStaticAndDynamicURLs(t *testing.T) {
	provider := &fakeSitemapProvider{movies: []SitemapMovie{{DoubanID: "1292052", UpdatedAt: time.Date(2026, time.July, 29, 9, 0, 0, 0, time.UTC)}}}
	app := newTestApp(t, provider)
	body := getBody(t, app.client, app.baseURL+"/sitemap.xml")

	for _, expected := range []string{
		"<loc>https://moovie.example/</loc>",
		"<loc>https://moovie.example/discover/cartoon</loc>",
		"<loc>https://moovie.example/movie/1292052</loc>",
		"<loc>https://moovie.example/similar/1292052</loc>",
		"<lastmod>2026-07-29</lastmod>",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("sitemap missing %q", expected)
		}
	}
	if provider.limit != sitemapMovieLimit {
		t.Fatalf("provider limit = %d, want %d", provider.limit, sitemapMovieLimit)
	}
}

func TestSitemapCachesGeneratedDocument(t *testing.T) {
	provider := &fakeSitemapProvider{movies: []SitemapMovie{{DoubanID: "1292052", UpdatedAt: time.Now()}}}
	app := newTestApp(t, provider)
	for index := 0; index < 3; index++ {
		response, err := app.client.Get(app.baseURL + "/sitemap.xml")
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Cache-Control"), "max-age=300") {
			t.Fatalf("sitemap response = status:%d cache:%q", response.StatusCode, response.Header.Get("Cache-Control"))
		}
	}
	if provider.calls != 1 {
		t.Fatalf("sitemap provider calls = %d, want 1", provider.calls)
	}
}

func TestSitemapKeepsStaticURLsWhenMovieProviderFails(t *testing.T) {
	app := newTestApp(t, &fakeSitemapProvider{err: errors.New("database unavailable")})
	body := getBody(t, app.client, app.baseURL+"/sitemap.xml")
	if !strings.Contains(body, "https://moovie.example/about") || strings.Contains(body, "/movie/") {
		t.Fatalf("unexpected degraded sitemap: %s", body)
	}
}

func TestStaticAssetsAreServedFromNewTree(t *testing.T) {
	app := newTestApp(t, nil)
	response, err := app.client.Get(app.baseURL + "/static/manifest.json")
	if err != nil {
		t.Fatalf("GET static asset: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("static status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

type fakeSitemapProvider struct {
	movies []SitemapMovie
	err    error
	limit  int
	calls  int
}

func (p *fakeSitemapProvider) LatestForSitemap(_ context.Context, limit int) ([]SitemapMovie, error) {
	p.calls++
	p.limit = limit
	return p.movies, p.err
}

type testApp struct {
	baseURL string
	client  *http.Client
}

func newTestApp(t *testing.T, provider SitemapMovieProvider) testApp {
	t.Helper()
	gin.SetMode(gin.TestMode)
	webRoot := filepath.Join("..", "..", "web")
	renderer, err := platformweb.LoadRenderer(filepath.Join(webRoot, "templates"), testPages)
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}
	cfg := config.Config{Env: "test", Port: "5008", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", WebRoot: webRoot}
	handler := NewHandler(cfg, provider)
	server := httpserver.New(cfg, nil, func(router *gin.Engine) {
		router.HTMLRender = renderer
		handler.Register(router, filepath.Join(webRoot, "static"))
	})
	return testApp{
		baseURL: "http://moovie.test",
		client:  &http.Client{Transport: inProcessTransport{handler: server.Handler}},
	}
}

type inProcessTransport struct {
	handler http.Handler
}

func (transport inProcessTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	incoming := request.Clone(request.Context())
	incoming.RequestURI = request.URL.RequestURI()
	transport.handler.ServeHTTP(recorder, incoming)
	return recorder.Result(), nil
}

func getBody(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", url, response.StatusCode, http.StatusOK)
	}
	return string(body)
}
