package catalog

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func statusResponse(request *http.Request, status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	header.Set("Content-Type", "application/json")
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func TestParseRetryAfterAcceptsSecondsAndCapsAbsurdValues(t *testing.T) {
	if parsed := parseRetryAfter("30"); parsed != 30*time.Second {
		t.Fatalf("seconds = %s", parsed)
	}
	if parsed := parseRetryAfter("86400"); parsed != maxRetryAfter {
		t.Fatalf("capped = %s", parsed)
	}
	if parsed := parseRetryAfter(""); parsed != 0 {
		t.Fatalf("empty = %s", parsed)
	}
	if parsed := parseRetryAfter("not-a-date"); parsed != 0 {
		t.Fatalf("garbage = %s", parsed)
	}
}

func TestWMDBResolverReportsRateLimitAsThrottleAndPausesItself(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return statusResponse(request, http.StatusTooManyRequests, http.Header{"Retry-After": {"12"}}, `{}`), nil
	})}
	resolver := NewWMDBResolver(client, "https://wmdb.test", time.Millisecond)
	_, err := resolver.ResolveOne(t.Context(), "1292052")
	retryAfter, throttled := workqueue.RetryAfter(err)
	if !throttled || retryAfter != 12*time.Second {
		t.Fatalf("error = %v (retry_after %s, throttled %t)", err, retryAfter, throttled)
	}
	// 收到 429 之后整个进程都要退避，而不是换个调用方再打一遍。
	if resolver.Allow() {
		t.Fatal("resolver kept issuing requests after a rate limit response")
	}
}

func TestWMDBResolverTreatsMissingMappingAsAnEmptyResult(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return statusResponse(request, http.StatusNotFound, nil, `{}`), nil
	})}
	// 上游没收录这个条目是正常结果，不该当成故障往上抛。
	imdbID, err := NewWMDBResolver(client, "https://wmdb.test", 0).ResolveOne(t.Context(), "1292052")
	if err != nil || imdbID != "" {
		t.Fatalf("resolve = %q/%v", imdbID, err)
	}
}

func TestTMDBSyncStopsWhenTheIMDbMappingIsMissing(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("TMDB sync must not reach the network without an IMDb ID: %s", request.URL.String())
		return nil, nil
	})}
	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "标题"})
	err := NewTMDBProvider(client, store, "tmdb-token", WithTMDBBase("https://tmdb.test")).
		SyncBackdrops(t.Context(), "1292052")
	// 缺映射不是可重试的失败：等回填补上之后会重新入队。
	if !workqueue.IsTerminal(err) || !strings.Contains(err.Error(), "IMDb 映射缺失") {
		t.Fatalf("error = %v", err)
	}
}

func TestDoubanFetchReportsEveryEndpointAndMarksAllNotFoundTerminal(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return statusResponse(request, http.StatusNotFound, nil, `{}`), nil
	})}
	provider := NewDoubanProvider(client, NewMemoryStore())
	err := provider.Fetch(t.Context(), "1292052", false)
	if err == nil {
		t.Fatal("expected an error")
	}
	// 只报最后一个端点等于把排查信息全丢了：三个端点的结论必须都在。
	for _, expected := range []string{"movie: Douban returned HTTP 404", "tv: Douban returned HTTP 404", "show: Douban returned HTTP 404"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error %q missing %q", err.Error(), expected)
		}
	}
	if !workqueue.IsTerminal(err) {
		t.Fatalf("all-404 should be terminal: %v", err)
	}
}

func TestDoubanFetchKeepsRetryingWhenOnlySomeEndpointsReturnNotFound(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/tv/") {
			return statusResponse(request, http.StatusForbidden, nil, `{}`), nil
		}
		return statusResponse(request, http.StatusNotFound, nil, `{}`), nil
	})}
	provider := NewDoubanProvider(client, NewMemoryStore())
	err := provider.Fetch(t.Context(), "1292052", false)
	if err == nil || workqueue.IsTerminal(err) {
		t.Fatalf("403 means blocked rather than missing, keep retrying: %v", err)
	}
	if !strings.Contains(err.Error(), "tv: Douban returned HTTP 403") {
		t.Fatalf("error = %v", err)
	}
}

func TestDoubanFetchSurfacesRateLimitAsThrottle(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "/tv/") {
			return statusResponse(request, http.StatusTooManyRequests, nil, `{}`), nil
		}
		return statusResponse(request, http.StatusNotFound, nil, `{}`), nil
	})}
	provider := NewDoubanProvider(client, NewMemoryStore())
	err := provider.Fetch(t.Context(), "1292052", false)
	if !workqueue.IsThrottled(err) || workqueue.IsTerminal(err) {
		t.Fatalf("error = %v", err)
	}
}
