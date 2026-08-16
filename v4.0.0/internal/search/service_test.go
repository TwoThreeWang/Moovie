package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceUsesLocalResultsFiltersCopyrightAndSortsBySpeed(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceCopyrightKeywords([]string{"禁片"})
	for _, item := range []VodItem{
		{SourceKey: "a", VodId: "1", VodName: "普通电影", AvgSpeedMs: 3000},
		{SourceKey: "b", VodId: "2", VodName: "禁片电影示例", AvgSpeedMs: 100},
		{SourceKey: "c", VodId: "3", VodName: "普通电影 第二源", AvgSpeedMs: 900},
		{SourceKey: "d", VodId: "4", VodName: "普通电影 未测速"},
	} {
		if err := store.Upsert(context.Background(), item); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{})
	result, err := service.Search(context.Background(), "电影", false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if result.FilteredCount != 1 || len(result.Items) != 3 {
		t.Fatalf("result = %+v", result)
	}
	if result.Items[0].AvgSpeedMs != 900 || result.Items[1].AvgSpeedMs != 3000 || result.Items[2].AvgSpeedMs != 0 {
		t.Fatalf("speed ordering changed: %+v", result.Items)
	}

	bypassResult, err := service.Search(context.Background(), "电影", true)
	if err != nil {
		t.Fatal(err)
	}
	if bypassResult.FilteredCount != 0 || len(bypassResult.Items) != 4 {
		t.Fatalf("bypass result = %+v", bypassResult)
	}
}

func TestServiceEnrichesResourceWithCanonicalMediaAndPersistsLink(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "肖申克", VodYear: "1994", VodDoubanId: "1292052"}); err != nil {
		t.Fatal(err)
	}
	identity := &fakeMediaIdentity{mediaID: 17}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{ResourceMatchShadow: true, ResourceMatchAutoApply: true}, WithMediaIdentity(identity))
	result, err := service.Search(context.Background(), "肖申克", false)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	item := result.Items[0]
	if item.MediaID != 17 || item.MediaMatch != "douban_id" || item.MediaConfidence != 1 || identity.linkedID != 17 {
		t.Fatalf("canonical enrichment = %+v, link=%+v", item, identity)
	}
}

func TestServiceKeepsExactMatchInShadowUntilAutoApplyIsEnabled(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "肖申克", VodYear: "1994", VodDoubanId: "1292052"}); err != nil {
		t.Fatal(err)
	}
	identity := &fakeMediaIdentity{mediaID: 17}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{ResourceMatchShadow: true}, WithMediaIdentity(identity))
	result, err := service.Search(context.Background(), "肖申克", false)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if result.Items[0].MediaID != 0 || identity.linkedID != 0 {
		t.Fatalf("shadow-only exact match affected the public result: item=%+v identity=%+v", result.Items[0], identity)
	}
	if identity.candidateID != 17 || identity.candidateConfidence != 1 || identity.candidateMatch != "douban_id" {
		t.Fatalf("exact shadow candidate = %+v", identity)
	}
}

func TestServiceCanDisableResourceMatchShadowWrites(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "同名影片", VodYear: "2026"})
	identity := &fakeMediaIdentity{mediaID: 17}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{}, WithMediaIdentity(identity))
	_, err := service.Search(context.Background(), "同名影片", false)
	if err != nil {
		t.Fatal(err)
	}
	if identity.candidateID != 0 || identity.linkedID != 0 {
		t.Fatalf("disabled match rollout still wrote state: %+v", identity)
	}
}

