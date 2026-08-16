package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryStore 允许应用在不接 PostgreSQL 的隔离环境中运行，
// 同时为所有搜索持久化接口提供结果可预测的测试实现。
type MemoryStore struct {
	mu                   sync.RWMutex
	items                []VodItem
	sites                []Site
	copyrightKeywords    []string
	categoryKeywords     []string
	trending             map[string]TrendingKeyword
	searchLogs           []memorySearchLog
	nextSiteID           uint
	nextFilterID         uint
	copyrightFilters     []Filter
	categoryFilters      []Filter
	healthStats          []HealthStat
	matchCandidates      []MatchCandidate
	matchAuditCount      int
	nextMatchCandidateID int64
	coolingBatches       map[int64]memoryCoolingBatch
	nextCoolingBatchID   int64
}

func (store *MemoryStore) RecordMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error {
	return store.RecordDetailedMatchCandidate(ctx, sourceKey, vodID, mediaID, confidence, matchedBy, MatchStatusReview, "")
}

func (store *MemoryStore) RecordDetailedMatchCandidate(_ context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	status = normalizeMatchDecision(status, MatchStatusReview)
	for index := range store.matchCandidates {
		candidate := &store.matchCandidates[index]
		if candidate.SourceKey == sourceKey && candidate.VodID == vodID && candidate.MediaID == mediaID {
			candidate.Confidence, candidate.MatchMethod, candidate.ReasonJSON = confidence, matchedBy, reasonJSON
			if candidate.Status != MatchStatusVerified && candidate.Status != MatchStatusRejected {
				candidate.Status = status
			}
			return nil
		}
	}
	store.matchCandidates = append(store.matchCandidates, MatchCandidate{ID: store.nextMatchCandidateID, SourceKey: sourceKey, VodID: vodID,
		MediaID: mediaID, Confidence: confidence, MatchMethod: matchedBy, Status: status, ReasonJSON: reasonJSON})
	store.nextMatchCandidateID++
	return nil
}

func (store *MemoryStore) ListMatchCandidates(_ context.Context, status string, limit int) ([]MatchCandidate, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	status = normalizeMatchDecision(status, MatchStatusReview)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	result := make([]MatchCandidate, 0)
	for _, stored := range store.matchCandidates {
		if stored.Status != status {
			continue
		}
		candidate := stored
		for _, item := range store.items {
			if item.SourceKey == candidate.SourceKey && item.VodId == candidate.VodID {
				candidate.ResourceTitle, candidate.ResourceYear, candidate.ResourcePoster = item.VodName, item.VodYear, item.VodPic
				candidate.ResourceActors, candidate.ResourceDirector = item.VodActor, item.VodDirector
				break
			}
		}
		result = append(result, candidate)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (store *MemoryStore) ReviewMatchCandidate(_ context.Context, sourceKey, vodID string, mediaID, actorUserID int, decision, reason string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.matchCandidates {
		candidate := &store.matchCandidates[index]
		if candidate.SourceKey == sourceKey && candidate.VodID == vodID && candidate.MediaID == mediaID {
			return store.reviewMatchCandidateLocked(index, 0, actorUserID, decision, reason)
		}
	}
	return fmt.Errorf("resource match candidate not found")
}

func (store *MemoryStore) ReviewMatchCandidateByID(ctx context.Context, candidateID int64, actorUserID int, decision, reason string) error {
	return store.ResolveMatchCandidateByID(ctx, candidateID, 0, actorUserID, decision, reason)
}

func (store *MemoryStore) ResolveMatchCandidateByID(_ context.Context, candidateID int64, resolvedMediaID, actorUserID int, decision, reason string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.matchCandidates {
		if store.matchCandidates[index].ID == candidateID {
			return store.reviewMatchCandidateLocked(index, resolvedMediaID, actorUserID, decision, reason)
		}
	}
	return fmt.Errorf("resource match candidate not found")
}

func (store *MemoryStore) reviewMatchCandidateLocked(index, resolvedMediaID, actorUserID int, decision, reason string) error {
	decision = normalizeMatchDecision(decision, "")
	if actorUserID <= 0 || strings.TrimSpace(reason) == "" || (decision != MatchStatusVerified && decision != MatchStatusRejected) {
		return fmt.Errorf("invalid resource match review")
	}
	candidate := &store.matchCandidates[index]
	if candidate.Status != MatchStatusReview {
		return fmt.Errorf("resource match candidate is already %s", candidate.Status)
	}
	candidate.Status = decision
	if resolvedMediaID <= 0 {
		resolvedMediaID = candidate.MediaID
	}
	if decision == MatchStatusRejected {
		resolvedMediaID = 0
	}
	candidate.ResolvedMediaID = resolvedMediaID
	if decision == MatchStatusVerified {
		for itemIndex := range store.items {
			item := &store.items[itemIndex]
			if item.SourceKey == candidate.SourceKey && item.VodId == candidate.VodID {
				if item.MediaID > 0 && item.MediaID != resolvedMediaID && (item.MediaMatch == "manual" || item.MediaMatch == "verified") {
					candidate.Status = MatchStatusReview
					candidate.ResolvedMediaID = 0
					return fmt.Errorf("resource is already locked to another media")
				}
				item.MediaID, item.MediaConfidence, item.MediaMatch = resolvedMediaID, 1, "manual"
				break
			}
		}
	}
	store.matchAuditCount++
	return nil
}

type memorySearchLog struct {
	keyword  string
	searched time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{trending: make(map[string]TrendingKeyword), nextSiteID: 1, nextFilterID: 1, nextMatchCandidateID: 1,
		coolingBatches: make(map[int64]memoryCoolingBatch), nextCoolingBatchID: 1}
}

func (store *MemoryStore) Search(_ context.Context, keyword string) ([]VodItem, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]VodItem, 0)
	for _, item := range store.items {
		if strings.Contains(item.VodName, keyword) || strings.Contains(item.VodSub, keyword) || strings.Contains(item.VodEn, keyword) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastVisitedAt.After(items[j].LastVisitedAt) })
	return items, nil
}

