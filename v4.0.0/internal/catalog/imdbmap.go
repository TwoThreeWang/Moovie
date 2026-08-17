package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

// TaskIMDbBackfill 补齐豆瓣 ID 到 IMDb ID 的映射。
// 这件事从 tmdb 任务里拆出来，是因为映射查询和 TMDB 抓取的速率特性完全不同：
// TMDB 每秒能打几十次，而映射源要么只能批量查（Wikidata），要么限流极严（wmdb）。
// 混在一条路径上的结果是 worker 全部堵在映射查询上，真正该做的抓取反而排不上。
const TaskIMDbBackfill = "imdb_backfill"

const (
	defaultWMDBBase         = "https://api.wmdb.tv"
	defaultWikidataEndpoint = "https://query.wikidata.org/sparql"
	// defaultIMDbLookupInterval 是 wmdb 的最小发送间隔。这是个免费接口，
	// 多个调用方并发不配速就会被整片 429。
	defaultIMDbLookupInterval = 1200 * time.Millisecond
	// doubanFilmProperty 是 Wikidata 的「豆瓣电影 ID」属性，imdbProperty 是「IMDb ID」。
	// 两个属性挂在同一个条目上，所以一次查询就能把豆瓣 ID 翻译成 IMDb ID。
	doubanFilmProperty = "P4529"
	imdbProperty       = "P345"
)

// IMDbMappingStore 把两个阶段的尝试记录分开保存。共用一个时间戳会让批量源的
// 「查不到」结论无处安放：它既不算命中，又不能等兜底源轮到才记账，
// 否则这些条目会永远压在队首，新条目一辈子排不上。
type IMDbMappingStore interface {
	PendingIMDbBatchLookups(ctx context.Context, limit int, retryAfter time.Duration) ([]string, error)
	PendingIMDbFallbackLookups(ctx context.Context, limit int, retryAfter time.Duration) ([]string, error)
	SaveIMDbID(ctx context.Context, doubanID, imdbID string) error
	MarkIMDbBatchAttempt(ctx context.Context, doubanIDs []string) error
	MarkIMDbLookupAttempt(ctx context.Context, doubanIDs []string) error
}

// BatchIMDbResolver 一次翻译一批豆瓣 ID，返回命中的部分。
type BatchIMDbResolver interface {
	Resolve(ctx context.Context, doubanIDs []string) (map[string]string, error)
}

// SingleIMDbResolver 是逐条查询的兜底源。Wait 阻塞到下一个可用发送时刻，
// 由调用方用 ctx 决定愿意等多久——这里必须是阻塞语义。
// 早先用的非阻塞 Allow() 一探到「还没到点」就收手，结果是上游响应越快、
// 单轮能处理的条目越少（快到 0.3 秒时每轮只做 1 条），时间预算一次也用不满。
type SingleIMDbResolver interface {
	Wait(ctx context.Context) error
	ResolveOne(ctx context.Context, doubanID string) (string, error)
}

type IMDbBackfillHandler struct {
	store             IMDbMappingStore
	queue             RefreshQueue
	batch             BatchIMDbResolver
	fallback          SingleIMDbResolver
	batchSize         int
	fallbackBatchSize int
	budget            time.Duration
	retryAfter        time.Duration
	batchRetryAfter   time.Duration
	logger            *slog.Logger
}

// stageStats 只为日志服务。把计数拆开是因为旧日志里的 resolved=0 settled=0
// 有好几种完全不同的成因，看不出到底是查不到、没查、还是被限流了。
type stageStats struct {
	candidates int
	asked      int
	hits       int
	misses     int
}

type IMDbBackfillOption func(*IMDbBackfillHandler)

// WithIMDbFallback 配置逐条兜底源（wmdb）。不配置时只用批量源。
func WithIMDbFallback(resolver SingleIMDbResolver) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) { handler.fallback = resolver }
}

// WithIMDbBatchSize 覆盖批量阶段单轮处理的对象数量（一次 SPARQL 查询问多少条）。
func WithIMDbBatchSize(size int) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) {
		if size > 0 {
			handler.batchSize = size
		}
	}
}

