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

type IMDbMappingStore interface {
	PendingIMDbLookups(ctx context.Context, limit int, retryAfter time.Duration) ([]string, error)
	SaveIMDbID(ctx context.Context, doubanID, imdbID string) error
	MarkIMDbLookupAttempt(ctx context.Context, doubanIDs []string) error
}

// BatchIMDbResolver 一次翻译一批豆瓣 ID，返回命中的部分。
type BatchIMDbResolver interface {
	Resolve(ctx context.Context, doubanIDs []string) (map[string]string, error)
}

// SingleIMDbResolver 是逐条查询的兜底源。Allow 返回 false 表示当前配额已用完，
// 调用方应当停下而不是等待——等待只会白占一个执行槽。
type SingleIMDbResolver interface {
	Allow() bool
	ResolveOne(ctx context.Context, doubanID string) (string, error)
}

type IMDbBackfillHandler struct {
	store      IMDbMappingStore
	queue      RefreshQueue
	batch      BatchIMDbResolver
	fallback   SingleIMDbResolver
	batchSize  int
	budget     time.Duration
	retryAfter time.Duration
	logger     *slog.Logger
}

type IMDbBackfillOption func(*IMDbBackfillHandler)

// WithIMDbFallback 配置逐条兜底源（wmdb）。不配置时只用批量源。
func WithIMDbFallback(resolver SingleIMDbResolver) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) { handler.fallback = resolver }
}

// WithIMDbBatchSize 覆盖单轮处理的对象数量。
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

// WithIMDbRetryAfter 覆盖「查不到」的重查间隔。
func WithIMDbRetryAfter(retryAfter time.Duration) IMDbBackfillOption {
	return func(handler *IMDbBackfillHandler) {
		if retryAfter > 0 {
			handler.retryAfter = retryAfter
		}
	}
}

func NewIMDbBackfillHandler(store IMDbMappingStore, queue RefreshQueue, batch BatchIMDbResolver, options ...IMDbBackfillOption) *IMDbBackfillHandler {
	handler := &IMDbBackfillHandler{store: store, queue: queue, batch: batch,
		batchSize: 200, budget: 20 * time.Second, retryAfter: 30 * 24 * time.Hour, logger: slog.Default()}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// Handle 分两个阶段：先用一次批量查询解决绝大多数，剩下的交给限流严重的兜底源，
// 并且只在时间预算内处理——没轮到的这一轮不打标记，下一轮自然会再被捞出来。
func (handler *IMDbBackfillHandler) Handle(ctx context.Context, _ workqueue.Job) error {
	if handler.store == nil || handler.batch == nil {
		return workqueue.Terminal(fmt.Errorf("IMDb 映射回填未配置"))
	}
	candidates, err := handler.store.PendingIMDbLookups(ctx, handler.batchSize, handler.retryAfter)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	resolved, err := handler.batch.Resolve(ctx, candidates)
	if err != nil {
		// 批量源不可用时不要顺势去打兜底源：那是限流最严的一个，
		// 用它去扛整批流量正是这次改造要避免的事。
		return err
	}
	settled := make([]string, 0, len(candidates))
	for _, doubanID := range candidates {
		if imdbID := resolved[doubanID]; imdbID != "" {
			handler.persist(ctx, doubanID, imdbID)
			settled = append(settled, doubanID)
		}
	}
	settled = append(settled, handler.resolveRemainder(ctx, candidates, resolved)...)
	if err := handler.store.MarkIMDbLookupAttempt(ctx, settled); err != nil {
		return err
	}
	handler.logger.Info("IMDb 映射回填", "candidates", len(candidates),
		"resolved", len(resolved), "settled", len(settled))
	return nil
}

// resolveRemainder 在时间预算内用兜底源处理批量源没覆盖到的对象，返回已经处理完的豆瓣 ID。
func (handler *IMDbBackfillHandler) resolveRemainder(ctx context.Context, candidates []string, resolved map[string]string) []string {
	if handler.fallback == nil {
		return nil
	}
	deadline := time.Now().Add(handler.budget)
	settled := make([]string, 0, len(candidates))
	for _, doubanID := range candidates {
		if resolved[doubanID] != "" {
			continue
		}
		if time.Now().After(deadline) || !handler.fallback.Allow() {
			break
		}
		imdbID, err := handler.fallback.ResolveOne(ctx, doubanID)
		if err != nil {
			// 限流和网络故障都不该让这个对象被标记成「查过了」，
			// 否则它要等满一个重查周期才会被再看一眼。
			handler.logger.Warn("IMDb 兜底查询失败", "douban_id", doubanID, "error", err)
			if workqueue.IsThrottled(err) {
				break
			}
			continue
		}
		if imdbID != "" {
			handler.persist(ctx, doubanID, imdbID)
		}
		settled = append(settled, doubanID)
	}
	return settled
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
