package search

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/cache"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	"golang.org/x/sync/singleflight"
)

// ServiceConfig 保存一次跨来源搜索及媒体匹配所允许使用的时间、并发和阈值预算。
type ServiceConfig struct {
	SourceTimeout time.Duration
	TotalTimeout  time.Duration
	// RefreshWait 是「本地已经有结果时，最多再等上游刷新多久」。
	// 0 表示用 defaultRefreshWait。它必须远小于 TotalTimeout，
	// 否则搜索页会一直干等最慢的那个资源站。
	RefreshWait               time.Duration
	SourceMaxConcurrency      int
	PersistRetries            int
	ResourceMatchShadow       bool
	ResourceMatchAutoApply    bool
	MediaAutoMatchThreshold   float64
	MediaReviewMatchThreshold float64
}

// Service 协调本地搜索、上游刷新、版权过滤和规范媒体身份匹配。
type Service struct {
	items         ItemStore
	sites         SiteStore
	filters       FilterStore
	crawler       SourceCrawler
	health        HealthMonitor
	runner        BackgroundRunner
	config        ServiceConfig
	singleflight  singleflight.Group
	identity      MediaIdentity
	episodes      ResourceEpisodeIndexer
	identityCache *cache.TTL[mediaIdentityResult]
}

// mediaIdentityResult 是媒体匹配结果的缓存值；found=false 表示"确认匹配不上"，同样要缓存以免反复计算。
type mediaIdentityResult struct {
	mediaID    int
	confidence float64
	matchedBy  string
	found      bool
}

