package search

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type UnifiedQuery struct {
	Keyword          string
	Year             string
	MediaType        string
	ExcludeSourceKey string
	ExcludeVodID     string
	BypassFilter     bool
	Limit            int
}

type UnifiedItem struct {
	MediaID       int               `json:"media_id"`
	Title         string            `json:"title"`
	OriginalTitle string            `json:"original_title,omitempty"`
	Year          string            `json:"year,omitempty"`
	MediaType     string            `json:"media_type,omitempty"`
	Poster        string            `json:"poster,omitempty"`
	DoubanID      string            `json:"douban_id,omitempty"`
	RatingDouban  float64           `json:"rating_douban,omitempty"`
	Summary       string            `json:"summary,omitempty"`
	ResourceCount int               `json:"resource_count"`
	Resources     []UnifiedResource `json:"resources"`
	BestResource  *UnifiedResource  `json:"best_resource,omitempty"`
	SearchAliases []string          `json:"-"`
}

// UnifiedResource 刻意保持精简。搜索响应只需要稳定资源键和质量摘要；
// 播放地址与完整来源数据仍只允许通过详情或播放端点获取。
type UnifiedResource struct {
	MediaID        int     `json:"media_id,omitempty"`
	SourceKey      string  `json:"source_key"`
	VodId          string  `json:"vod_id"`
	VodName        string  `json:"name"`
	VodRemarks     string  `json:"remarks,omitempty"`
	VodYear        string  `json:"year,omitempty"`
	TypeName       string  `json:"media_type,omitempty"`
	VodPic         string  `json:"poster,omitempty"`
	AvgSpeedMs     int     `json:"avg_speed_ms"`
	SampleCount    int     `json:"sample_count"`
	FailedCount    int     `json:"failed_count"`
	SuccessRate    float64 `json:"success_rate"`
	ResourceStatus string  `json:"resource_status,omitempty"`
}

type UnifiedResult struct {
	Items               []UnifiedItem     `json:"items"`
	Unmatched           []UnifiedResource `json:"unmatched"`
	FilteredCount       int               `json:"filtered_count"`
	DurationMS          int64             `json:"duration_ms"`
	ResourceDurationMS  int64             `json:"resource_duration_ms"`
	ResourceUnavailable bool              `json:"resource_unavailable"`
	CatalogDurationMS   int64             `json:"catalog_duration_ms"`
	CatalogFallback     bool              `json:"catalog_fallback"`
}

type UnifiedSearcher interface {
	SearchUnified(ctx context.Context, query UnifiedQuery) (UnifiedResult, error)
}

type UnifiedCatalog interface {
	SearchUnifiedMedia(ctx context.Context, query UnifiedQuery) ([]UnifiedItem, error)
	ListUnifiedResources(ctx context.Context, mediaIDs []int) ([]VodItem, error)
}

type UnifiedSearchOption func(*UnifiedSearchService)

func WithUnifiedCatalog(catalog UnifiedCatalog) UnifiedSearchOption {
	return func(service *UnifiedSearchService) { service.catalog = catalog }
}

type UnifiedSearchService struct {
	resources Searcher
	catalog   UnifiedCatalog
}

func NewUnifiedSearchService(resources Searcher, options ...UnifiedSearchOption) *UnifiedSearchService {
	service := &UnifiedSearchService{resources: resources}
	for _, option := range options {
		option(service)
	}
	return service
}