// WithIMDbFallbackBudget 限制兜底阶段占用执行槽的时长。
func WithIMDbFallbackBudget(budget time.Duration) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) {
		if budget > 0 {
			handler.budget = budget
		}
	}
}

// WithIMDbRetryAfter 覆盖兜底源「查不到」的重查间隔。
func WithIMDbRetryAfter(retryAfter time.Duration) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) {
		if retryAfter > 0 {
			handler.retryAfter = retryAfter
		}
	}
}

// WithIMDbBatchRetryAfter 覆盖批量源的重扫间隔。批量查询几乎免费，
// 所以这个间隔可以比兜底源短得多——Wikidata 上的映射会随时间新增。
func WithIMDbBatchRetryAfter(retryAfter time.Duration) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) {
		if retryAfter > 0 {
			handler.batchRetryAfter = retryAfter
		}
	}
}

// WithIMDbFallbackBatchSize 覆盖兜底阶段单轮取多少候选。
// 取多了不会浪费配额：没轮到的不打标记，下一轮会重新捞出来。
func WithIMDbFallbackBatchSize(size int) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) {
		if size > 0 {
			handler.fallbackBatchSize = size
		}
	}
}

func NewIMDbBackfillHandler(store IMDbMappingStore, queue RefreshQueue, batch BatchIMDbResolver, options ...IMDbBackfillOption) *IMDbBackfillHandler {
	handler := &IMDbBackfillHandler{store: store, queue: queue, batch: batch,
		batchSize: 200, fallbackBatchSize: 32, budget: 20 * time.Second,
		retryAfter: 30 * 24 * time.Hour, batchRetryAfter: 7 * 24 * time.Hour, logger: slog.Default()}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Handle 跑两个互不阻塞的阶段。两者用各自的候选队列和各自的时间戳，
// 因为它们的成本差了三个数量级：批量源一次请求解决 200 条，兜底源一条要 1.2 秒。
// 早先两个阶段共用一份名单，慢的那个就把快的那个拖死了。
func (handler *IMDbBackfillHandler) Handle(ctx context.Context, _ workqueue.Job) error {
	if handler.store == nil || handler.batch == nil {
		return workqueue.Terminal(fmt.Errorf("IMDb 映射回填未配置"))
	}
	batchStats, err := handler.runBatchStage(ctx)
	if err != nil {
		return err
	}
	fallbackStats := handler.runFallbackStage(ctx)
	if batchStats.candidates == 0 && fallbackStats.candidates == 0 {
		return nil
	}
	handler.logger.Info("IMDb 映射回填",
		"batch_candidates", batchStats.candidates, "batch_hits", batchStats.hits,
		"fallback_candidates", fallbackStats.candidates, "fallback_asked", fallbackStats.asked,
		"fallback_hits", fallbackStats.hits, "fallback_misses", fallbackStats.misses)
	return nil
}

// runBatchStage 用一次 SPARQL 查询扫过一批候选，并且无论命中与否都记账。
func (handler *IMDbBackfillHandler) runBatchStage(ctx context.Context) (stageStats, error) {
	candidates, err := handler.store.PendingIMDbBatchLookups(ctx, handler.batchSize, handler.batchRetryAfter)
	if err != nil || len(candidates) == 0 {
		return stageStats{}, err
	}
	resolved, err := handler.batch.Resolve(ctx, candidates)
	if err != nil {
		// 批量源不可用时一个字都不记，这批留给下一轮；更不要顺势去打兜底源，
		// 那是限流最严的一个，用它去扛整批流量正是这套设计要避免的事。
		return stageStats{}, err
	}
	for _, doubanID := range candidates {
		if imdbID := resolved[doubanID]; imdbID != "" {
			handler.persist(ctx, doubanID, imdbID)
		}
	}
	// 关键：SPARQL 返回 200 而某个 ID 没有 binding，是一个确定的结论——
	// Wikidata 上就是没有 P4529→P345 的路径。整批无条件记账，队首才会往前滚动。
	// 这些条目随后会进入兜底队列，由 wmdb 再确认一次，所以记账并不等于放弃它们。
	if err := handler.store.MarkIMDbBatchAttempt(ctx, candidates); err != nil {
		return stageStats{}, err
	}
	return stageStats{candidates: len(candidates), hits: len(resolved)}, nil
}

// runFallbackStage 在时间预算内用兜底源啃批量源确认查不到的条目。
// 这个阶段的失败不该让整个任务重试：批量阶段已经提交了成果，
// 重试只会让那批候选被重新扫一遍。
func (handler *IMDbBackfillHandler) runFallbackStage(ctx context.Context) stageStats {
	if handler.fallback == nil {
		return stageStats{}
	}
	candidates, err := handler.store.PendingIMDbFallbackLookups(ctx, handler.fallbackBatchSize, handler.retryAfter)
	if err != nil {
		handler.logger.Warn("读取 IMDb 兜底候选失败", "error", err)
		return stageStats{}
	}
	if len(candidates) == 0 {
		return stageStats{}
	}
	stats := stageStats{candidates: len(candidates)}
	// 预算只限定这个阶段占用执行槽的时长。在预算之内等限流器是应该的：
	// 1.2 秒的间隔本来就是配速的一部分，等它才能把 20 秒用满。
	budgetCtx, cancel := context.WithTimeout(ctx, handler.budget)
	defer cancel()
	settled := make([]string, 0, len(candidates))
	for _, doubanID := range candidates {
		if err := handler.fallback.Wait(budgetCtx); err != nil {
			// 预算用完，没轮到的一律不打标记，下一轮会重新捞出来。
			break
		}
		// asked 统计真正发出去的请求，hits+misses 统计拿到确定结论的。
		// 两者之差就是失败次数，不必再单独数一个计数器。
		stats.asked++
		// 这里刻意用 ctx 而不是 budgetCtx：请求已经拿到发送许可，
		// 用预算去掐断一个进行中的请求只会白白浪费一个限流名额。
		imdbID, err := handler.fallback.ResolveOne(ctx, doubanID)
		if err != nil {
			// 限流和网络故障都不该让这个对象被记成「查过了」，
			// 否则它要等满一个重查周期才会被再看一眼。
			handler.logger.Warn("IMDb 兜底查询失败", "douban_id", doubanID, "error", err)
			if workqueue.IsThrottled(err) {
				break
			}
			continue
		}
		if imdbID != "" {
			handler.persist(ctx, doubanID, imdbID)
			stats.hits++
		} else {
			stats.misses++
		}
		settled = append(settled, doubanID)
	}
	if err := handler.store.MarkIMDbLookupAttempt(ctx, settled); err != nil {
		handler.logger.Error("记录 IMDb 兜底尝试失败", "count", len(settled), "error", err)
	}
	return stats
}

// persist 写入映射并把 TMDB 抓取重新排进队列。回填是唯一知道「刚刚多了新映射」的地方，
// 不在这里触发的话，这部片子要等下一轮 24 小时的资料刷新才会补上剧照。
func (handler *IMDbBackfillHandler) persist(ctx context.Context, doubanID, imdbID string) {
	if err := handler.store.SaveIMDbID(ctx, doubanID, imdbID); err != nil {
		handler.logger.Error("保存 IMDb 映射失败", "douban_id", doubanID, "imdb_id", imdbID, "error", err)
		return
	}
	if handler.queue == nil {
		return
	}
	if _, err := handler.queue.EnqueueRefresh(ctx, doubanID, RefreshProviderTMDB, "imdb_backfill", 0); err != nil {
		handler.logger.Error("回填后重新入队 TMDB 任务失败", "douban_id", doubanID, "error", err)
	}
}

// WikidataResolver 用一次 SPARQL 查询翻译一整批豆瓣 ID。
// 相比逐条查询的映射站，它的价值不在单次速度，而在于 200 个 ID 只算一次请求。
type WikidataResolver struct {
	client    *http.Client
	endpoint  string
	userAgent string
}

func NewWikidataResolver(client *http.Client, endpoint, userAgent string) *WikidataResolver {
	if endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/"); endpoint == "" {
		endpoint = defaultWikidataEndpoint
	}
	// 维基媒体要求请求带上能说明来源和联系方式的 User-Agent，默认 UA 会被直接拒绝。
	if userAgent = strings.TrimSpace(userAgent); userAgent == "" {
		userAgent = "MoovieBot/1.0 (https://github.com/TwoThreeWang/Moovie)"
	}
	return &WikidataResolver{client: client, endpoint: endpoint, userAgent: userAgent}
}

