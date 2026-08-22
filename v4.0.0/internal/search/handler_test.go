package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/compat"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestSearchPageRedirectsAndPreservesSEOFormula(t *testing.T) {
	app := newSearchTestApp(t, &fakeSearcher{})

	empty := performRequest(app.handler, "/search")
	if empty.Code != http.StatusFound || empty.Header().Get("Location") != "/" {
		t.Fatalf("empty search = %d %q", empty.Code, empty.Header().Get("Location"))
	}
	douban := performRequest(app.handler, "/search?kw=%E8%82%96%E7%94%B3%E5%85%8B&doubanId=1292052")
	if douban.Code != http.StatusFound || douban.Header().Get("Location") != "/movie/1292052?title=%E8%82%96%E7%94%B3%E5%85%8B" {
		t.Fatalf("douban redirect = %d %q", douban.Code, douban.Header().Get("Location"))
	}

	snapshot, err := compat.Fetch(context.Background(), app.client, app.baseURL, compat.Case{Path: "/search?kw=肖申克", Kind: "html"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.Status != http.StatusOK || snapshot.Title != "肖申克在线观看 - 肖申克免费高清资源搜索 - Moovie影牛" {
		t.Fatalf("unexpected search snapshot: %+v", snapshot)
	}
	if snapshot.Description != "Moovie影牛 为您找到关于“肖申克”的相关资源。包含最新电影、电视剧在线观看线路，支持4K/高清多源码切换。" {
		t.Fatalf("description = %q", snapshot.Description)
	}
	if snapshot.Canonical != "https://moovie.example/search?kw=%e8%82%96%e7%94%b3%e5%85%8b" || snapshot.Robots != "index, follow" {
		t.Fatalf("canonical/robots = %q/%q", snapshot.Canonical, snapshot.Robots)
	}
	body := responseBody(t, app.client, app.baseURL+"/search?kw=%E8%82%96%E7%94%B3%E5%85%8B")
	if !strings.Contains(body, "/api/htmx/search") {
		t.Fatalf("search page did not use the unified fragment: %s", body)
	}
}

func TestSearchPageUsesUnifiedFragment(t *testing.T) {
	app := newSearchTestApp(t, &fakeSearcher{})
	body := responseBody(t, app.client, app.baseURL+"/search?kw=%E6%B2%99%E4%B8%98&bypass=1")
	if !strings.Contains(body, `/api/htmx/search?q=%E6%B2%99%E4%B8%98&bypass=1`) {
		t.Fatalf("search page did not select unified fragment: %s", body)
	}
}

func TestSuccessfulSearchIsLoggedWithStableIPHash(t *testing.T) {
	logger := &fakeSearchLogStore{}
	app := newSearchTestApp(t, &fakeSearcher{result: Result{Items: []VodItem{{VodName: "结果"}}}}, WithSearchLogger(logger, immediateRunner{}))
	_ = responseBody(t, app.client, app.baseURL+"/api/htmx/search?q=记录")
	if logger.loggedKeyword != "记录" || logger.loggedIP != hashIP("192.0.2.1") || logger.loggedUserID != nil {
		t.Fatalf("logged values = keyword:%q ip:%q user:%v", logger.loggedKeyword, logger.loggedIP, logger.loggedUserID)
	}

	emptyLogger := &fakeSearchLogStore{}
	emptyApp := newSearchTestApp(t, &fakeSearcher{}, WithSearchLogger(emptyLogger, immediateRunner{}))
	_ = responseBody(t, emptyApp.client, emptyApp.baseURL+"/api/htmx/search?q=空")
	if emptyLogger.loggedKeyword != "" {
		t.Fatalf("empty result was logged: %q", emptyLogger.loggedKeyword)
	}
}

func TestUnifiedSearchAPIKeepsGroupedAndUnmatchedResultsSeparate(t *testing.T) {
	resources := &fakeSearcher{result: Result{Items: []VodItem{
		{SourceKey: "a", VodId: "1", VodName: "沙丘", VodPic: "https://img.example/canonical.jpg", VodDoubanId: "1292052", MediaID: 9, SampleCount: 10, AvgSpeedMs: 300},
		{SourceKey: "b", VodId: "2", VodName: "沙丘", MediaID: 9, SampleCount: 10, AvgSpeedMs: 100},
		{SourceKey: "unknown", VodId: "3", VodName: "沙丘 未确认", VodPic: "https://img.example/unmatched.jpg", VodPlayUrl: "must-not-leak-play-url", VodContent: "must-not-leak-full-content"},
	}}}
	app := newSearchTestApp(t, resources, WithUnifiedSearcher(NewUnifiedSearchService(resources)))
	response := performRequest(app.handler, "/api/v2/search?q=%E6%B2%99%E4%B8%98")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Items     []UnifiedItem     `json:"items"`
		Unmatched []UnifiedResource `json:"unmatched"`
		Rollout   struct {
			Enabled bool `json:"enabled"`
			Percent int  `json:"percent"`
		} `json:"rollout"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ResourceCount != 2 || body.Items[0].BestResource == nil || body.Items[0].BestResource.SourceKey != "b" {
		t.Fatalf("items = %+v", body.Items)
	}
	if len(body.Unmatched) != 1 || body.Unmatched[0].SourceKey != "unknown" {
		t.Fatalf("unmatched = %+v", body.Unmatched)
	}
	if body.Rollout.Enabled || body.Rollout.Percent != 0 {
		t.Fatalf("rollout = %+v", body.Rollout)
	}
	if strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("unified search leaked full resource payload: %s", response.Body.String())
	}

	fragment := responseBody(t, app.client, app.baseURL+"/api/htmx/search?q=%E6%B2%99%E4%B8%98")
	for _, expected := range []string{"2 个已确认资源", "最佳线路：b", "可能相关的其他资源", "沙丘 未确认", `href="/watch/1292052?source_key=b&vod_id=2"`, `href="/play/unknown/3"`} {
		if !strings.Contains(fragment, expected) {
			t.Fatalf("fragment missing %q: %s", expected, fragment)
		}
	}
	if strings.Count(fragment, "/api/proxy/image/r76RqSIVvUryzx") != 2 {
		t.Fatalf("search posters did not use the image proxy: %s", fragment)
	}
}

func TestUnifiedSearchFragmentHidesCanonicalMediaWithoutResources(t *testing.T) {
	resources := &fakeSearcher{}
	catalog := &recordingUnifiedCatalog{items: []UnifiedItem{{MediaID: 12, Title: "仅有规范资料", Year: "2026", MediaType: "movie"}}}
	app := newSearchTestApp(t, resources, WithUnifiedSearcher(NewUnifiedSearchService(resources, WithUnifiedCatalog(catalog))))
	fragment := responseBody(t, app.client, app.baseURL+"/api/htmx/search?q=%E8%A7%84%E8%8C%83")
	if strings.Contains(fragment, "仅有规范资料") || strings.Contains(fragment, "0 个已确认资源") || !strings.Contains(fragment, "未找到相关资源") {
		t.Fatalf("canonical-only fragment = %s", fragment)
	}
	response := performRequest(app.handler, "/api/v2/search?q=%E8%A7%84%E8%8C%83")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "仅有规范资料") {
		t.Fatalf("canonical-only API = %d %s", response.Code, response.Body.String())
	}
}

func TestUnifiedSearchAPIValidatesStableQueryContract(t *testing.T) {
	resources := &fakeSearcher{}
	app := newSearchTestApp(t, resources, WithUnifiedSearcher(NewUnifiedSearchService(resources)))
	for _, testCase := range []struct {
		path string
		code string
	}{
		{path: "/api/v2/search", code: "invalid_query"},
		{path: "/api/v2/search?q=test&limit=101", code: "invalid_limit"},
		{path: "/api/v2/search?q=test&type=invalid", code: "invalid_type"},
	} {
		response := performRequest(app.handler, testCase.path)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"`+testCase.code+`"`) {
			t.Fatalf("GET %s = %d %s", testCase.path, response.Code, response.Body.String())
		}
	}
}