func (store *MemoryStore) Upsert(_ context.Context, item VodItem) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if item.LastVisitedAt.IsZero() {
		item.LastVisitedAt = time.Now()
	}
	item.ResourceStatus = "active"
	for index := range store.items {
		if store.items[index].SourceKey == item.SourceKey && store.items[index].VodId == item.VodId {
			store.items[index] = item
			return nil
		}
	}
	store.items = append(store.items, item)
	return nil
}

func (store *MemoryStore) FindBySourceID(_ context.Context, sourceKey, vodID string) (*VodItem, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, item := range store.items {
		if item.SourceKey == sourceKey && item.VodId == vodID {
			copy := item
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) SearchByDoubanID(_ context.Context, doubanID string) ([]VodItem, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := make([]VodItem, 0)
	for _, item := range store.items {
		if item.VodDoubanId == doubanID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (store *MemoryStore) LoadStats(_ context.Context, sourceKey, vodID string) (*LoadStats, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	stats := &LoadStats{}
	for _, item := range store.items {
		if item.SourceKey == sourceKey && item.VodId == vodID {
			stats.AvgSpeedMs = item.AvgSpeedMs
			stats.SampleCount = item.SampleCount
			stats.FailedCount = item.FailedCount
			break
		}
	}
	if stats.SampleCount > 0 {
		stats.SuccessRate = float64(stats.SampleCount-stats.FailedCount) / float64(stats.SampleCount) * 100
	}
	return stats, nil
}

func (store *MemoryStore) RecordLoadSuccess(_ context.Context, sourceKey, vodID string, loadTime int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.items {
		item := &store.items[index]
		if item.SourceKey == sourceKey && item.VodId == vodID {
			item.AvgSpeedMs = (item.AvgSpeedMs*item.SampleCount + loadTime) / (item.SampleCount + 1)
			item.SampleCount++
			break
		}
	}
	return nil
}

func (store *MemoryStore) RecordLoadFailure(_ context.Context, sourceKey, vodID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.items {
		item := &store.items[index]
		if item.SourceKey == sourceKey && item.VodId == vodID {
			item.FailedCount++
			item.SampleCount++
			break
		}
	}
	return nil
}

func (store *MemoryStore) ListEnabled(_ context.Context) ([]Site, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	sites := make([]Site, 0, len(store.sites))
	for _, site := range store.sites {
		if site.Enabled {
			sites = append(sites, site)
		}
	}
	return sites, nil
}

func (store *MemoryStore) FindSiteByKey(_ context.Context, key string) (*Site, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, site := range store.sites {
		if site.Key == key {
			copy := site
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) CopyrightKeywords(_ context.Context) ([]string, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]string(nil), store.copyrightKeywords...), nil
}

func (store *MemoryStore) CategoryKeywords(_ context.Context) ([]string, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]string(nil), store.categoryKeywords...), nil
}

func (store *MemoryStore) ReplaceSites(sites []Site) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range sites {
		if sites[index].ID == 0 {
			sites[index].ID = store.nextSiteID
			store.nextSiteID++
		}
	}
	store.sites = append([]Site(nil), sites...)
}

func (store *MemoryStore) ReplaceCopyrightKeywords(keywords []string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.copyrightKeywords = append([]string(nil), keywords...)
	store.copyrightFilters = make([]Filter, 0, len(keywords))
	for _, keyword := range keywords {
		store.copyrightFilters = append(store.copyrightFilters, Filter{ID: store.nextFilterID, Keyword: keyword, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		store.nextFilterID++
	}
}

func (store *MemoryStore) ReplaceCategoryKeywords(keywords []string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.categoryKeywords = append([]string(nil), keywords...)
	store.categoryFilters = make([]Filter, 0, len(keywords))
	for _, keyword := range keywords {
		store.categoryFilters = append(store.categoryFilters, Filter{ID: store.nextFilterID, Keyword: keyword, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		store.nextFilterID++
	}
}

func (store *MemoryStore) ListSites(_ context.Context) ([]Site, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]Site(nil), store.sites...), nil
}

func (store *MemoryStore) GetSite(_ context.Context, id uint) (*Site, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, site := range store.sites {
		if site.ID == id {
			copy := site
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) CreateSite(_ context.Context, site Site) (*Site, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, existing := range store.sites {
		if existing.Key == site.Key {
			return nil, fmt.Errorf("site key already exists")
		}
	}
	site.ID = store.nextSiteID
	store.nextSiteID++
	now := time.Now().Unix()
	site.CreatedAt, site.UpdatedAt = now, now
	store.sites = append(store.sites, site)
	copy := site
	return &copy, nil
}

func (store *MemoryStore) UpdateSite(_ context.Context, site Site) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.sites {
		if store.sites[index].ID == site.ID {
			if site.Key == "" {
				site.Key = store.sites[index].Key
			}
			if site.BaseURL == "" {
				site.BaseURL = store.sites[index].BaseURL
			}
			site.CreatedAt = store.sites[index].CreatedAt
			site.UpdatedAt = time.Now().Unix()
			store.sites[index] = site
			return nil
		}
	}
	return nil
}

func (store *MemoryStore) DeleteSite(_ context.Context, id uint) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.sites {
		if store.sites[index].ID == id {
			store.sites = append(store.sites[:index], store.sites[index+1:]...)
			break
		}
	}
	return nil
}

func (store *MemoryStore) DeleteInactive(_ context.Context, days int) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -days)
	affected := 0
	for index := range store.items {
		item := &store.items[index]
		if item.LastVisitedAt.Before(cutoff) && item.ResourceStatus != "stale" {
			item.ResourceStatus = "stale"
			affected++
		}
	}
	return affected, nil
}

func (store *MemoryStore) DeleteOldKeywords(_ context.Context, days int) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -days)
	affected := 0
	for keyword, item := range store.trending {
		if item.LastSearchedAt.Before(cutoff) {
			delete(store.trending, keyword)
			affected++
		}
	}
	return affected, nil
}

