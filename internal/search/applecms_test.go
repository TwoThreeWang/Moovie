package search

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAppleCMSCrawlerMapsValuesAndFiltersCategories(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.RawQuery != "ac=videolist&pg=1&wd=%E8%82%96%E7%94%B3%E5%85%8B" {
			t.Fatalf("query = %q", request.URL.RawQuery)
		}
		if request.Header.Get("User-Agent") != crawlerUserAgent {
			t.Fatalf("user-agent = %q", request.Header.Get("User-Agent"))
		}
		body := `{"list":[{"vod_id":123,"vod_name":"肖申克","vod_play_url":"a$m3u8","type_name":"电影"},{"vod_id":"2","vod_name":"写真","vod_play_url":"b$m3u8","type_name":"写真片"},{"vod_id":"3","vod_name":"无地址","type_name":"电影"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})
	crawler := NewAppleCMSCrawler(&http.Client{Transport: transport})
	items, err := crawler.Search(context.Background(), "https://source.example/api.php/provide/vod/", "肖申克", "source", []string{"写真"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(items) != 1 || items[0].VodId != "123" || items[0].SourceKey != "source" {
		t.Fatalf("unexpected mapped items: %+v", items)
	}
}

func TestAppleCMSCrawlerRejectsNon200AndInvalidJSON(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "status", status: http.StatusBadGateway, body: `{}`},
		{name: "json", status: http.StatusOK, body: `{not-json`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			crawler := NewAppleCMSCrawler(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: testCase.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(testCase.body)), Request: request}, nil
			})})
			if _, err := crawler.Search(context.Background(), "https://source.example", "test", "source", nil); err == nil {
				t.Fatal("Search() error = nil")
			}
		})
	}
}

func TestAppleCMSCrawlerGetsDetailWithProviderQuery(t *testing.T) {
	crawler := NewAppleCMSCrawler(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.RawQuery != "ac=detail&ids=vod+42" {
			t.Fatalf("query = %q", request.URL.RawQuery)
		}
		body := `{"list":[{"vod_id":"vod 42","vod_name":"测试","vod_play_url":"正片$https://video.example/test.m3u8"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})
	item, err := crawler.GetDetail(context.Background(), "https://source.example/api", "vod 42", "source")
	if err != nil || item == nil || item.VodId != "vod 42" || item.SourceKey != "source" {
		t.Fatalf("item/error = %+v/%v", item, err)
	}
}

func TestAppleCMSCrawlerRejectsPrivateOriginAndRedirect(t *testing.T) {
	requests := 0
	crawler := NewAppleCMSCrawler(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"http://169.254.169.254/latest/meta-data"}},
			Body: io.NopCloser(strings.NewReader("redirect")), Request: request}, nil
	})})
	if _, err := crawler.Search(context.Background(), "http://127.0.0.1/api", "test", "source", nil); err == nil || requests != 0 {
		t.Fatalf("private origin error/requests = %v/%d", err, requests)
	}
	if _, err := crawler.Search(context.Background(), "https://source.example/api", "test", "source", nil); err == nil || requests != 1 {
		t.Fatalf("redirect error/requests = %v/%d", err, requests)
	}
}

func TestAppleCMSCrawlerRejectsOversizedResponseWithoutUnboundedRead(t *testing.T) {
	body := `{"list":[{"vod_content":"` + strings.Repeat("x", maxAppleCMSResponseBytes) + `"}]}`
	crawler := NewAppleCMSCrawler(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})})
	if _, err := crawler.Search(context.Background(), "https://source.example", "test", "source", nil); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