func TestTrendsPagePreservesThresholdsSEOAndCache(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 34, 0, 0, time.UTC)
	logger := &fakeSearchLogStore{trending: map[int][]TrendingKeyword{
		24:  {{Keyword: "热词", Count: 101, LastSearchedAt: now}, {Keyword: "新词", Count: 100, LastSearchedAt: now}},
		720: {{Keyword: "爆词", Count: 4001}, {Keyword: "总榜热词", Count: 2001}},
	}}
	app := newSearchTestApp(t, &fakeSearcher{}, WithSearchLogger(logger, immediateRunner{}))
	app.searchHandler.now = func() time.Time { return now }

	snapshot, err := compat.Fetch(context.Background(), app.client, app.baseURL, compat.Case{Path: "/trends", Kind: "html"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if snapshot.Status != http.StatusOK || snapshot.Title != "今日影视热搜榜 - 热门电影电视剧排行榜 - 实时更新 - Moovie影牛" || snapshot.Canonical != "https://moovie.example/trends" {
		t.Fatalf("trends snapshot = %+v", snapshot)
	}
	if snapshot.H1 != "搜索趋势" || len(snapshot.StructuredData) != 2 {
		t.Fatalf("trends H1/JSON-LD = %q/%v", snapshot.H1, snapshot.StructuredData)
	}
	body := responseBody(t, app.client, app.baseURL+"/trends")
	for _, expected := range []string{"热词", "新词", "爆词", "总榜热词", "12:34", "trend-tag hot", "trend-tag new", "trend-tag bao"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("trends body missing %q", expected)
		}
	}
	if logger.trendingCalls != 2 {
		t.Fatalf("trends cache missed: calls=%d", logger.trendingCalls)
	}
}

