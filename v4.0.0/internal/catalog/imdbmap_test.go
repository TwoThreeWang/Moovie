package catalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

type imdbStoreStub struct {
	pending []string
	saved   map[string]string
	marked  []string
	limit   int
}

func newIMDbStoreStub(pending ...string) *imdbStoreStub {
	return &imdbStoreStub{pending: pending, saved: map[string]string{}}
}

func (stub *imdbStoreStub) PendingIMDbLookups(_ context.Context, limit int, _ time.Duration) ([]string, error) {
	stub.limit = limit
	return stub.pending, nil
}

func (stub *imdbStoreStub) SaveIMDbID(_ context.Context, doubanID, imdbID string) error {
	stub.saved[doubanID] = imdbID
	return nil
}

func (stub *imdbStoreStub) MarkIMDbLookupAttempt(_ context.Context, doubanIDs []string) error {
	stub.marked = append(stub.marked, doubanIDs...)
	return nil
}

type batchResolverStub struct {
	mapping map[string]string
	asked   []string
	calls   int
	err     error
}

func (stub *batchResolverStub) Resolve(_ context.Context, doubanIDs []string) (map[string]string, error) {
	stub.calls++
	stub.asked = append(stub.asked, doubanIDs...)
	return stub.mapping, stub.err
}

type singleResolverStub struct {
	mapping map[string]string
	asked   []string
	budget  int
	err     error
}

func (stub *singleResolverStub) Allow() bool {
	if stub.budget <= 0 {
		return false
	}
	stub.budget--
	return true
}

func (stub *singleResolverStub) ResolveOne(_ context.Context, doubanID string) (string, error) {
	stub.asked = append(stub.asked, doubanID)
	if stub.err != nil {
		return "", stub.err
	}
	return stub.mapping[doubanID], nil
}

func TestBackfillResolvesInBatchAndRequeuesTMDBForEveryHit(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864")
	queue := &refreshQueueStub{}
	batch := &batchResolverStub{mapping: map[string]string{"1292052": "tt0111161", "1291864": "tt0468569"}}
	handler := NewIMDbBackfillHandler(store, queue, batch)

	if err := handler.Handle(t.Context(), workqueue.Job{TaskType: TaskIMDbBackfill}); err != nil {
		t.Fatal(err)
	}
	// 两个对象只花了一次上游请求，这正是批量源的意义。
	if batch.calls != 1 || len(batch.asked) != 2 {
		t.Fatalf("batch calls = %d, asked = %v", batch.calls, batch.asked)
	}
	if store.saved["1292052"] != "tt0111161" || store.saved["1291864"] != "tt0468569" {
		t.Fatalf("saved = %+v", store.saved)
	}
	// 回填是唯一知道「刚刚多了新映射」的地方，必须由它把 TMDB 任务放回队列。
	if len(queue.jobs) != 2 || queue.jobs[0].TaskType != RefreshProviderTMDB {
		t.Fatalf("requeued jobs = %+v", queue.jobs)
	}
	if len(store.marked) != 2 {
		t.Fatalf("marked = %v", store.marked)
	}
}

func TestBackfillSendsOnlyBatchMissesToTheFallback(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864", "1296139")
	batch := &batchResolverStub{mapping: map[string]string{"1292052": "tt0111161"}}
	fallback := &singleResolverStub{mapping: map[string]string{"1291864": "tt0468569"}, budget: 10}
	handler := NewIMDbBackfillHandler(store, nil, batch, WithIMDbFallback(fallback))

	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	// 批量源已经解决的对象不该再打一次限流严重的兜底源。
	if len(fallback.asked) != 2 || fallback.asked[0] != "1291864" || fallback.asked[1] != "1296139" {
		t.Fatalf("fallback asked = %v", fallback.asked)
	}
	if store.saved["1291864"] != "tt0468569" {
		t.Fatalf("saved = %+v", store.saved)
	}
	// 三个都有了结论（命中或确认查不到），所以都要打上时间戳。
	if len(store.marked) != 3 {
		t.Fatalf("marked = %v", store.marked)
	}
}