func TestServicePersistsScoredEvidenceWithoutExposingShadowMatch(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "候选影片", VodYear: "2026", TypeName: "电影"})
	identity := &fakeScoredMediaIdentity{match: MediaMatchResult{MediaID: 17, Confidence: 0.82,
		MatchedBy: "weighted_features", Status: MatchStatusReview, ReasonJSON: `{"features":{"title":{"score":0.4}}}`}}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{ResourceMatchShadow: true}, WithMediaIdentity(identity))
	result, err := service.Search(context.Background(), "候选影片", false)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if result.Items[0].MediaID != 0 || identity.linkedID != 0 || identity.detailedStatus != MatchStatusReview || identity.detailedReason != identity.match.ReasonJSON {
		t.Fatalf("scored shadow match = item:%+v identity:%+v", result.Items[0], identity)
	}
}

func TestServicePersistsHardConflictBelowReviewThresholdAsRejected(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "冲突影片", VodYear: "2026"})
	identity := &fakeScoredMediaIdentity{match: MediaMatchResult{MediaID: 18, Confidence: 0.55,
		MatchedBy: "weighted_features", Status: MatchStatusRejected, HardConflict: "year_mismatch", ReasonJSON: `{"hard_conflicts":["year_mismatch"]}`}}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{ResourceMatchShadow: true}, WithMediaIdentity(identity))
	_, err := service.Search(context.Background(), "冲突影片", false)
	if err != nil {
		t.Fatal(err)
	}
	if identity.detailedStatus != MatchStatusRejected || identity.detailedReason != identity.match.ReasonJSON || identity.linkedID != 0 {
		t.Fatalf("hard conflict = %+v", identity)
	}
}

func TestServiceKeepsTitleYearMatchInReviewWithoutClaimingCanonicalLink(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "同名影片", VodYear: "2026"}); err != nil {
		t.Fatal(err)
	}
	identity := &fakeMediaIdentity{mediaID: 17}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{ResourceMatchShadow: true}, WithMediaIdentity(identity))
	result, err := service.Search(context.Background(), "同名影片", false)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	item := result.Items[0]
	if item.MediaID != 0 || item.MediaMatch != "" || identity.linkedID != 0 {
		t.Fatalf("review candidate was exposed as a canonical link: item=%+v identity=%+v", item, identity)
	}
	if identity.candidateID != 17 || identity.candidateConfidence != 0.7 || identity.candidateMatch != "title_year" {
		t.Fatalf("review candidate = %+v", identity)
	}
}

func TestServiceDoesNotTrustLowConfidenceResourceLink(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Upsert(context.Background(), VodItem{SourceKey: "source", VodId: "42", VodName: "同名影片", VodYear: "2026"}); err != nil {
		t.Fatal(err)
	}
	identity := &fakeMediaIdentity{mediaID: 17, existingLinkID: 17, existingLinkConfidence: 0.7, existingLinkMatch: "title_year"}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{ResourceMatchShadow: true}, WithMediaIdentity(identity))
	result, err := service.Search(context.Background(), "同名影片", false)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if result.Items[0].MediaID != 0 || identity.linkedID != 0 || identity.candidateID != 17 {
		t.Fatalf("low-confidence link was trusted: item=%+v identity=%+v", result.Items[0], identity)
	}
}

func TestServiceFetchesEnabledSitesConcurrentlyAndToleratesPartialFailure(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceSites([]Site{{Key: "ok", BaseURL: "https://ok", Enabled: true}, {Key: "bad", BaseURL: "https://bad", Enabled: true}, {Key: "off", Enabled: false}})
	store.ReplaceCategoryKeywords([]string{"写真"})
	health := &recordingHealth{}
	crawler := crawlerFunc(func(_ context.Context, _ string, _ string, sourceKey string, categories []string) ([]VodItem, error) {
		if len(categories) != 1 || categories[0] != "写真" {
			t.Fatalf("categories = %v", categories)
		}
		if sourceKey == "bad" {
			return nil, errors.New("upstream failed")
		}
		return []VodItem{{SourceKey: sourceKey, VodId: "1", VodName: "测试", VodPlayUrl: "a$m3u8"}}, nil
	})
	service := NewService(store, store, store, crawler, health, nil, ServiceConfig{SourceTimeout: time.Second, TotalTimeout: time.Second})
	result, err := service.Search(context.Background(), "测试", false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].SourceKey != "ok" {
		t.Fatalf("result = %+v", result)
	}
	if health.count(OutcomeOK) != 1 || health.count(OutcomeError) != 1 {
		t.Fatalf("health outcomes = %v", health.outcomes)
	}
	local, _ := store.Search(context.Background(), "测试")
	if len(local) != 1 {
		t.Fatalf("remote result was not persisted: %+v", local)
	}
}