func (store *MemoryStore) DeleteOldSearchLogs(_ context.Context, days int) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	cutoff := time.Now().AddDate(0, 0, -days)
	kept := store.searchLogs[:0]
	affected := 0
	for _, entry := range store.searchLogs {
		if entry.searched.Before(cutoff) {
			affected++
			continue
		}
		kept = append(kept, entry)
	}
	store.searchLogs = kept
	return affected, nil
}

func (store *MemoryStore) DeleteHealthBefore(_ context.Context, before time.Time) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	kept := store.healthStats[:0]
	affected := 0
	for _, stat := range store.healthStats {
		if stat.Bucket.Before(before) {
			affected++
			continue
		}
		kept = append(kept, stat)
	}
	store.healthStats = kept
	return affected, nil
}

func (store *MemoryStore) ListCopyrightFilters(_ context.Context) ([]Filter, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]Filter(nil), store.copyrightFilters...), nil
}

func (store *MemoryStore) CreateCopyrightFilter(_ context.Context, keyword string) (*Filter, error) {
	return store.createFilter(keyword, true)
}

func (store *MemoryStore) UpdateCopyrightFilter(_ context.Context, id uint, keyword string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.copyrightFilters {
		if store.copyrightFilters[index].ID == id {
			store.copyrightFilters[index].Keyword = keyword
			store.copyrightFilters[index].UpdatedAt = time.Now()
			store.syncKeywords()
			break
		}
	}
	return nil
}

func (store *MemoryStore) DeleteCopyrightFilter(_ context.Context, id uint) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.copyrightFilters {
		if store.copyrightFilters[index].ID == id {
			store.copyrightFilters = append(store.copyrightFilters[:index], store.copyrightFilters[index+1:]...)
			break
		}
	}
	store.syncKeywords()
	return nil
}

