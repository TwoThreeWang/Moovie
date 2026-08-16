package doubanpopular

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestFetchRexxarMapsMovieAndMobileHeaders(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.URL.Path, "/movie/hot_gaia") || request.Header.Get("Referer") != "https://m.douban.com/" || !strings.Contains(request.Header.Get("Cookie"), "bid=") {
			t.Fatalf("request = %s / %#v", request.URL, request.Header)
		}
		return response(request, http.StatusOK, `{"items":[{"id":"1292052","title":"肖申克","pic":{"normal":"cover"},"rating":{"value":9.7}}]}`), nil
	})}
	subjects, err := FetchRexxar(context.Background(), client, "movie")
	if err != nil || len(subjects) != 1 || subjects[0].ID != "1292052" || subjects[0].Rate != "9.7" || subjects[0].Cover != "cover" || subjects[0].URL != "https://movie.douban.com/subject/1292052/" {
		t.Fatalf("subjects/error = %+v/%v", subjects, err)
	}
}

func TestFetchRexxarKeepsPartialTVResult(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if strings.Contains(request.URL.Path, "tv_domestic") {
			return response(request, http.StatusBadGateway, `{}`), nil
		}
		return response(request, http.StatusOK, `{"subject_collection_items":[{"id":"1","title":"美剧","pic":{"large":"large"},"rating":{"value":8},"episodes_info":"全10集"}]}`), nil
	})}
	subjects, err := FetchRexxar(context.Background(), client, "tv")
	if err != nil || requests != 2 || len(subjects) != 1 || subjects[0].Cover != "large" || subjects[0].EpisodesInfo != "全10集" {
		t.Fatalf("subjects/requests/error = %+v/%d/%v", subjects, requests, err)
	}
}

func response(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
