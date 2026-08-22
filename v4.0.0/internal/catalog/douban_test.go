package catalog

import (
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/gin-gonic/gin"
)

func TestDoubanProviderFallsBackAcrossMediaTypesAndMapsMovie(t *testing.T) {
	requests := make([]*http.Request, 0)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		if strings.Contains(request.URL.Path, "/movie/") {
			return jsonResponse(request, http.StatusNotFound, `{}`), nil
		}
		return jsonResponse(request, http.StatusOK, `{
"id":"1292052","title":"肖申克的救赎","original_title":"The Shawshank Redemption","year":"1994",
"intro":"希望让人自由","cover_url":"fallback","rating":{"value":9.7},"genres":["剧情","犯罪"],
"countries":["美国"],"durations":["142分钟"],"pic":{"large":"https://img3.doubanio.com/a.jpg"},
"directors":[{"id":"1","name":"弗兰克"}],"actors":[{"id":"2","name":"蒂姆"}]}`), nil
	})}
	store := NewPostgresStore(testdb.Pool(t))
	provider := NewDoubanProvider(client, store)
	if err := provider.Fetch(t.Context(), "1292052", false); err != nil {
		t.Fatal(err)
	}
	movie, _ := store.FindByDoubanID(t.Context(), "1292052")
	if movie == nil || movie.Title != "肖申克的救赎" || movie.Rating != 9.7 || movie.Duration != "142分钟" || movie.Directors != `[{"id":"1","name":"弗兰克"}]` {
		t.Fatalf("mapped movie = %+v", movie)
	}
	if len(requests) != 2 || !strings.Contains(requests[0].URL.Path, "/movie/") || !strings.Contains(requests[1].URL.Path, "/tv/") {
		t.Fatalf("request fallback = %#v", requests)
	}
	if requests[1].Header.Get("Accept") != "application/json" || requests[1].Header.Get("Referer") != "https://m.douban.com/" {
		t.Fatalf("request headers = %#v", requests[1].Header)
	}
}

func TestDoubanProviderUsesSuccessfulDetailEndpointForCanonicalMediaType(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/movie/") {
			return jsonResponse(request, http.StatusNotFound, `{}`), nil
		}
		return jsonResponse(request, http.StatusOK, `{
"id":"30181230","title":"测试剧集","aka":["别名甲","别名乙"],"year":"2026","genres":["剧情"],
"rating":{"value":8.6},"pic":{"large":"https://img.example/tv.jpg"}}`), nil
	})}
	writer := &canonicalWriterStub{}
	provider := NewDoubanProvider(client, NewPostgresStore(testdb.Pool(t)), WithDoubanCanonicalWriter(writer))
	if err := provider.Fetch(t.Context(), "30181230", false); err != nil {
		t.Fatal(err)
	}
	if writer.media.MediaType != "tv" {
		t.Fatalf("canonical media type = %q, want tv", writer.media.MediaType)
	}
	if len(writer.externalIDs) != 1 || writer.externalIDs[0].ExternalType != "tv" {
		t.Fatalf("canonical external IDs = %+v", writer.externalIDs)
	}
	if len(writer.aliases) != 2 || writer.aliases[0].Alias != "别名甲" || writer.aliases[0].AliasType != "aka" {
		t.Fatalf("canonical aliases = %+v", writer.aliases)
	}
}

func TestCanonicalDoubanMediaTypeCollapsesNonMovieEndpointsToTV(t *testing.T) {
	tests := map[string]string{"movie": "movie", "tv": "tv", "show": "tv"}
	for endpointType, expected := range tests {
		if got := canonicalDoubanMediaType(endpointType); got != expected {
			t.Fatalf("canonicalDoubanMediaType(%q) = %q, want %q", endpointType, got, expected)
		}
	}
}

func TestDoubanProviderFetchesReviewsIntoExistingMovie(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, http.StatusOK, `{"interests":[{"comment":"经典","create_time":"2026-07-01","sharing_url":"https://douban.example/1","user":{"name":"用户甲"}}]}`), nil
	})}
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克"})
	provider := NewDoubanProvider(client, store)
	if err := provider.FetchReviews(t.Context(), "1292052"); err != nil {
		t.Fatal(err)
	}
	movie, _ := store.FindByDoubanID(t.Context(), "1292052")
	if !strings.Contains(movie.ReviewsJSON, `"title":"经典"`) || !strings.Contains(movie.ReviewsJSON, `"author":"用户甲"`) || movie.ReviewsUpdatedAt.IsZero() {
		t.Fatalf("stored reviews = %+v", movie)
	}
}

func TestDoubanProviderRejectsInvalidIDBeforeNetwork(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid ID reached network")
		return nil, nil
	})}
	provider := NewDoubanProvider(client, NewPostgresStore(testdb.Pool(t)))
	for _, id := range []string{"", "12345", "abc123", "1234567890"} {
		if err := provider.Fetch(t.Context(), id, false); err == nil {
			t.Fatalf("Fetch(%q) unexpectedly succeeded", id)
		}
	}
}

