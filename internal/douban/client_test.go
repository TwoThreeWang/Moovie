package douban

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClientPreservesRexxarRequestAndParsesRSSWindow(t *testing.T) {
	client := NewClient(&http.Client{Transport: doubanRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Path, "/rexxar/"):
			if request.URL.Query().Get("type") != "movie" || request.URL.Query().Get("status") != "mark" || request.Header.Get("Referer") != "https://m.douban.com/" || !strings.Contains(request.Header.Get("Cookie"), "bid=") {
				t.Fatalf("Rexxar request = %s headers=%v", request.URL.String(), request.Header)
			}
			return doubanResponse(request, http.StatusOK, `{"total":1,"interests":[{"status":"mark","subject":{"id":1292052,"title":"肖申克","type":"movie"}}]}`), nil
		case strings.Contains(request.URL.Path, "/feed/people/"):
			return doubanResponse(request, http.StatusOK, `<?xml version="1.0"?><rss><channel><item><link>https://movie.douban.com/subject/1292052/</link><pubDate>Wed, 29 Jul 2026 12:00:00 +0800</pubDate></item></channel></rss>`), nil
		default:
			return doubanResponse(request, http.StatusNotFound, "missing"), nil
		}
	})}, WithBases("https://m.douban.test", "https://www.douban.test"))
	items, total, err := client.Interests(t.Context(), "198878447", "movie", "mark", 0, 20)
	if err != nil || total != 1 || len(items) != 1 || items[0].Subject.ID.String() != "1292052" {
		t.Fatalf("interests/total/error = %+v/%d/%v", items, total, err)
	}
	subjects, earliest, err := client.RSSSubjects(t.Context(), "198878447")
	want := time.Date(2026, time.July, 28, 4, 0, 0, 0, time.UTC)
	if err != nil || !subjects["1292052"] || !earliest.Equal(want) {
		t.Fatalf("RSS = %#v/%s/%v, want earliest %s", subjects, earliest, err, want)
	}
}

type doubanRoundTripFunc func(*http.Request) (*http.Response, error)

func (function doubanRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func doubanResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}