func TestFetchAndSaveImmediatelyLinksFreshExactMediaIdentity(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceSites([]Site{{Key: "source", BaseURL: "https://source.example", Enabled: true}})
	identity := &fakeMediaIdentity{mediaID: 17}
	service := NewService(store, store, store, crawlerFunc(func(context.Context, string, string, string, []string) ([]VodItem, error) {
		return []VodItem{{SourceKey: "source", VodId: "42", VodName: "新资源", VodDoubanId: "1292052"}}, nil
	}), nil, nil, ServiceConfig{ResourceMatchAutoApply: true}, WithMediaIdentity(identity))

	items, err := service.fetchAndSave(t.Context(), "已有本地结果触发的后台刷新")
	if err != nil || len(items) != 1 {
		t.Fatalf("fetchAndSave() = %+v, %v", items, err)
	}
	if identity.linkedID != 17 || items[0].MediaID != 17 || items[0].MediaMatch != "douban_id" {
		t.Fatalf("fresh exact identity was not linked: item=%+v identity=%+v", items[0], identity)
	}
}

func TestServiceReturnsFreshResourcesAlongsideWarmLocalResults(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceSites([]Site{{Key: "source", BaseURL: "https://source.example", Enabled: true}})
	if err := store.Upsert(t.Context(), VodItem{SourceKey: "source", VodId: "2", VodName: "末日地堡 第二季"}); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, store, store, crawlerFunc(func(context.Context, string, string, string, []string) ([]VodItem, error) {
		return []VodItem{{SourceKey: "source", VodId: "1", VodName: "末日地堡 第一季"}}, nil
	}), nil, immediateRunner{}, ServiceConfig{})

	result, err := service.Search(t.Context(), "末日地堡", false)
	if err != nil || len(result.Items) != 2 {
		t.Fatalf("warm search result/error = %+v/%v", result, err)
	}
	if result.Items[0].VodId != "2" && result.Items[1].VodId != "2" {
		t.Fatalf("local resource disappeared after refresh: %+v", result.Items)
	}
	if result.Items[0].VodId != "1" && result.Items[1].VodId != "1" {
		t.Fatalf("fresh resource was not returned immediately: %+v", result.Items)
	}
}

func TestServiceBoundsPerSearchSourceFanout(t *testing.T) {
	store := NewMemoryStore()
	sites := make([]Site, 10)
	for index := range sites {
		sites[index] = Site{Key: fmt.Sprintf("source-%d", index), BaseURL: "https://source.example", Enabled: true}
	}
	store.ReplaceSites(sites)
	var active atomic.Int32
	var maximum atomic.Int32
	crawler := crawlerFunc(func(context.Context, string, string, string, []string) ([]VodItem, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		return []VodItem{}, nil
	})
	service := NewService(store, store, store, crawler, nil, nil, ServiceConfig{
		SourceTimeout: time.Second, TotalTimeout: time.Second, SourceMaxConcurrency: 3,
	})
	if _, err := service.Search(context.Background(), "扇出", false); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() > 3 || maximum.Load() < 2 {
		t.Fatalf("maximum source concurrency = %d, want 2..3", maximum.Load())
	}
}

