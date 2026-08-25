package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
)

type popularProviderFunc func(context.Context, string) ([]PopularSubject, error)

func (function popularProviderFunc) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	return function(ctx, mediaType)
}

func TestCompositePopularProviderDeduplicatesInPriorityOrder(t *testing.T) {
	provider := NewCompositePopularProvider(
		PopularSource{Name: "douban", Provider: popularProviderFunc(func(context.Context, string) ([]PopularSubject, error) {
			return []PopularSubject{{ID: "129", Title: "同一部电影", Rate: "8.0", Cover: "douban"}, {ID: "130", Title: "另一部", Rate: "7.0"}}, nil
		})},
		PopularSource{Name: "tmdb", Provider: popularProviderFunc(func(context.Context, string) ([]PopularSubject, error) {
			return []PopularSubject{{ID: "129", Title: "同一部电影", Rate: "8.5", Cover: "tmdb"}, {Title: "无外部ID", Year: "2026", Rate: "7.5"}}, nil
		})},
	)
	items, err := provider.Popular(context.Background(), "movie")
	if err != nil || len(items) != 3 {
		t.Fatalf("items/error = %+v/%v", items, err)
	}
	if items[0].ID != "129" || items[0].Cover != "douban" || items[0].Rate != "8.0" {
		t.Fatalf("first source should win for duplicates: %+v", items[0])
	}
}

func TestCompositePopularProviderKeepsStaleCacheWhenAllSourcesFail(t *testing.T) {
	provider := NewCompositePopularProvider(PopularSource{Name: "douban", Provider: popularProviderFunc(func(context.Context, string) ([]PopularSubject, error) {
		return []PopularSubject{{ID: "1", Title: "缓存"}}, nil
	})})
	if _, err := provider.Popular(context.Background(), "movie"); err != nil {
		t.Fatal(err)
	}
	provider.sources[0].Provider = popularProviderFunc(func(context.Context, string) ([]PopularSubject, error) {
		return nil, fmt.Errorf("source down")
	})
	items, err := provider.Popular(context.Background(), "movie")
	if err != nil || len(items) != 1 || items[0].Title != "缓存" {
		t.Fatalf("stale cache/error = %+v/%v", items, err)
	}
}

func TestCompositePopularProviderCoalescesColdBurst(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	provider := NewCompositePopularProvider(PopularSource{Name: "douban", Provider: popularProviderFunc(func(context.Context, string) ([]PopularSubject, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []PopularSubject{{ID: "1", Title: "热门"}}, nil
	})})
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			items, err := provider.Popular(context.Background(), "movie")
			if err != nil || len(items) != 1 {
				t.Errorf("items/error = %+v/%v", items, err)
			}
		}()
	}
	<-started
	close(release)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("cold popularity calls = %d, want 1", calls.Load())
	}
}

type popularIdentityResolverFunc func(context.Context, string, string, string) (mediaidentity.Media, error)

func (function popularIdentityResolverFunc) FindByExternalID(ctx context.Context, provider, externalType, externalID string) (mediaidentity.Media, error) {
	return function(ctx, provider, externalType, externalID)
}

func TestTMDBPopularProviderMapsOnlyBridgedCanonicalMedia(t *testing.T) {
	provider := NewTMDBPopularProvider(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/3/movie/popular" || request.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("request = %s auth=%q", request.URL, request.Header.Get("Authorization"))
		}
		body := `{"results":[{"id":101,"title":"已关联","poster_path":"/poster.jpg","vote_average":8.2,"release_date":"2026-01-02"},{"id":102,"title":"未关联","vote_average":9}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}, "token", popularIdentityResolverFunc(func(_ context.Context, provider, externalType, externalID string) (mediaidentity.Media, error) {
		if provider != "tmdb" || externalType != "movie" || externalID != "101" {
			return mediaidentity.Media{}, fmt.Errorf("not bridged")
		}
		return mediaidentity.Media{ID: 7, DoubanID: "1292052", Title: "已关联"}, nil
	}))
	items, err := provider.Popular(context.Background(), "movie")
	if err != nil || len(items) != 1 || items[0].ID != "1292052" || items[0].Year != "2026" {
		t.Fatalf("TMDB items/error = %+v/%v", items, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestDoubanPopularProviderPreservesEndpointAndImageProxy(t *testing.T) {
	requests := 0
	provider := NewDoubanPopularProvider(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Scheme != "https" || request.URL.Host != "movie.douban.com" || request.URL.Query().Get("type") != "tv" || request.URL.Query().Get("tag") != "综艺" || request.URL.Query().Get("page_limit") != "50" {
			t.Fatalf("URL = %s", request.URL.String())
		}
		body := `{"subjects":[{"id":"1","title":"节目","cover":"https://img.example/cover.webp"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})
	first, err := provider.Popular(context.Background(), "show")
	if err != nil || len(first) != 1 || !strings.HasPrefix(first[0].Cover, "/api/proxy/image/r76RqSIVvUryzx") {
		t.Fatalf("subjects/error = %+v/%v", first, err)
	}
	_, _ = provider.Popular(context.Background(), "show")
	if requests != 1 {
		t.Fatalf("requests = %d, want cached second call", requests)
	}
}

func TestDoubanPopularProviderFallsBackToRexxarAndCachesIt(t *testing.T) {
	requests := 0
	provider := NewDoubanPopularProvider(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Host == "movie.douban.com" {
			return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
		}
		if !strings.Contains(request.URL.Path, "tv_variety_show") {
			t.Fatalf("Rexxar URL = %s", request.URL)
		}
		body := `{"subject_collection_items":[{"id":"1","title":"节目","pic":{"normal":"https://img.example/show.webp"},"rating":{"value":8.6}}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})
	subjects, err := provider.Popular(context.Background(), "show")
	if err != nil || len(subjects) != 1 || subjects[0].Rate != "8.6" || !strings.HasPrefix(subjects[0].Cover, "/api/proxy/image/r76RqSIVvUryzx") {
		t.Fatalf("subjects/error = %+v/%v", subjects, err)
	}
	_, _ = provider.Popular(context.Background(), "show")
	if requests != 2 {
		t.Fatalf("requests = %d, want primary + Rexxar then cache", requests)
	}
}