// MediaIdentity 是资源搜索与规范媒体表之间的窄接口。migration 0013 尚未应用时，
// 实现可以返回错误，但搜索必须退回旧字段继续工作。
//
// 匹配按以下层级依次尝试，命中后立即停止：
//  1. FindResourceLink：已有人工确认或已应用的关联
//  2. FindByDoubanID：豆瓣 ID 精确匹配，置信度 1.0
//  3. FindByExternalID：IMDb/TMDB ID 匹配，置信度 1.0
//  4. FindByTitleYearType：规范标题、年份和类型精确匹配，置信度 0.95
//  5. ScoredMediaIdentity.MatchResource：多特征加权评分
//  6. 低于阈值：进入待复核，不强制合并
type MediaIdentity interface {
	FindResourceLink(ctx context.Context, sourceKey, vodID string) (mediaID int, confidence float64, matchedBy string, err error)
	FindByDoubanID(ctx context.Context, doubanID string) (mediaID int, err error)
	FindByExternalID(ctx context.Context, provider, externalID string) (mediaID int, err error)
	FindByTitleYearType(ctx context.Context, title, year, mediaType string) (mediaID int, err error)
	FindByTitleYear(ctx context.Context, title, year string) (mediaID int, err error)
	LinkResource(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error
}

// MatchCandidateRecorder 记录待复核的匹配候选（简版）。
type MatchCandidateRecorder interface {
	RecordMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error
}

// DetailedMatchCandidateRecorder 记录带打分理由的匹配候选，后台复核页会展示这些理由。
type DetailedMatchCandidateRecorder interface {
	RecordDetailedMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error
}

// MediaMatchRequest 是送去打分的资源特征。
type MediaMatchRequest struct {
	Title         string
	OriginalTitle string
	Year          string
	MediaType     string
	Actors        string
	Directors     string
}

// MediaMatchResult 是打分结果；HardConflict 非空表示存在硬冲突（例如年份差太多），必须直接拒绝。
type MediaMatchResult struct {
	MediaID      int
	Confidence   float64
	MatchedBy    string
	Status       string
	ReasonJSON   string
	HardConflict string
}

// ScoredMediaIdentity 是第 4 层加权打分匹配的接口。
type ScoredMediaIdentity interface {
	MatchResource(ctx context.Context, request MediaMatchRequest) (MediaMatchResult, error)
}

// ResourceEpisodeIndexer 把资源的播放地址解析成结构化的剧集候选。
type ResourceEpisodeIndexer interface {
	IndexResourceEpisodes(ctx context.Context, item VodItem) error
}

// ServiceOption 是 Service 的可选装配项。
type ServiceOption func(*Service)

// WithMediaIdentity 注入媒体身份匹配实现；不注入时搜索仍可用，只是不做归一。
func WithMediaIdentity(identity MediaIdentity) ServiceOption {
	return func(service *Service) { service.identity = identity }
}

// WithResourceEpisodeIndexer 注入剧集索引器。
func WithResourceEpisodeIndexer(indexer ResourceEpisodeIndexer) ServiceOption {
	return func(service *Service) { service.episodes = indexer }
}

// NewService 应用安全默认值并创建有界身份缓存；所有上游异步任务必须经 runner 执行。
func NewService(items ItemStore, sites SiteStore, filters FilterStore, crawler SourceCrawler, health HealthMonitor, runner BackgroundRunner, cfg ServiceConfig, options ...ServiceOption) *Service {
	if cfg.SourceTimeout <= 0 {
		cfg.SourceTimeout = 10 * time.Second
	}
	if cfg.TotalTimeout <= 0 {
		cfg.TotalTimeout = 30 * time.Second
	}
	if cfg.SourceMaxConcurrency <= 0 {
		cfg.SourceMaxConcurrency = 6
	}
	if cfg.PersistRetries <= 0 {
		cfg.PersistRetries = 2
	}
	if cfg.MediaAutoMatchThreshold <= 0 || cfg.MediaAutoMatchThreshold > 1 {
		cfg.MediaAutoMatchThreshold = 0.88
	}
	if cfg.MediaReviewMatchThreshold <= 0 || cfg.MediaReviewMatchThreshold >= cfg.MediaAutoMatchThreshold {
		cfg.MediaReviewMatchThreshold = 0.68
	}
	service := &Service{items: items, sites: sites, filters: filters, crawler: crawler, health: health, runner: runner, config: cfg,
		identityCache: cache.New[mediaIdentityResult](5000, 30*time.Minute)}
	for _, option := range options {
		option(service)
	}
	return service
}

// Search 先读取本地结果，再在配置了上游搜索时合并本次刷新结果。
// 数据库或上游失败时仍降级为空结果或旧结果，不把整个 HTMX 响应变成 500。
func (service *Service) Search(ctx context.Context, keyword string, bypassFilter bool) (*Result, error) {
	items, err := service.items.Search(ctx, keyword)
	if err != nil {
		requestmeta.Logger(ctx).Warn("local search failed", "error", err)
	}

	if len(items) == 0 {
		value, refreshErr, _ := service.singleflight.Do(keyword, func() (any, error) {
			return service.fetchAndSave(ctx, keyword)
		})
		if refreshErr != nil {
			requestmeta.Logger(ctx).Warn("synchronous source refresh failed", "error", refreshErr)
		} else if value != nil {
			items = value.([]VodItem)
		}
	} else if service.runner != nil {
		if fresh, arrived := service.refreshWithinBudget(ctx, keyword); arrived && fresh != nil {
			items = mergeSearchItems(items, fresh)
		}
	}
	service.enrichMediaIdentity(ctx, items)

	filteredCount := 0
	if !bypassFilter {
		items, filteredCount = service.filterCopyright(ctx, items)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AvgSpeedMs == 0 && items[j].AvgSpeedMs == 0 {
			return false
		}
		if items[i].AvgSpeedMs > 0 && items[j].AvgSpeedMs == 0 {
			return true
		}
		if items[i].AvgSpeedMs == 0 && items[j].AvgSpeedMs > 0 {
			return false
		}
		return items[i].AvgSpeedMs < items[j].AvgSpeedMs
	})
	return &Result{Items: items, FilteredCount: filteredCount}, nil
}

