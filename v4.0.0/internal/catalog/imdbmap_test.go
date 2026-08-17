package catalog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

type imdbStoreStub struct {
	pending         []string
	fallbackPending []string
	saved           map[string]string
	marked          []string
	batchMarked     []string
	limit           int
	fallbackLimit   int
}

// newIMDbStoreStub 让两个队列默认返回同一批候选，这样单阶段的断言不用重复铺设数据。
// 需要区分两个队列时直接改 fallbackPending。
func newIMDbStoreStub(pending ...string) *imdbStoreStub {
	return &imdbStoreStub{pending: pending, fallbackPending: pending, saved: map[string]string{}}
}

func (stub *imdbStoreStub) PendingIMDbBatchLookups(_ context.Context, limit int, _ time.Duration) ([]string, error) {
	stub.limit = limit
	return stub.pending, nil
}

func (stub *imdbStoreStub) PendingIMDbFallbackLookups(_ context.Context, limit int, _ time.Duration) ([]string, error) {
	stub.fallbackLimit = limit
	return stub.fallbackPending, nil
}

func (stub *imdbStoreStub) SaveIMDbID(_ context.Context, doubanID, imdbID string) error {
	stub.saved[doubanID] = imdbID
	return nil
}

func (stub *imdbStoreStub) MarkIMDbBatchAttempt(_ context.Context, doubanIDs []string) error {
	stub.batchMarked = append(stub.batchMarked, doubanIDs...)
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

// singleResolverStub 内嵌一个真实的 outbound.Limiter，而不是用计数器假装配额。
// 这一点很要紧：旧 stub 的 Allow() 是「还剩几次」，真实限流器是「到点了吗」，
// 两种语义在测试里长得一样，于是「上游越快、单轮处理越少」的退化一路溜到了线上。
type singleResolverStub struct {
	mapping map[string]string
	asked   []string
	err     error
	limiter *outbound.Limiter
	rtt     time.Duration
}

func newSingleResolverStub(interval time.Duration, mapping map[string]string) *singleResolverStub {
	if mapping == nil {
		mapping = map[string]string{}
	}
	return &singleResolverStub{mapping: mapping, limiter: outbound.NewLimiter(interval)}
}

func (stub *singleResolverStub) Wait(ctx context.Context) error { return stub.limiter.Wait(ctx) }

func (stub *singleResolverStub) ResolveOne(_ context.Context, doubanID string) (string, error) {
	stub.asked = append(stub.asked, doubanID)
	if stub.rtt > 0 {
		time.Sleep(stub.rtt)
	}
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
	// 批量阶段不管命中与否都要记账，否则队首永远不会往前滚动。
	if len(store.batchMarked) != 2 {
		t.Fatalf("batch marked = %v", store.batchMarked)
	}
}

// 这是线上 candidates=200 resolved=0 settled=0 无限循环的回归用例。
// 批量源明确回答「这些我这儿都没有」是一个确定结论，必须落到 imdb_batch_lookup_at 上；
// 只有兜底源摸到过才记账的话，这批候选会永远压在队首，新条目一辈子排不上。
func TestBackfillMarksBatchMissesEvenWhenTheFallbackIsUnavailable(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864", "1296139")
	batch := &batchResolverStub{mapping: map[string]string{}}
	handler := NewIMDbBackfillHandler(store, nil, batch) // 完全不配兜底源

	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	if len(store.batchMarked) != 3 {
		t.Fatalf("批量源零命中时仍须整批记账，实际 = %v", store.batchMarked)
	}
	// 兜底源没跑过，就不该在兜底那一列留下痕迹。
	if len(store.marked) != 0 {
		t.Fatalf("fallback marked = %v", store.marked)
	}
}

func TestBackfillRunsTheFallbackOnItsOwnQueue(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864")
	// 兜底队列由 SQL 保证只含批量源已给过结论、且仍然没有 IMDb ID 的条目。
	store.fallbackPending = []string{"1296139", "1300267"}
	batch := &batchResolverStub{mapping: map[string]string{"1292052": "tt0111161"}}
	fallback := newSingleResolverStub(time.Millisecond, map[string]string{"1296139": "tt0468569"})
	handler := NewIMDbBackfillHandler(store, nil, batch, WithIMDbFallback(fallback))

	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	// 批量源已经解决的对象不会出现在兜底队列里，也就不会再去打限流严重的那个源。
	if len(fallback.asked) != 2 || fallback.asked[0] != "1296139" || fallback.asked[1] != "1300267" {
		t.Fatalf("fallback asked = %v", fallback.asked)
	}
	if store.saved["1292052"] != "tt0111161" || store.saved["1296139"] != "tt0468569" {
		t.Fatalf("saved = %+v", store.saved)
	}
	// 两个阶段各记自己那一列，互不干扰。
	if len(store.batchMarked) != 2 || len(store.marked) != 2 {
		t.Fatalf("batch marked = %v fallback marked = %v", store.batchMarked, store.marked)
	}
}

// 这是「上游越快、单轮处理越少」的回归用例。
// 旧实现用非阻塞的 Allow()，一探到「还没到 1.2 秒」就 break，
// 于是上游响应 0.3 秒时每轮只能处理 1 条，20 秒预算一次也用不满。
func TestFallbackKeepsWorkingThroughTheBudgetWhenTheUpstreamIsFast(t *testing.T) {
	// 40 条候选按 10ms 间隔全部处理完需要 400ms，而预算只给 150ms，
	// 所以这一轮必然处理不完——正好同时检验「用得够多」和「按预算截断」两件事。
	const candidateCount = 40
	candidates := make([]string, 0, candidateCount)
	for index := 0; index < candidateCount; index++ {
		candidates = append(candidates, strconv.Itoa(1300000+index))
	}
	store := newIMDbStoreStub()
	store.fallbackPending = candidates
	// 上游比限流间隔快得多：这正是旧实现会退化成 1 条/轮的场景。
	fallback := newSingleResolverStub(10*time.Millisecond, nil)
	fallback.rtt = time.Millisecond
	handler := NewIMDbBackfillHandler(store, nil, &batchResolverStub{mapping: map[string]string{}},
		WithIMDbFallback(fallback), WithIMDbFallbackBudget(150*time.Millisecond))

	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	// 卡在 1 条就说明又退回非阻塞语义了。机器再慢也只会让这个数更小，所以下界留得很宽。
	if len(fallback.asked) < 5 {
		t.Fatalf("兜底阶段只处理了 %d 条，预算没有被用满：asked = %v", len(fallback.asked), fallback.asked)
	}
	if len(store.marked) != len(fallback.asked) {
		t.Fatalf("asked = %d marked = %d", len(fallback.asked), len(store.marked))
	}
	// 预算之外的条目一律不记账，下一轮才会被重新捞出来。
	if len(store.marked) >= candidateCount {
		t.Fatalf("预算应当在处理完全部候选之前就截断：marked = %d", len(store.marked))
	}
}

func TestBackfillStopsAtTheFirstThrottleAndNeverFallsBackOnBatchFailure(t *testing.T) {
	store := newIMDbStoreStub("1292052", "1291864")
	fallback := newSingleResolverStub(time.Millisecond, nil)
	fallback.err = workqueue.Throttled(errors.New("upstream returned HTTP 429"), time.Second)
	handler := NewIMDbBackfillHandler(store, nil, &batchResolverStub{mapping: map[string]string{}}, WithIMDbFallback(fallback))
	if err := handler.Handle(t.Context(), workqueue.Job{}); err != nil {
		t.Fatal(err)
	}
	// 撞上限流就收手，剩下的留给下一轮，别把配额继续喂给一个正在拒绝的上游。
	if len(fallback.asked) != 1 || len(store.marked) != 0 {
		t.Fatalf("asked = %v marked = %v", fallback.asked, store.marked)
	}
	// 兜底源被限流不影响批量阶段已经拿到的成果。
	if len(store.batchMarked) != 2 {
		t.Fatalf("batch marked = %v", store.batchMarked)
	}

	failing := &batchResolverStub{err: errors.New("wikidata is down")}
	untouched := newSingleResolverStub(time.Millisecond, nil)
	brokenStore := newIMDbStoreStub("1292052")
	handler = NewIMDbBackfillHandler(brokenStore, nil, failing, WithIMDbFallback(untouched))
	if err := handler.Handle(t.Context(), workqueue.Job{}); err == nil {
		t.Fatal("batch failure must surface")
	}
	// 批量源挂了不代表可以用兜底源去扛整批流量。
	if len(untouched.asked) != 0 {
		t.Fatalf("fallback was used as a batch replacement: %v", untouched.asked)
	}
	// 批量源报错时一个字都不记，这批候选要完整留给下一轮。
	if len(brokenStore.batchMarked) != 0 {
		t.Fatalf("batch marked on failure = %v", brokenStore.batchMarked)
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