func TestServiceIndexesResourceEpisodesImmediatelyAfterUpsert(t *testing.T) {
	store := NewMemoryStore()
	indexer := &recordingEpisodeIndexer{}
	service := NewService(store, store, store, crawlerFunc(nil), nil, nil, ServiceConfig{}, WithResourceEpisodeIndexer(indexer))
	item := VodItem{SourceKey: "source", VodId: "42", VodName: "剧集", VodPlayUrl: "第01集$https://a.example/1.m3u8"}
	if err := service.persistItem(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.FindBySourceID(t.Context(), "source", "42")
	if stored == nil || len(indexer.items) != 1 || indexer.items[0].VodPlayUrl != item.VodPlayUrl {
		t.Fatalf("stored/indexed = %+v/%+v", stored, indexer.items)
	}
}

func TestServiceRetriesTransientResourcePersistence(t *testing.T) {
	base := NewMemoryStore()
	base.ReplaceSites([]Site{{Key: "source", Enabled: true}})
	itemStore := &flakyItemStore{store: base, failures: 2}
	service := NewService(itemStore, base, base, crawlerFunc(func(context.Context, string, string, string, []string) ([]VodItem, error) {
		return []VodItem{{SourceKey: "source", VodId: "1", VodName: "重试资源", VodPlayUrl: "url"}}, nil
	}), nil, nil, ServiceConfig{PersistRetries: 2})
	result, err := service.Search(context.Background(), "重试资源", false)
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if itemStore.calls != 3 {
		t.Fatalf("persist calls = %d, want 3", itemStore.calls)
	}
	stored, _ := base.FindBySourceID(context.Background(), "source", "1")
	if stored == nil || stored.ResourceStatus != "active" {
		t.Fatalf("stored item = %+v", stored)
	}
}

func TestServiceDoesNotRecordEmptyWhenEverySiteIsEmpty(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceSites([]Site{{Key: "a", Enabled: true}, {Key: "b", Enabled: true}})
	health := &recordingHealth{}
	service := NewService(store, store, store, crawlerFunc(func(context.Context, string, string, string, []string) ([]VodItem, error) {
		return []VodItem{}, nil
	}), health, nil, ServiceConfig{})
	result, err := service.Search(context.Background(), "冷门词", false)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if len(health.outcomes) != 0 {
		t.Fatalf("all-empty search polluted health stats: %v", health.outcomes)
	}
}

func TestServiceClassifiesPerSiteDeadlineAsTimeout(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceSites([]Site{{Key: "slow", Enabled: true}})
	health := &recordingHealth{}
	service := NewService(store, store, store, crawlerFunc(func(ctx context.Context, _ string, _ string, _ string, _ []string) ([]VodItem, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), health, nil, ServiceConfig{SourceTimeout: 5 * time.Millisecond, TotalTimeout: time.Second})
	result, err := service.Search(context.Background(), "超时", false)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("result/error = %+v/%v", result, err)
	}
	if health.count(OutcomeTimeout) != 1 {
		t.Fatalf("health outcomes = %v", health.outcomes)
	}
}

func TestServiceSingleflightCoalescesColdSearch(t *testing.T) {
	store := NewMemoryStore()
	store.ReplaceSites([]Site{{Key: "source", Enabled: true}})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	crawler := crawlerFunc(func(context.Context, string, string, string, []string) ([]VodItem, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []VodItem{{SourceKey: "source", VodId: "1", VodName: "并发", VodPlayUrl: "url"}}, nil
	})
	service := NewService(store, store, store, crawler, nil, nil, ServiceConfig{SourceTimeout: time.Second, TotalTimeout: time.Second})

	var waitGroup sync.WaitGroup
	results := make([]*Result, 2)
	for index := range results {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			results[index], _ = service.Search(context.Background(), "并发", false)
		}(index)
	}
	<-started
	time.Sleep(10 * time.Millisecond)
	close(release)
	waitGroup.Wait()
	if calls.Load() != 1 {
		t.Fatalf("crawler calls = %d, want 1", calls.Load())
	}
	for _, result := range results {
		if result == nil || len(result.Items) != 1 {
			t.Fatalf("joined result = %+v", result)
		}
	}
}

type crawlerFunc func(context.Context, string, string, string, []string) ([]VodItem, error)