func (store *MemoryStore) ListCategoryFilters(_ context.Context) ([]Filter, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]Filter(nil), store.categoryFilters...), nil
}

func (store *MemoryStore) CreateCategoryFilter(_ context.Context, keyword string) (*Filter, error) {
	return store.createFilter(keyword, false)
}

func (store *MemoryStore) DeleteCategoryFilter(_ context.Context, id uint) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.categoryFilters {
		if store.categoryFilters[index].ID == id {
			store.categoryFilters = append(store.categoryFilters[:index], store.categoryFilters[index+1:]...)
			break
		}
	}
	store.syncKeywords()
	return nil
}

func (store *MemoryStore) createFilter(keyword string, copyright bool) (*Filter, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	filters := &store.categoryFilters
	if copyright {
		filters = &store.copyrightFilters
	}
	for _, existing := range *filters {
		if existing.Keyword == keyword {
			return nil, fmt.Errorf("keyword already exists")
		}
	}
	now := time.Now()
	filter := Filter{ID: store.nextFilterID, Keyword: keyword, CreatedAt: now, UpdatedAt: now}
	store.nextFilterID++
	*filters = append(*filters, filter)
	store.syncKeywords()
	return &filter, nil
}

func (store *MemoryStore) syncKeywords() {
	store.copyrightKeywords = store.copyrightKeywords[:0]
	for _, filter := range store.copyrightFilters {
		store.copyrightKeywords = append(store.copyrightKeywords, filter.Keyword)
	}
	store.categoryKeywords = store.categoryKeywords[:0]
	for _, filter := range store.categoryFilters {
		store.categoryKeywords = append(store.categoryKeywords, filter.Keyword)
	}
}

func (store *MemoryStore) Log(_ context.Context, keyword string, _ *int, _ string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	item := store.trending[keyword]
	item.Keyword = keyword
	item.Count++
	item.LastSearchedAt = time.Now()
	store.trending[keyword] = item
	store.searchLogs = append(store.searchLogs, memorySearchLog{keyword: keyword, searched: item.LastSearchedAt})
	return nil
}

func (store *MemoryStore) Trending(_ context.Context, hours, limit int) ([]TrendingKeyword, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	cutoff := time.Time{}
	if hours > 0 {
		cutoff = time.Now().Add(-time.Duration(hours) * time.Hour)
	}
	items := make([]TrendingKeyword, 0, len(store.trending))
	if cutoff.IsZero() {
		for _, item := range store.trending {
			items = append(items, item)
		}
	} else {
		aggregated := make(map[string]TrendingKeyword)
		for _, entry := range store.searchLogs {
			if !entry.searched.After(cutoff) {
				continue
			}
			item := aggregated[entry.keyword]
			item.Keyword = entry.keyword
			item.Count++
			if entry.searched.After(item.LastSearchedAt) {
				item.LastSearchedAt = entry.searched
			}
			aggregated[entry.keyword] = item
		}
		for _, item := range aggregated {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Count > items[j].Count })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (store *MemoryStore) AddHealthStats(_ context.Context, stats []HealthStat) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, stat := range stats {
		merged := false
		for index := range store.healthStats {
			current := &store.healthStats[index]
			if current.SiteKey == stat.SiteKey && current.Bucket.Equal(stat.Bucket) {
				current.OKCount += stat.OKCount
				current.EmptyCount += stat.EmptyCount
				current.TimeoutCount += stat.TimeoutCount
				current.ErrorCount += stat.ErrorCount
				current.TotalMs += stat.TotalMs
				merged = true
				break
			}
		}
		if !merged {
			store.healthStats = append(store.healthStats, stat)
		}
	}
	return nil
}

func (store *MemoryStore) SummaryHealthSince(_ context.Context, since time.Time) (map[string]*HealthSummary, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	summaries := make(map[string]*HealthSummary)
	for _, stat := range store.healthStats {
		if stat.Bucket.Before(since) {
			continue
		}
		summary := summaries[stat.SiteKey]
		if summary == nil {
			summary = &HealthSummary{SiteKey: stat.SiteKey}
			summaries[stat.SiteKey] = summary
		}
		summary.OKCount += stat.OKCount
		summary.EmptyCount += stat.EmptyCount
		summary.TimeoutCount += stat.TimeoutCount
		summary.ErrorCount += stat.ErrorCount
		summary.TotalMs += stat.TotalMs
	}
	return summaries, nil
}