func (service *UnifiedSearchService) SearchUnified(ctx context.Context, query UnifiedQuery) (UnifiedResult, error) {
	query.Keyword, query.Year, query.MediaType = strings.TrimSpace(query.Keyword), strings.TrimSpace(query.Year), normalizeUnifiedMediaType(query.MediaType)
	if query.Limit <= 0 || query.Limit > 100 {
		query.Limit = 20
	}
	started := time.Now()
	var (
		resourceResult                 *Result
		resourceErr                    error
		resourceDuration               int64
		catalogItems                   []UnifiedItem
		catalogResources               []VodItem
		catalogErr, catalogResourceErr error
		catalogDuration                int64
	)
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		resourceStarted := time.Now()
		resourceResult, resourceErr = service.resources.Search(ctx, query.Keyword, query.BypassFilter)
		resourceDuration = time.Since(resourceStarted).Milliseconds()
	}()
	if service.catalog != nil {
		wait.Add(1)
		go func() {
			defer wait.Done()
			catalogStarted := time.Now()
			catalogItems, catalogErr = service.catalog.SearchUnifiedMedia(ctx, query)
			if catalogErr == nil {
				mediaIDs := make([]int, 0, len(catalogItems))
				for _, item := range catalogItems {
					mediaIDs = append(mediaIDs, item.MediaID)
				}
				catalogResources, catalogResourceErr = service.catalog.ListUnifiedResources(ctx, mediaIDs)
			}
			catalogDuration = time.Since(catalogStarted).Milliseconds()
		}()
	}
	wait.Wait()
	resourceUnavailable := resourceErr != nil || resourceResult == nil
	if resourceResult == nil {
		resourceResult = &Result{Items: []VodItem{}}
	}
	if resourceUnavailable && service.catalog == nil {
		if resourceErr == nil {
			resourceErr = errors.New("resource search returned no result")
		}
		return UnifiedResult{Items: []UnifiedItem{}, Unmatched: []UnifiedResource{}, DurationMS: time.Since(started).Milliseconds(),
			ResourceDurationMS: resourceDuration, ResourceUnavailable: true}, resourceErr
	}
	service.appendAliasResourceMatches(ctx, query, catalogItems, catalogResources, resourceResult)

	groups := make(map[int]*UnifiedItem)
	order := make([]int, 0)
	catalogFallback := catalogErr != nil || catalogResourceErr != nil
	catalogSucceeded := service.catalog != nil && catalogErr == nil
	if service.catalog != nil {
		if catalogErr == nil {
			for index := range catalogItems {
				item := catalogItems[index]
				resources := make([]UnifiedResource, 0, len(item.Resources))
				for _, resource := range item.Resources {
					if !query.excludes(resource.SourceKey, resource.VodId) {
						resources = append(resources, resource)
					}
				}
				item.Resources = resources
				groups[item.MediaID] = &item
				order = append(order, item.MediaID)
			}
			if catalogResourceErr == nil {
				for _, resource := range catalogResources {
					if query.excludes(resource.SourceKey, resource.VodId) {
						continue
					}
					if group := groups[resource.MediaID]; group != nil {
						appendUniqueUnifiedResource(group, resource)
					}
				}
			}
		}
	}
	if resourceUnavailable && !catalogSucceeded {
		if resourceErr == nil {
			resourceErr = errors.New("unified search dependencies unavailable")
		}
		return UnifiedResult{Items: []UnifiedItem{}, Unmatched: []UnifiedResource{}, DurationMS: time.Since(started).Milliseconds(),
			ResourceDurationMS: resourceDuration, ResourceUnavailable: true, CatalogDurationMS: catalogDuration, CatalogFallback: catalogFallback}, resourceErr
	}

	unmatched := make([]UnifiedResource, 0)
	for _, resource := range resourceResult.Items {
		if query.excludes(resource.SourceKey, resource.VodId) {
			continue
		}
		if query.Year != "" && strings.TrimSpace(resource.VodYear) != query.Year {
			continue
		}
		if query.MediaType != "" && normalizeUnifiedMediaType(resource.TypeName) != query.MediaType {
			continue
		}
		if resource.MediaID <= 0 {
			if len(unmatched) < query.Limit {
				unmatched = append(unmatched, newUnifiedResource(resource))
			}
			continue
		}
		group := groups[resource.MediaID]
		if group == nil {
			group = unifiedItemFromResource(resource)
			groups[resource.MediaID] = group
			order = append(order, resource.MediaID)
		}
		appendUniqueUnifiedResource(group, resource)
	}

	items := make([]UnifiedItem, 0, min(len(order), query.Limit))
	for _, mediaID := range order {
		if len(items) == query.Limit {
			break
		}
		group := groups[mediaID]
		sort.SliceStable(group.Resources, func(left, right int) bool {
			return resourceIsBetter(group.Resources[left], group.Resources[right])
		})
		group.ResourceCount = len(group.Resources)
		if group.ResourceCount > 0 {
			best := group.Resources[0]
			group.BestResource = &best
		}
		items = append(items, *group)
	}
	return UnifiedResult{Items: items, Unmatched: unmatched, FilteredCount: resourceResult.FilteredCount,
		DurationMS: time.Since(started).Milliseconds(), ResourceDurationMS: resourceDuration, ResourceUnavailable: resourceUnavailable,
		CatalogDurationMS: catalogDuration, CatalogFallback: catalogFallback}, nil
}

const maxAliasResourceSearches = 3