type flakyItemStore struct {
	store    *MemoryStore
	failures int
	calls    int
}

func (store *flakyItemStore) Search(ctx context.Context, keyword string) ([]VodItem, error) {
	return store.store.Search(ctx, keyword)
}

func (store *flakyItemStore) Upsert(ctx context.Context, item VodItem) error {
	store.calls++
	if store.failures > 0 {
		store.failures--
		return errors.New("temporary persistence failure")
	}
	return store.store.Upsert(ctx, item)
}

func (function crawlerFunc) Search(ctx context.Context, baseURL, keyword, sourceKey string, categories []string) ([]VodItem, error) {
	if function == nil {
		return nil, nil
	}
	return function(ctx, baseURL, keyword, sourceKey, categories)
}

type recordingHealth struct {
	mu       sync.Mutex
	outcomes []Outcome
}

type recordingEpisodeIndexer struct{ items []VodItem }

func (indexer *recordingEpisodeIndexer) IndexResourceEpisodes(_ context.Context, item VodItem) error {
	indexer.items = append(indexer.items, item)
	return nil
}

type fakeMediaIdentity struct {
	mediaID                int
	linkedID               int
	candidateID            int
	candidateConfidence    float64
	candidateMatch         string
	existingLinkID         int
	existingLinkConfidence float64
	existingLinkMatch      string
}

type fakeScoredMediaIdentity struct {
	fakeMediaIdentity
	match          MediaMatchResult
	detailedStatus string
	detailedReason string
}

func (identity *fakeScoredMediaIdentity) MatchResource(context.Context, MediaMatchRequest) (MediaMatchResult, error) {
	return identity.match, nil
}

func (identity *fakeScoredMediaIdentity) RecordDetailedMatchCandidate(_ context.Context, _, _ string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error {
	identity.candidateID, identity.candidateConfidence, identity.candidateMatch = mediaID, confidence, matchedBy
	identity.detailedStatus, identity.detailedReason = status, reasonJSON
	return nil
}

func (identity *fakeMediaIdentity) RecordMatchCandidate(_ context.Context, _, _ string, mediaID int, confidence float64, matchedBy string) error {
	identity.candidateID = mediaID
	identity.candidateConfidence = confidence
	identity.candidateMatch = matchedBy
	return nil
}

func (identity *fakeMediaIdentity) FindResourceLink(context.Context, string, string) (int, float64, string, error) {
	if identity.existingLinkID > 0 {
		return identity.existingLinkID, identity.existingLinkConfidence, identity.existingLinkMatch, nil
	}
	return 0, 0, "", errors.New("not linked")
}

func (identity *fakeMediaIdentity) FindByDoubanID(context.Context, string) (int, error) {
	return identity.mediaID, nil
}

func (identity *fakeMediaIdentity) FindByExternalID(context.Context, string, string) (int, error) {
	return 0, nil
}

func (identity *fakeMediaIdentity) FindByTitleYearType(context.Context, string, string, string) (int, error) {
	return identity.mediaID, nil
}

func (identity *fakeMediaIdentity) FindByTitleYear(context.Context, string, string) (int, error) {
	return identity.mediaID, nil
}

func (identity *fakeMediaIdentity) LinkResource(_ context.Context, _ string, _ string, mediaID int, _ float64, _ string) error {
	identity.linkedID = mediaID
	return nil
}

func (health *recordingHealth) FilterAvailable(sites []Site) ([]Site, []string) { return sites, nil }

func (health *recordingHealth) Record(_ string, outcome Outcome, _ int64) {
	health.mu.Lock()
	defer health.mu.Unlock()
	health.outcomes = append(health.outcomes, outcome)
}

func (health *recordingHealth) count(want Outcome) int {
	health.mu.Lock()
	defer health.mu.Unlock()
	count := 0
	for _, outcome := range health.outcomes {
		if outcome == want {
			count++
		}
	}
	return count
}
