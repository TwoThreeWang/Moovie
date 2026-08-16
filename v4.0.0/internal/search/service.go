package search

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/requestmeta"
	"golang.org/x/sync/singleflight"
)

// ServiceConfig 保存一次跨来源搜索及媒体匹配所允许使用的时间、并发和阈值预算。
type ServiceConfig struct {
	SourceTimeout             time.Duration
	TotalTimeout              time.Duration
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
	identityCache *Cache[mediaIdentityResult]
}

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

type MatchCandidateRecorder interface {
	RecordMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error
}

type DetailedMatchCandidateRecorder interface {
	RecordDetailedMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error
}

type MediaMatchRequest struct {
	Title         string
	OriginalTitle string
	Year          string
	MediaType     string
	Actors        string
	Directors     string
}

type MediaMatchResult struct {
	MediaID      int
	Confidence   float64
	MatchedBy    string
	Status       string
	ReasonJSON   string
	HardConflict string
}

type ScoredMediaIdentity interface {
	MatchResource(ctx context.Context, request MediaMatchRequest) (MediaMatchResult, error)
}

type ResourceEpisodeIndexer interface {
	IndexResourceEpisodes(ctx context.Context, item VodItem) error
}

type ServiceOption func(*Service)

func WithMediaIdentity(identity MediaIdentity) ServiceOption {
	return func(service *Service) { service.identity = identity }
}

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
		identityCache: NewCache[mediaIdentityResult](5000, 30*time.Minute)}
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
		value, refreshErr, _ := service.singleflight.Do(keyword, func() (any, error) {
			return service.fetchAndSave(ctx, keyword)
		})
		if refreshErr != nil {
			requestmeta.Logger(ctx).Warn("source refresh failed; keeping local results", "error", refreshErr)
		} else if value != nil {
			items = mergeSearchItems(items, value.([]VodItem))
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
			normalizedType := normalizeSearchMediaType(item.TypeName)
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

func normalizeSearchMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "movie" || value == "film" || strings.Contains(value, "电影"):
		return "movie"
	case value == "tv" || value == "series" || value == "season" || value == "show" || value == "animation" ||
		strings.Contains(value, "电视") || strings.Contains(value, "连续剧") || strings.Contains(value, "动漫") || strings.Contains(value, "综艺") ||
		strings.HasSuffix(value, "剧"):
		return "tv"
	default:
		return ""
	}
}

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

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

func (service *Service) fetchAndSave(ctx context.Context, keyword string) ([]VodItem, error) {
	totalContext, cancel := context.WithTimeout(ctx, service.config.TotalTimeout)
	defer cancel()
	items, err := service.fetchFromSources(totalContext, keyword)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if err := service.persistItem(totalContext, item); err != nil {
			requestmeta.Logger(ctx).Warn("persist source item failed", "source", item.SourceKey, "vod_id", item.VodId, "error", err)
		}
	}
	// 在返回本次刷新结果前立即回填精确媒体关联，
	// 避免新资源停留在“有豆瓣 ID 但未关联”的状态。
	service.enrichMediaIdentity(totalContext, items)
	return items, nil
}

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

type probe struct {
	key     string
	outcome Outcome
	elapsed time.Duration
}

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

func matchesCopyright(name string, keywords []string) bool {
	lowerName := strings.ToLower(name)
	for _, keyword := range keywords {
		if strings.TrimSpace(keyword) != "" && strings.Contains(lowerName, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