// appendAliasResourceMatches 只为没有资源的规范媒体补搜豆瓣别名。
// 补搜结果必须带相同豆瓣 ID，避免展示忽略关键词的上游返回的无关列表。
func (service *UnifiedSearchService) appendAliasResourceMatches(ctx context.Context, query UnifiedQuery, catalogItems []UnifiedItem, catalogResources []VodItem, resourceResult *Result) {
	if service.catalog == nil || resourceResult == nil {
		return
	}
	availableDoubanIDs := make(map[string]bool)
	availableMediaIDs := make(map[int]bool)
	seenResources := make(map[string]bool)
	for _, resource := range catalogResources {
		if resource.MediaID > 0 {
			availableMediaIDs[resource.MediaID] = true
		}
		if resource.VodDoubanId != "" {
			availableDoubanIDs[resource.VodDoubanId] = true
		}
		seenResources[resource.SourceKey+"\x00"+resource.VodId] = true
	}
	for _, resource := range resourceResult.Items {
		if resource.VodDoubanId != "" {
			availableDoubanIDs[resource.VodDoubanId] = true
		}
		seenResources[resource.SourceKey+"\x00"+resource.VodId] = true
	}

	searches := 0
	for _, item := range catalogItems {
		if searches >= maxAliasResourceSearches || item.DoubanID == "" || availableMediaIDs[item.MediaID] || availableDoubanIDs[item.DoubanID] {
			continue
		}
		for _, alias := range item.SearchAliases {
			alias = strings.TrimSpace(alias)
			if alias == "" || strings.EqualFold(alias, query.Keyword) {
				continue
			}
			searches++
			aliasResult, err := service.resources.Search(ctx, alias, query.BypassFilter)
			if err != nil || aliasResult == nil {
				continue
			}
			found := false
			for _, resource := range aliasResult.Items {
				if resource.VodDoubanId != item.DoubanID {
					continue
				}
				key := resource.SourceKey + "\x00" + resource.VodId
				if seenResources[key] {
					continue
				}
				seenResources[key] = true
				resourceResult.Items = append(resourceResult.Items, resource)
				found = true
			}
			if found || searches >= maxAliasResourceSearches {
				break
			}
		}
	}
}

// excludes 只排除当前播放线路，避免“更多资源”再次指向正在播放的同一条资源。
func (query UnifiedQuery) excludes(sourceKey, vodID string) bool {
	return query.ExcludeSourceKey != "" && query.ExcludeVodID != "" &&
		query.ExcludeSourceKey == sourceKey && query.ExcludeVodID == vodID
}

func unifiedItemFromResource(resource VodItem) *UnifiedItem {
	return &UnifiedItem{MediaID: resource.MediaID, Title: resource.VodName, OriginalTitle: firstNonEmpty(resource.VodEn, resource.VodSub),
		Year: resource.VodYear, MediaType: normalizeUnifiedMediaType(resource.TypeName), Poster: resource.VodPic,
		DoubanID: resource.VodDoubanId, Resources: make([]UnifiedResource, 0)}
}

func appendUniqueUnifiedResource(group *UnifiedItem, resource VodItem) {
	for _, existing := range group.Resources {
		if existing.SourceKey == resource.SourceKey && existing.VodId == resource.VodId {
			return
		}
	}
	group.Resources = append(group.Resources, newUnifiedResource(resource))
}

func newUnifiedResource(item VodItem) UnifiedResource {
	return UnifiedResource{MediaID: item.MediaID, SourceKey: item.SourceKey, VodId: item.VodId, VodName: item.VodName,
		VodRemarks: item.VodRemarks, VodYear: item.VodYear, TypeName: normalizeUnifiedMediaType(item.TypeName), VodPic: item.VodPic,
		AvgSpeedMs: item.AvgSpeedMs, SampleCount: item.SampleCount, FailedCount: item.FailedCount,
		SuccessRate: resourceSuccessRate(item.SampleCount, item.FailedCount), ResourceStatus: item.ResourceStatus}
}

func resourceIsBetter(left, right UnifiedResource) bool {
	leftSuccess, rightSuccess := left.SuccessRate, right.SuccessRate
	if leftSuccess != rightSuccess {
		return leftSuccess > rightSuccess
	}
	if left.AvgSpeedMs == 0 {
		return false
	}
	if right.AvgSpeedMs == 0 {
		return true
	}
	return left.AvgSpeedMs < right.AvgSpeedMs
}

func resourceSuccessRate(sampleCount, failedCount int) float64 {
	if sampleCount <= 0 {
		return 0
	}
	successes := sampleCount - failedCount
	if successes < 0 {
		successes = 0
	}
	return float64(successes) / float64(sampleCount)
}

func normalizeUnifiedMediaType(value string) string {
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