// mergeSearchItems 用 source_key+vod_id 做键，把本次上游刷新结果覆盖到本地结果上。
func mergeSearchItems(local, refreshed []VodItem) []VodItem {
	merged := append([]VodItem(nil), local...)
	indexes := make(map[string]int, len(merged))
	for index, item := range merged {
		indexes[item.SourceKey+"\x00"+item.VodId] = index
	}
	for _, item := range refreshed {
		key := item.SourceKey + "\x00" + item.VodId
		if index, found := indexes[key]; found {
			merged[index] = item
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, item)
	}
	return merged
}

// enrichMediaIdentity 为每条资源执行五层媒体身份匹配：
//
//	第 0 层：已有资源关联（缓存或持久化）
//	第 1 层：豆瓣 ID 精确匹配
//	第 2 层：IMDb/TMDB ID 匹配
//	第 3 层：规范标题、年份和类型精确匹配
//	第 4 层：标题、季和人物等特征加权评分
//	低于阈值：进入待复核，不强制合并
func (service *Service) enrichMediaIdentity(ctx context.Context, items []VodItem) {
	if service.identity == nil {
		return
	}
	for index := range items {
		item := &items[index]
		if item.MediaID > 0 {
			continue
		}
		cacheKey := item.SourceKey + "\x00" + item.VodId
		if cached, found := service.identityCache.Get(cacheKey); found {
			if cached.found {
				item.MediaID, item.MediaConfidence, item.MediaMatch = cached.mediaID, cached.confidence, cached.matchedBy
			}
			continue
		}

		// 第 0 层：读取已经确认的关联。
		if mediaID, confidence, matchedBy, err := service.identity.FindResourceLink(ctx, item.SourceKey, item.VodId); err == nil && mediaID > 0 &&
			(confidence >= service.config.MediaAutoMatchThreshold || matchedBy == "manual" || matchedBy == "verified") {
			identity := mediaIdentityResult{mediaID: mediaID, confidence: confidence, matchedBy: matchedBy, found: true}
			service.identityCache.Set(cacheKey, identity)
			item.MediaID, item.MediaConfidence, item.MediaMatch = mediaID, confidence, matchedBy
			continue
		}

		mediaID, matchedBy, confidence := 0, "", 0.0
		reasonJSON, candidateStatus := "", MatchStatusReview

		// 第 1 层：豆瓣 ID 精确命中，可以直接关联。
		if item.VodDoubanId != "" {
			if id, err := service.identity.FindByDoubanID(ctx, item.VodDoubanId); err == nil && id > 0 {
				mediaID, matchedBy, confidence = id, "douban_id", 1.0
			}
		}

		// 第 2 层：IMDb/TMDB 外部 ID 命中，可以直接关联。
		if mediaID <= 0 {
			if imdbID := extractIMDbID(item); imdbID != "" {
				if id, err := service.identity.FindByExternalID(ctx, "imdb", imdbID); err == nil && id > 0 {
					mediaID, matchedBy, confidence = id, "imdb_id", 1.0
				}
			}
		}

		// 第 3 层：规范标题、年份和类型全部相同，作为高置信度自动候选。
		if mediaID <= 0 && item.VodName != "" && item.VodYear != "" {
			normalizedType := normalizeMediaType(item.TypeName)
			if normalizedType != "" {
				if id, err := service.identity.FindByTitleYearType(ctx, item.VodName, item.VodYear, normalizedType); err == nil && id > 0 {
					mediaID, matchedBy, confidence = id, "title_year_type", 0.95
				}
			}
		}

		// 第 4 层：根据标题、季、导演和演员等特征加权评分。
		if mediaID <= 0 {
			if scorer, ok := service.identity.(ScoredMediaIdentity); ok {
				match, matchErr := scorer.MatchResource(ctx, MediaMatchRequest{Title: item.VodName,
					OriginalTitle: firstNonEmpty(item.VodEn, item.VodSub), Year: item.VodYear, MediaType: item.TypeName,
					Actors: item.VodActor, Directors: item.VodDirector})
				if matchErr == nil && match.MediaID > 0 {
					mediaID, confidence, matchedBy = match.MediaID, match.Confidence, match.MatchedBy
					reasonJSON, candidateStatus = match.ReasonJSON, normalizeMatchDecision(match.Status, MatchStatusReview)
					if match.HardConflict != "" {
						candidateStatus = MatchStatusRejected
					}
				}
			} else if item.VodYear != "" {
				if id, err := service.identity.FindByTitleYear(ctx, item.VodName, item.VodYear); err == nil && id > 0 {
					mediaID, matchedBy, confidence = id, "title_year", 0.7
				}
			}
		}

		// 所有层级都没有得到可接受的匹配。
		if mediaID <= 0 {
			service.identityCache.Set(cacheKey, mediaIdentityResult{})
			continue
		}

		// 存在明确冲突时直接拒绝，不能靠较低分数强行合并。
		if candidateStatus == MatchStatusRejected {
			service.identityCache.Set(cacheKey, mediaIdentityResult{})
			service.recordMatchCandidate(ctx, *item, mediaID, confidence, matchedBy, candidateStatus, reasonJSON)
			continue
		}

		// Shadow 模式绝不创建或暴露新的规范关联，即使外部 ID 精确匹配也一样。
		// 第 0 层仍可读取既有确认关联，新发现的候选必须等待复核。
		if !service.config.ResourceMatchAutoApply {
			service.identityCache.Set(cacheKey, mediaIdentityResult{})
			service.recordMatchCandidate(ctx, *item, mediaID, confidence, matchedBy, MatchStatusReview, reasonJSON)
			continue
		}
		if confidence < service.config.MediaAutoMatchThreshold {
			service.identityCache.Set(cacheKey, mediaIdentityResult{})
			service.recordMatchCandidate(ctx, *item, mediaID, confidence, matchedBy, MatchStatusReview, reasonJSON)
			continue
		}

		// 只有越过自动应用阈值且开关允许时，才真正写入关联。
		service.identityCache.Set(cacheKey, mediaIdentityResult{mediaID: mediaID, confidence: confidence, matchedBy: matchedBy, found: true})
		item.MediaID, item.MediaConfidence, item.MediaMatch = mediaID, confidence, matchedBy
		if err := service.identity.LinkResource(ctx, item.SourceKey, item.VodId, mediaID, confidence, matchedBy); err != nil {
			requestmeta.Logger(ctx).Debug("persist resource media link failed", "source", item.SourceKey, "vod_id", item.VodId, "error", err)
		}
	}
}