type fakeSearcher struct {
	mu     sync.Mutex
	result Result
	calls  int
}

func (searcher *fakeSearcher) Search(_ context.Context, _ string, _ bool) (*Result, error) {
	searcher.mu.Lock()
	defer searcher.mu.Unlock()
	searcher.calls++
	copyResult := searcher.result
	copyResult.Items = append([]VodItem(nil), searcher.result.Items...)
	return &copyResult, nil
}

func (searcher *fakeSearcher) callCount() int {
	searcher.mu.Lock()
	defer searcher.mu.Unlock()
	return searcher.calls
}

type searchTestApp struct {
	baseURL       string
	client        *http.Client
	handler       http.Handler
	searchHandler *Handler
}

func newSearchTestApp(t *testing.T, searcher Searcher, options ...HandlerOption) searchTestApp {
	t.Helper()
	gin.SetMode(gin.TestMode)
	webRoot := filepath.Join("..", "..", "web")
	renderer, err := platformweb.LoadRenderer(filepath.Join(webRoot, "templates"), []string{"search", "trends"})
	if err != nil {
		t.Fatalf("LoadRenderer() error = %v", err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", WebRoot: webRoot}
	searchHandler := NewHandler(cfg, searcher, options...)
	searchHandler.Register(router)
	return searchTestApp{baseURL: "http://moovie.test", client: &http.Client{Transport: handlerTransport{handler: router}}, handler: router, searchHandler: searchHandler}
}

type immediateRunner struct{}

func (immediateRunner) Run(task func(context.Context)) { task(context.Background()) }

type fakeSearchLogStore struct {
	loggedKeyword string
	loggedIP      string
	loggedUserID  *int
	trending      map[int][]TrendingKeyword
	trendingCalls int
}

func (store *fakeSearchLogStore) Log(_ context.Context, keyword string, userID *int, ipHash string) error {
	store.loggedKeyword, store.loggedUserID, store.loggedIP = keyword, userID, ipHash
	return nil
}

func (store *fakeSearchLogStore) Trending(_ context.Context, hours, _ int) ([]TrendingKeyword, error) {
	store.trendingCalls++
	return append([]TrendingKeyword(nil), store.trending[hours]...), nil
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	incoming := request.Clone(request.Context())
	incoming.RequestURI = request.URL.RequestURI()
	incoming.RemoteAddr = "192.0.2.1:1234"
	transport.handler.ServeHTTP(recorder, incoming)
	return recorder.Result(), nil
}

func performRequest(handler http.Handler, target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func responseBody(t *testing.T, client *http.Client, target string) string {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(parsed.String())
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", target, response.StatusCode)
	}
	return string(body)
}
