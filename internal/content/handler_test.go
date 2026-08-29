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
		path      string
		title     string
		h1        string
		canonical string
		robots    string
	}{
		{path: "/", title: "Moovie影牛 - 发现你的下一部电影", h1: "Moovie 影牛", canonical: "https://moovie.example/", robots: "index, follow"},
		{path: "/about", title: "关于 - Moovie影牛", h1: "关于 Moovie 影牛", canonical: "https://moovie.example/about", robots: "index, follow"},
		{path: "/advertise", title: "广告合作 - Moovie影牛", h1: "广告合作", canonical: "https://moovie.example/advertise", robots: "index, follow"},
		{path: "/changelog", title: "更新记录 - Moovie影牛", h1: "更新记录 (Changelog)", canonical: "https://moovie.example/changelog", robots: "index, follow"},
		{path: "/dmca", title: "DMCA 声明 - Moovie影牛", h1: "DMCA 声明", canonical: "https://moovie.example/dmca", robots: "index, follow"},
		{path: "/copyright-restricted?title=%E6%B5%8B%E8%AF%95", title: "版权限制 - Moovie影牛", h1: "版权限制", robots: "noindex, follow"},
		{path: "/privacy", title: "隐私政策 - Moovie影牛", h1: "隐私政策", canonical: "https://moovie.example/privacy", robots: "index, follow"},
		{path: "/terms", title: "服务协议 - Moovie影牛", h1: "服务协议", canonical: "https://moovie.example/terms", robots: "index, follow"},
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
			if snapshot.Keywords != DefaultKeywords || snapshot.Robots != testCase.robots {
				t.Fatalf("keywords/robots changed: %q/%q", snapshot.Keywords, snapshot.Robots)
			}
			if snapshot.Canonical != testCase.canonical {
				t.Fatalf("canonical = %q, want %q", snapshot.Canonical, testCase.canonical)
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
	if snapshot.Robots != "noindex, follow" {
		t.Fatalf("404 robots = %q", snapshot.Robots)
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

func TestSitemapIndexAndShardsPreserveStaticAndDynamicURLs(t *testing.T) {
	provider := &fakeSitemapProvider{counts: map[SitemapKind]int{SitemapMovies: 5001, SitemapSimilar: 1}, movies: []SitemapMovie{{DoubanID: "1292052", UpdatedAt: time.Date(2026, time.July, 29, 9, 0, 0, 0, time.UTC)}}}
	app := newTestApp(t, provider)
	index := getBody(t, app.client, app.baseURL+"/sitemap.xml")
	for _, expected := range []string{"/sitemaps/static.xml", "/sitemaps/movies/1.xml", "/sitemaps/movies/2.xml", "/sitemaps/similar/1.xml"} {
		if !strings.Contains(index, expected) {
			t.Fatalf("sitemap index missing %q", expected)
		}
	}
	staticBody := getBody(t, app.client, app.baseURL+"/sitemaps/static.xml")
	if !strings.Contains(staticBody, "<loc>https://moovie.example/</loc>") || !strings.Contains(staticBody, "<loc>https://moovie.example/cinema</loc>") {
		t.Fatalf("static sitemap missing URLs: %s", staticBody)
	}
	movieBody := getBody(t, app.client, app.baseURL+"/sitemaps/movies/1.xml")
	similarBody := getBody(t, app.client, app.baseURL+"/sitemaps/similar/1.xml")
	if !strings.Contains(movieBody, "/movie/1292052</loc>") || !strings.Contains(similarBody, "/similar/1292052</loc>") || !strings.Contains(movieBody, "<lastmod>2026-07-29</lastmod>") {
		t.Fatalf("dynamic sitemap bodies = %s / %s", movieBody, similarBody)
	}
	if provider.limit != sitemapPageSize || provider.offset != 0 {
		t.Fatalf("provider page args = %d/%d", provider.limit, provider.offset)
	}
}

func TestSitemapCachesGeneratedDocument(t *testing.T) {
	provider := &fakeSitemapProvider{counts: map[SitemapKind]int{SitemapMovies: 1, SitemapSimilar: 0}, movies: []SitemapMovie{{DoubanID: "1292052", UpdatedAt: time.Now()}}}
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
	if provider.countCalls != 2 {
		t.Fatalf("sitemap provider count calls = %d, want 2", provider.countCalls)
	}
}

func TestSitemapErrorsAreNotCached(t *testing.T) {
	app := newTestApp(t, &fakeSitemapProvider{err: errors.New("database unavailable")})
	response, err := app.client.Get(app.baseURL + "/sitemap.xml")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusInternalServerError || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("error sitemap = status:%d cache:%q", response.StatusCode, response.Header.Get("Cache-Control"))
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
	counts     map[SitemapKind]int
	movies     []SitemapMovie
	err        error
	limit      int
	offset     int
	countCalls int
}

func (p *fakeSitemapProvider) CountForSitemap(_ context.Context, kind SitemapKind) (int, error) {
	p.countCalls++
	return p.counts[kind], p.err
}

func (p *fakeSitemapProvider) PageForSitemap(_ context.Context, _ SitemapKind, limit, offset int) ([]SitemapMovie, error) {
	p.limit, p.offset = limit, offset
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