// imdbIDPattern 用来从文本里认出 IMDb ID。
var imdbIDPattern = regexp.MustCompile(`tt\d{7,}`)

// extractIMDbID 尝试从资源元数据中提取 IMDb ID（例如 tt1234567）。
// 部分资源站会把 IMDb ID 放在标签或正文中，而不是独立字段。
func extractIMDbID(item *VodItem) string {
	for _, field := range []string{item.VodTag, item.VodBlurb, item.VodContent, item.VodRemarks} {
		if match := imdbIDPattern.FindString(field); match != "" {
			return match
		}
	}
	return ""
}

// recordMatchCandidate 把低于自动阈值的匹配写进待复核表；只有开了 shadow 开关才记录。
func (service *Service) recordMatchCandidate(ctx context.Context, item VodItem, mediaID int, confidence float64, matchedBy, status, reasonJSON string) {
	if !service.config.ResourceMatchShadow || (status != MatchStatusRejected && confidence < service.config.MediaReviewMatchThreshold) {
		return
	}
	if recorder, ok := service.identity.(DetailedMatchCandidateRecorder); ok && reasonJSON != "" {
		if err := recorder.RecordDetailedMatchCandidate(ctx, item.SourceKey, item.VodId, mediaID, confidence, matchedBy, status, reasonJSON); err != nil {
			requestmeta.Logger(ctx).Debug("persist detailed resource match candidate failed", "source", item.SourceKey, "vod_id", item.VodId, "error", err)
		}
		return
	}
	if recorder, ok := service.identity.(MatchCandidateRecorder); ok {
		if err := recorder.RecordMatchCandidate(ctx, item.SourceKey, item.VodId, mediaID, confidence, matchedBy); err != nil {
			requestmeta.Logger(ctx).Debug("persist resource match candidate failed", "source", item.SourceKey, "vod_id", item.VodId, "error", err)
		}
	}
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// IsCopyrightRestricted 判断标题是否命中版权屏蔽词，命中时返回命中的关键词。
func (service *Service) IsCopyrightRestricted(ctx context.Context, title string) (bool, string) {
	keywords, err := service.filters.CopyrightKeywords(ctx)
	if err != nil {
		return false, ""
	}
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(title, keyword) {
			return true, keyword
		}
	}
	return false, ""
}

