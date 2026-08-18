package danmaku

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestNormalizationAndExternalConversionMatchLegacyFormats(t *testing.T) {
	season, title := splitSeason("庆余年第二季（真彩）1080P")
	if season != 2 || title != "庆余年" || parseEpisode("第十二集") != 12 || parseEpisode("HD国语") != 0 {
		t.Fatalf("normalized = season:%d title:%q episode:%d", season, title, parseEpisode("第十二集"))
	}
	if buildVodKey(title, season, parseEpisode("第3集")) != buildVodKey("庆余年", 2, parseEpisode("03")) {
		t.Fatal("equivalent episode labels produced different keys")
	}
	if sanitized := sanitizeText(" 换行\n零宽\u200b  字符 "); sanitized != "换行 零宽 字符" {
		t.Fatalf("sanitizeText() = %q", sanitized)
	}
	items := convertComments([]upstreamComment{
		{Parameters: "0,1,16777215,[qq]", Message: "滚动"},
		{Parameters: "12.5,5,16711680,[qq]", Message: "顶部"},
		{Parameters: "30,4,255,[bili]", Message: "底部"},
		{Parameters: "bad", Message: "丢弃"},
	})
	want := []Item{{Text: "滚动", Time: 0, Mode: 0, Color: "#FFFFFF"}, {Text: "顶部", Time: 12.5, Mode: 1, Color: "#FF0000"}, {Text: "底部", Time: 30, Mode: 2, Color: "#0000FF"}}
	if fmt.Sprint(items) != fmt.Sprint(want) {
		t.Fatalf("items = %+v, want %+v", items, want)
	}
	large := make([]Item, 20732)
	for index := range large {
		large[index].Time = float64(index)
	}
	sampled := sample(large, 4000)
	if len(sampled) != 4000 || sampled[len(sampled)-1].Time < 20000 {
		t.Fatalf("sampled = %d, last=%v", len(sampled), sampled[len(sampled)-1].Time)
	}
}

func TestListMergesCachedExternalAndFreshLocalWithoutSharingSources(t *testing.T) {
	var matchCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/api/v2/match":
			matchCalls.Add(1)
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["fileName"] != "庆余年 S02E03" {
				t.Errorf("filename = %q", body["fileName"])
			}
			return jsonResponse(http.StatusOK, `{"isMatched":true,"matches":[{"episodeId":42}]}`), nil
		case request.Method == http.MethodGet && request.URL.Path == "/api/v2/comment/42":
			return jsonResponse(http.StatusOK, `{"comments":[{"p":"2,1,16777215,[qq]","m":"外部"}]}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})}
	pool := testdb.Pool(t)
	seedUsers(t, pool, 1, 2)
	store := NewPostgresStore(pool)
	now := time.Now()
	_, _ = store.CreateGuarded(t.Context(), Record{VodKey: "庆余年|S02|E003", Time: 4, Text: "站内", Mode: 0, Color: "#FFFFFF", UserID: 1, CreatedAt: now}, now.Add(-time.Minute), now.Add(-time.Minute), 10)
	service := NewService(store, client, "https://danmaku.example")
	first := service.List(t.Context(), "庆余年第二季", "第3集", "192.0.2.1")
	if len(first) != 2 || first[0].Text != "外部" || first[1].Text != "站内" {
		t.Fatalf("first list = %+v", first)
	}
	_, _ = store.CreateGuarded(t.Context(), Record{VodKey: "庆余年|S02|E003", Time: 5, Text: "刚发送", Mode: 1, Color: "#FF0000", UserID: 2, CreatedAt: now}, now.Add(-time.Minute), now.Add(-time.Minute), 10)
	second := service.List(t.Context(), "庆余年 第2季", "03", "192.0.2.1")
	if len(second) != 3 || second[2].Text != "刚发送" || matchCalls.Load() != 1 {
		t.Fatalf("second list/calls = %+v/%d", second, matchCalls.Load())
	}
}

func TestSendSanitizesDefaultsRejectsDuplicatesAndAtomicallyLimitsConcurrentTraffic(t *testing.T) {
	pool := testdb.Pool(t)
	seedUsers(t, pool, 7, 8, 9)
	store := NewPostgresStore(pool)
	service := NewService(store, nil, "")
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	if err := service.Send(t.Context(), 7, SendInput{Title: "三体", Episode: "EP3", Text: "换行\n零宽\u200b字符", Time: -2, Mode: 99, Color: "red"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.ListByVodKey(t.Context(), "三体|S01|E003", 10)
	if len(rows) != 1 || rows[0].Text != "换行 零宽字符" || rows[0].Time != 0 || rows[0].Mode != 0 || rows[0].Color != "#FFFFFF" {
		t.Fatalf("stored = %+v", rows)
	}
	if err := service.Send(t.Context(), 7, SendInput{Title: "三体", Episode: "3", Text: "换行 零宽字符"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := service.Send(t.Context(), 8, SendInput{Title: "三体", Text: strings.Repeat("长", 51)}); !errors.Is(err, errLongText) {
		t.Fatalf("long text error = %v", err)
	}

	var successes atomic.Int32
	var limited atomic.Int32
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			err := service.Send(t.Context(), 9, SendInput{Title: "并发剧", Episode: "1", Text: fmt.Sprintf("弹幕-%d", index)})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrRateLimited):
				limited.Add(1)
			default:
				t.Errorf("unexpected send error: %v", err)
			}
		}(index)
	}
	wait.Wait()
	if successes.Load() != 10 || limited.Load() != 10 {
		t.Fatalf("success/limited = %d/%d", successes.Load(), limited.Load())
	}
}

func TestExternalOriginRateLimitOnlyAppliesToCacheMisses(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api/v2/match" {
			calls.Add(1)
			return jsonResponse(http.StatusOK, `{"isMatched":false,"matches":[]}`), nil
		}
		return jsonResponse(http.StatusNotFound, `{}`), nil
	})}
	service := NewService(NewPostgresStore(testdb.Pool(t)), client, "https://danmaku.example")
	for index := 0; index < 25; index++ {
		service.List(t.Context(), fmt.Sprintf("不同影片%d", index), "", "203.0.113.7")
	}
	if calls.Load() != 20 {
		t.Fatalf("upstream calls = %d, want 20", calls.Load())
	}
}

func TestExternalIPLimiterBoundsDistinctClientMemory(t *testing.T) {
	limiter := newIPLimiter(1, time.Minute)
	limiter.capacity = 2
	if !limiter.Allow("192.0.2.1") || !limiter.Allow("192.0.2.2") || limiter.Allow("192.0.2.3") || len(limiter.counts) != 2 {
		t.Fatalf("bounded limiter counts = %d", len(limiter.counts))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

// seedUsers 满足 danmakus.user_id 的外键。
func seedUsers(t *testing.T, pool *database.Pool, ids ...int) {
	t.Helper()
	for _, id := range ids {
		if _, err := pool.Exec(t.Context(), `INSERT INTO users (id,email,username,password_hash)
VALUES ($1,$2,$3,'x') ON CONFLICT (id) DO NOTHING`, id, fmt.Sprintf("u%d@test.local", id), fmt.Sprintf("u%d", id)); err != nil {
			t.Fatal(err)
		}
	}
}