func (resolver *WikidataResolver) Resolve(ctx context.Context, doubanIDs []string) (map[string]string, error) {
	query := wikidataQuery(doubanIDs)
	if query == "" {
		return map[string]string{}, nil
	}
	form := url.Values{"query": {query}, "format": {"json"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resolver.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/sparql-results+json")
	request.Header.Set("User-Agent", resolver.userAgent)
	response, err := resolver.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("query Wikidata: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, classifyUpstreamStatus("Wikidata", response)
	}
	var payload struct {
		Results struct {
			Bindings []struct {
				Douban struct {
					Value string `json:"value"`
				} `json:"douban"`
				IMDb struct {
					Value string `json:"value"`
				} `json:"imdb"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode Wikidata response: %w", err)
	}
	mapping := make(map[string]string, len(payload.Results.Bindings))
	for _, binding := range payload.Results.Bindings {
		doubanID, imdbID := strings.TrimSpace(binding.Douban.Value), strings.TrimSpace(binding.IMDb.Value)
		if doubanID != "" && imdbID != "" {
			mapping[doubanID] = imdbID
		}
	}
	return mapping, nil
}

// wikidataQuery 拼 VALUES 子句。只接受纯数字的豆瓣 ID，既是数据校验也顺带杜绝了查询注入。
func wikidataQuery(doubanIDs []string) string {
	values := make([]string, 0, len(doubanIDs))
	for _, doubanID := range doubanIDs {
		if validDoubanID(doubanID) {
			values = append(values, `"`+doubanID+`"`)
		}
	}
	if len(values) == 0 {
		return ""
	}
	return fmt.Sprintf(`SELECT ?douban ?imdb WHERE { VALUES ?douban { %s } ?item wdt:%s ?douban; wdt:%s ?imdb. }`,
		strings.Join(values, " "), doubanFilmProperty, imdbProperty)
}

// WMDBResolver 是逐条查询的兜底源。它限流极严，所以自带配速器，
// 并且用非阻塞的 Allow 暴露配额状态——调用方应当择日再来，而不是在这里干等。
type WMDBResolver struct {
	client  *http.Client
	base    string
	limiter *outbound.Limiter
}

func NewWMDBResolver(client *http.Client, base string, interval time.Duration) *WMDBResolver {
	if base = strings.TrimRight(strings.TrimSpace(base), "/"); base == "" {
		base = defaultWMDBBase
	}
	if interval <= 0 {
		interval = defaultIMDbLookupInterval
	}
	return &WMDBResolver{client: client, base: base, limiter: outbound.NewLimiter(interval)}
}

// Wait 阻塞到下一个可用发送时刻，或在 ctx 到期时返回错误。
// 调用方用 ctx 的 deadline 表达「我最多愿意等这么久」。
func (resolver *WMDBResolver) Wait(ctx context.Context) error { return resolver.limiter.Wait(ctx) }

// Allow 是非阻塞版本，保留给只想探一下配额状态、不打算等待的调用方。
func (resolver *WMDBResolver) Allow() bool { return resolver.limiter.Allow() }

func (resolver *WMDBResolver) ResolveOne(ctx context.Context, doubanID string) (string, error) {
	if !validDoubanID(doubanID) {
		return "", workqueue.Terminal(fmt.Errorf("invalid Douban ID %q", doubanID))
	}
	endpoint := resolver.base + "/movie/api?id=" + url.QueryEscape(doubanID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := resolver.client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		// 上游确实没有这个条目，不是故障，按「查不到」处理即可。
		return "", nil
	}
	if response.StatusCode != http.StatusOK {
		err := classifyUpstreamStatus("upstream", response)
		if retryAfter, throttled := workqueue.RetryAfter(err); throttled {
			if retryAfter <= 0 {
				retryAfter = 30 * time.Second
			}
			resolver.limiter.Pause(retryAfter)
		}
		return "", err
	}
	var payload struct {
		IMDbID string `json:"imdbId"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode upstream response: %w", err)
	}
	return strings.TrimSpace(payload.IMDbID), nil
}