// fetchAndSave 向所有启用的资源站抓取一轮，逐条落库，再立即回填媒体关联。
// defaultRefreshWait 是本地已有结果时等待上游刷新的默认上限。
// 取 3 秒是因为绝大多数资源站在 1 秒内就返回了，慢于此的基本是超时或挂掉的站，
// 没必要让用户陪着一起等。
const defaultRefreshWait = 3 * time.Second

// refreshWithinBudget 触发一次上游刷新，但最多只等 RefreshWait。
//
// 为什么要有这个：本地已经有结果时，老写法会同步等 fetchAndSave 跑完才返回，
// 而它的预算是 SEARCH_TOTAL_TIMEOUT_SECONDS（默认 30 秒）。
// 于是只要有一个资源站卡住，搜索页就要转 30 秒圈才出内容——这正是「页面打开了、
// 内容半天不出来」的主要来源。
//
// 现在改成：等一小会儿，上游及时回来就照旧合并（用户看到的东西一点没少）；
// 回不来就先把本地结果给用户，刷新任务用脱离请求的 context 在后台跑完并落库，
// 下一次搜索（以及同一关键词的其它并发请求）就能拿到完整结果。
//
// 返回值第二项为 false 表示这次没等到，调用方应当直接用本地结果。
func (service *Service) refreshWithinBudget(ctx context.Context, keyword string) ([]VodItem, bool) {
	// 必须脱离请求 context：请求一结束 ctx 就被取消，后台那半截刷新会跟着夭折，
	// 结果是既没让用户等到、也没把数据存下来，等于白跑。
	detached := context.WithoutCancel(ctx)
	outcome := service.singleflight.DoChan(keyword, func() (any, error) {
		return service.fetchAndSave(detached, keyword)
	})
	budget := service.config.RefreshWait
	if budget <= 0 {
		budget = defaultRefreshWait
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case result := <-outcome:
		if result.Err != nil {
			requestmeta.Logger(ctx).Warn("source refresh failed; keeping local results", "error", result.Err)
			return nil, false
		}
		if result.Val == nil {
			return nil, false
		}
		return result.Val.([]VodItem), true
	case <-timer.C:
		requestmeta.Logger(ctx).Info("source refresh exceeded wait budget; serving local results",
			"keyword", keyword, "budget_ms", budget.Milliseconds())
		return nil, false
	}
}

func (service *Service) fetchAndSave(ctx context.Context, keyword string) ([]VodItem, error) {
	fetchCtx, fetchCancel := context.WithTimeout(ctx, service.config.TotalTimeout)
	defer fetchCancel()
	items, err := service.fetchFromSources(fetchCtx, keyword)
	if err != nil {
		return nil, err
	}
	// persist/enrich 用独立超时，不受 fetch 阶段的时间消耗影响
	persistCtx, persistCancel := context.WithTimeout(ctx, service.config.TotalTimeout)
	defer persistCancel()
	for _, item := range items {
		if err := service.persistItem(persistCtx, item); err != nil {
			requestmeta.Logger(ctx).Warn("persist source item failed", "source", item.SourceKey, "vod_id", item.VodId, "error", err)
		}
	}
	service.enrichMediaIdentity(persistCtx, items)
	return items, nil
}