func TestDoubanPopularFallsBackToRexxarBeforeLocalStore(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Host == "movie.douban.com" {
			return jsonResponse(request, http.StatusBadGateway, `{}`), nil
		}
		return jsonResponse(request, http.StatusOK, `{"items":[{"id":"1292052","title":"肖申克","pic":{"normal":"https://img.example/a.jpg"},"rating":{"value":9.7}}]}`), nil
	})}
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "local", Title: "本地降级", Genres: "剧情", Rating: 8})
	provider := NewDoubanProvider(client, store)
	subjects, err := provider.Popular(t.Context(), "movie")
	if err != nil || requests != 2 || len(subjects) != 1 || subjects[0].ID != "1292052" || !strings.HasPrefix(subjects[0].Cover, "/api/proxy/image/r76RqSIVvUryzx") {
		t.Fatalf("subjects/requests/error = %+v/%d/%v", subjects, requests, err)
	}
	_, _ = provider.Popular(t.Context(), "movie")
	if requests != 2 {
		t.Fatalf("Rexxar result was not cached: %d", requests)
	}
}

func TestDoubanSuggestionsPreferLocalAndProxyExternalImages(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎", OriginalTitle: "Shawshank", Genres: "剧情", Poster: "local-poster", Rating: 9.7})
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("local suggestion unexpectedly reached network")
		return nil, nil
	})}
	provider := NewDoubanProvider(client, store)
	local, err := provider.Suggest(t.Context(), "肖申克")
	if err != nil || len(local) != 1 || local[0].ID != "1292052" || local[0].Img != "local-poster" || local[0].Type != "movie" {
		t.Fatalf("local suggestions/error = %+v/%v", local, err)
	}

	emptyStore := NewPostgresStore(testdb.Pool(t))
	client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("q") != "霸王别姬" {
			t.Fatalf("suggestion query = %q", request.URL.RawQuery)
		}
		return jsonResponse(request, http.StatusOK, `[{"id":"1291546","title":"霸王别姬","sub_title":"Farewell","type":"movie","year":"1993","episode":"","img":"https://img3.doubanio.com/a.jpg"}]`), nil
	})}
	provider = NewDoubanProvider(client, emptyStore)
	external, err := provider.Suggest(t.Context(), "霸王别姬")
	if err != nil || len(external) != 1 || !strings.HasPrefix(external[0].Img, "/api/proxy/image/r76RqSIVvUryzx") {
		t.Fatalf("external suggestions/error = %+v/%v", external, err)
	}
}

func jsonResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: request,
	}
}

// 缓存过期后不能让页面同步等上游：发现页要立刻拿到旧榜单，刷新在后台跑完。
func TestDoubanPopularServesStaleCacheWhileRefreshingInBackground(t *testing.T) {
	released := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-released
		return jsonResponse(request, http.StatusOK, `{"subjects":[{"id":"fresh","title":"新榜单","rate":"9.0"}]}`), nil
	})}
	provider := NewDoubanProvider(client, NewPostgresStore(testdb.Pool(t)))
	provider.popular["movie"] = popularCacheEntry{
		subjects:  []PopularSubject{{ID: "stale", Title: "旧榜单"}},
		expiresAt: time.Now().Add(-time.Minute),
	}

	started := time.Now()
	subjects, err := provider.Popular(t.Context(), "movie")
	elapsed := time.Since(started)
	if err != nil || len(subjects) != 1 || subjects[0].ID != "stale" {
		t.Fatalf("subjects/error = %+v/%v", subjects, err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("stale cache waited for the upstream refresh: %s", elapsed)
	}

	close(released)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cached, _ := provider.cachedPopular("movie"); len(cached.subjects) == 1 && cached.subjects[0].ID == "fresh" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh never updated the cache")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 搜索页的豆瓣卡片不能被慢豆瓣拖住：超时后只显示本地匹配，而不是干等 10 秒。
func TestDoubanCardFallsBackToLocalWhenExternalSuggestIsSlow(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	store := NewPostgresStore(testdb.Pool(t))
	if err := store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎", Genres: "剧情", Rating: 9.7}); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	provider := NewDoubanProvider(client, store)
	router := gin.New()
	router.SetHTMLTemplate(template.Must(template.New("partials/douban_card.html").Parse(`{{ range .Matches }}{{ .DoubanID }};{{ end }}`)))
	NewHandler(config.Config{}, store, WithSuggester(provider)).Register(router)

	recorder := httptest.NewRecorder()
	started := time.Now()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/htmx/douban-card?kw=肖申克", nil))
	elapsed := time.Since(started)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "1292052") {
		t.Fatalf("status/body = %d/%q", recorder.Code, recorder.Body.String())
	}
	if elapsed > externalSuggestBudget+2*time.Second {
		t.Fatalf("douban card waited on the slow upstream: %s", elapsed)
	}
}