func TestBackfillLeavesUnprocessedCandidatesUnmarkedWhenTheBudgetRunsOut(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864", "1296139")
	batch := &batchResolverStub{mapping: map[string]string{}}
	fallback := &singleResolverStub{mapping: map[string]string{}, budget: 1}
	handler := NewIMDbBackfillHandler(store, nil, batch, WithIMDbFallback(fallback))

	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	// 配额只够一个：没轮到的两个不能打标记，否则要等满一个重查周期才会被再看一眼。
	if len(fallback.asked) != 1 || len(store.marked) != 1 || store.marked[0] != "1292052" {
		t.Fatalf("asked = %v marked = %v", fallback.asked, store.marked)
	}
}

func TestBackfillStopsAtTheFirstThrottleAndNeverFallsBackOnBatchFailure(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864")
	fallback := &singleResolverStub{budget: 10, err: workqueue.Throttled(errors.New("upstream returned HTTP 429"), time.Second)}
	handler := NewIMDbBackfillHandler(store, nil, &batchResolverStub{mapping: map[string]string{}}, WithIMDbFallback(fallback))
	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	// 撞上限流就收手，剩下的留给下一轮，别把配额继续喂给一个正在拒绝的上游。
	if len(fallback.asked) != 1 || len(store.marked) != 0 {
		t.Fatalf("asked = %v marked = %v", fallback.asked, store.marked)
	}

	failing := &batchResolverStub{err: errors.New("wikidata is down")}
	untouched := &singleResolverStub{budget: 10}
	handler = NewIMDbBackfillHandler(newIMDbStoreStub("1292052"), nil, failing, WithIMDbFallback(untouched))
	if err := handler.Handle(t.Context(), workqueue.Job{}); err == nil {
		t.Fatal("batch failure must surface")
	}
	// 批量源挂了不代表可以用兜底源去扛整批流量。
	if len(untouched.asked) != 0 {
		t.Fatalf("fallback was used as a batch replacement: %v", untouched.asked)
	}
}

func TestWikidataResolverBuildsOneQueryAndParsesTheMapping(t *testing.T) {
	var body string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(request.Body)
		body = string(raw)
		if request.Method != http.MethodPost {
			t.Fatalf("method = %s", request.Method)
		}
		// 维基媒体会拒绝没有说明来源的默认 User-Agent。
		if !strings.Contains(request.Header.Get("User-Agent"), "Moovie") {
			t.Fatalf("user agent = %q", request.Header.Get("User-Agent"))
		}
		return statusResponse(request, http.StatusOK, nil, `{"results":{"bindings":[
			{"douban":{"value":"1292052"},"imdb":{"value":"tt0111161"}},
			{"douban":{"value":"1291864"},"imdb":{"value":"tt0468569"}}]}}`), nil
	})}
	resolver := NewWikidataResolver(client, "https://wikidata.test/sparql", "MoovieBot/1.0 (contact@example.com)")
	mapping, err := resolver.Resolve(t.Context(), []string{"1292052", "1291864"})
	if err != nil {
		t.Fatal(err)
	}
	if mapping["1292052"] != "tt0111161" || mapping["1291864"] != "tt0468569" {
		t.Fatalf("mapping = %+v", mapping)
	}
	for _, expected := range []string{"P4529", "P345", "1292052", "1291864"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("query %q missing %q", body, expected)
		}
	}
}

func TestWikidataResolverRejectsNonNumericIDsAndSkipsEmptyBatches(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("no request should be sent for an empty batch: %s", request.URL.String())
		return nil, nil
	})}
	// 只接受纯数字的豆瓣 ID，既是数据校验也顺带杜绝了查询注入。
	mapping, err := NewWikidataResolver(client, "", "").Resolve(t.Context(), []string{`" } INJECT {`, "abc"})
	if err != nil || len(mapping) != 0 {
		t.Fatalf("mapping = %+v, err = %v", mapping, err)
	}
	if query := wikidataQuery([]string{"1292052", "bad"}); strings.Contains(query, "bad") {
		t.Fatalf("query kept an invalid ID: %s", query)
	}
}

func TestWikidataRateLimitIsReportedAsThrottle(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return statusResponse(request, http.StatusTooManyRequests, http.Header{"Retry-After": {"5"}}, `{}`), nil
	})}
	_, err := NewWikidataResolver(client, "https://wikidata.test/sparql", "MoovieBot/1.0").
		Resolve(t.Context(), []string{"1292052"})
	if retryAfter, throttled := workqueue.RetryAfter(err); !throttled || retryAfter != 5*time.Second {
		t.Fatalf("error = %v", err)
	}
}