// persistItem 带重试地写入一条资源；成功后顺带做剧集索引。
func (service *Service) persistItem(ctx context.Context, item VodItem) error {
	var lastErr error
	for attempt := 0; attempt <= service.config.PersistRetries; attempt++ {
		if err := service.items.Upsert(ctx, item); err == nil {
			if service.episodes != nil && item.VodPlayUrl != "" {
				if indexErr := service.episodes.IndexResourceEpisodes(ctx, item); indexErr != nil {
					requestmeta.Logger(ctx).Debug("shadow resource episode indexing failed", "source", item.SourceKey, "vod_id", item.VodId, "error", indexErr)
				}
			}
			return nil
		} else {
			lastErr = err
		}
		if attempt == service.config.PersistRetries {
			break
		}
		backoff := time.Duration(attempt+1) * 25 * time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("upsert after %d retries: %w", service.config.PersistRetries, lastErr)
}

// probe 记录一次资源站抓取的结果和耗时，用于事后统一写健康统计。
type probe struct {
	key     string
	outcome Outcome
	elapsed time.Duration
}

// fetchFromSources 用固定数量的 worker 并发抓取所有启用资源站。
// 每个站单独超时，整体再受 TotalTimeout 约束；慢站不会拖垮整轮搜索。
func (service *Service) fetchFromSources(ctx context.Context, keyword string) ([]VodItem, error) {
	sites, err := service.sites.ListEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if len(sites) == 0 {
		return []VodItem{}, nil
	}
	if service.health != nil {
		sites, _ = service.health.FilterAvailable(sites)
	}
	categories, _ := service.filters.CategoryKeywords(ctx)

	workerCount := min(service.config.SourceMaxConcurrency, len(sites))
	jobs := make(chan Site)
	var waitGroup sync.WaitGroup
	var mutex sync.Mutex
	allItems := make([]VodItem, 0)
	probes := make([]probe, 0, len(sites))
	for index := 0; index < workerCount; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for site := range jobs {
				requestContext, cancel := context.WithTimeout(ctx, service.config.SourceTimeout)
				startedAt := time.Now()
				items, crawlErr := service.crawler.Search(requestContext, site.BaseURL, keyword, site.Key, categories)
				elapsed := time.Since(startedAt)
				outcome := classifyOutcome(requestContext, crawlErr, len(items))
				cancel()
				mutex.Lock()
				probes = append(probes, probe{key: site.Key, outcome: outcome, elapsed: elapsed})
				if crawlErr == nil {
					allItems = append(allItems, items...)
				}
				mutex.Unlock()
			}
		}()
	}
	for _, site := range sites {
		select {
		case jobs <- site:
		case <-ctx.Done():
			close(jobs)
			waitGroup.Wait()
			service.recordOutcomes(probes, len(allItems) > 0)
			return allItems, ctx.Err()
		}
	}
	close(jobs)
	waitGroup.Wait()
	service.recordOutcomes(probes, len(allItems) > 0)
	return allItems, nil
}

// recordOutcomes 写入健康统计。特例：整轮一条结果都没有时，"返回空"不算某个站的问题，不计入。
func (service *Service) recordOutcomes(probes []probe, anyHit bool) {
	if service.health == nil {
		return
	}
	for _, current := range probes {
		if current.outcome == OutcomeEmpty && !anyHit {
			continue
		}
		service.health.Record(current.key, current.outcome, current.elapsed.Milliseconds())
	}
}

// filterCopyright 按版权关键词过滤结果，返回过滤后的列表和被过滤条数。
func (service *Service) filterCopyright(ctx context.Context, items []VodItem) ([]VodItem, int) {
	keywords, err := service.filters.CopyrightKeywords(ctx)
	if err != nil || len(keywords) == 0 {
		return items, 0
	}
	filtered := make([]VodItem, 0, len(items))
	for _, item := range items {
		if !matchesCopyright(item.VodName, keywords) {
			filtered = append(filtered, item)
		}
	}
	return filtered, len(items) - len(filtered)
}

// matchesCopyright 判断片名是否包含任一屏蔽词（忽略大小写）。
func matchesCopyright(name string, keywords []string) bool {
	lowerName := strings.ToLower(name)
	for _, keyword := range keywords {
		if strings.TrimSpace(keyword) != "" && strings.Contains(lowerName, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
