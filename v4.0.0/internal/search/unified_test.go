package search

import (
	"context"
	"errors"
	"testing"
)

func TestUnifiedSearchGroupsOnlyTrustedMediaLinks(t *testing.T) {
	resources := &recordingUnifiedResources{result: Result{FilteredCount: 3, Items: []VodItem{
		{SourceKey: "slow", VodId: "1", VodName: "流浪地球", VodEn: "The Wandering Earth", VodYear: "2019", TypeName: "电影", MediaID: 7, SampleCount: 10, FailedCount: 1, AvgSpeedMs: 80},
		{SourceKey: "reliable", VodId: "2", VodName: "流浪地球", VodYear: "2019", TypeName: "movie", MediaID: 7, SampleCount: 20, FailedCount: 1, AvgSpeedMs: 500},
		{SourceKey: "unknown-a", VodId: "3", VodName: "流浪地球 未确认", VodYear: "2019", TypeName: "电影"},
		{SourceKey: "unknown-b", VodId: "4", VodName: "流浪地球 未确认", VodYear: "2019", TypeName: "电影"},
		{SourceKey: "tv", VodId: "5", VodName: "流浪地球 剧集", VodYear: "2019", TypeName: "电视剧", MediaID: 8},
	}}}
	service := NewUnifiedSearchService(resources)

	result, err := service.SearchUnified(t.Context(), UnifiedQuery{
		Keyword: "  流浪地球  ", Year: "2019", MediaType: "film", BypassFilter: true, Limit: 20,
	})
	if err != nil {
		t.Fatalf("SearchUnified() error = %v", err)
	}
	if resources.keyword != "流浪地球" || !resources.bypass {
		t.Fatalf("resource query = %q bypass=%v", resources.keyword, resources.bypass)
	}
	if len(result.Items) != 1 || result.Items[0].MediaID != 7 || result.Items[0].ResourceCount != 2 {
		t.Fatalf("grouped items = %+v", result.Items)
	}
	if result.Items[0].BestResource == nil || result.Items[0].BestResource.SourceKey != "reliable" {
		t.Fatalf("best resource = %+v", result.Items[0].BestResource)
	}
	if len(result.Unmatched) != 2 {
		t.Fatalf("unmatched = %+v", result.Unmatched)
	}
	if result.Unmatched[0].SourceKey == result.Unmatched[1].SourceKey {
		t.Fatal("distinct unmatched resources were merged")
	}
	if result.FilteredCount != 3 {
		t.Fatalf("filtered count = %d", result.FilteredCount)
	}
}

func TestUnifiedSearchHonorsLimitWithoutMergingUnmatched(t *testing.T) {
	resources := &recordingUnifiedResources{result: Result{Items: []VodItem{
		{SourceKey: "one", VodId: "1", VodName: "一", MediaID: 1},
		{SourceKey: "two", VodId: "2", VodName: "二", MediaID: 2},
		{SourceKey: "unknown-one", VodId: "3", VodName: "同名"},
		{SourceKey: "unknown-two", VodId: "4", VodName: "同名"},
	}}}
	result, err := NewUnifiedSearchService(resources).SearchUnified(t.Context(), UnifiedQuery{Keyword: "片", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || len(result.Unmatched) != 1 || result.Unmatched[0].SourceKey != "unknown-one" {
		t.Fatalf("limited result = %+v", result)
	}
}

func TestUnifiedSearchExcludesCurrentPlaybackResource(t *testing.T) {
	resources := &recordingUnifiedResources{result: Result{Items: []VodItem{
		{SourceKey: "current", VodId: "1", VodName: "影片", MediaID: 7, SampleCount: 10, AvgSpeedMs: 20},
		{SourceKey: "alternative", VodId: "2", VodName: "影片", MediaID: 7, SampleCount: 10, AvgSpeedMs: 40},
		{SourceKey: "current", VodId: "1", VodName: "未确认的重复资源"},
	}}}
	result, err := NewUnifiedSearchService(resources).SearchUnified(t.Context(), UnifiedQuery{
		Keyword: "影片", ExcludeSourceKey: "current", ExcludeVodID: "1", Limit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ResourceCount != 1 || result.Items[0].BestResource == nil || result.Items[0].BestResource.SourceKey != "alternative" {
		t.Fatalf("resources after exclusion = %+v", result.Items)
	}
	if len(result.Unmatched) != 0 {
		t.Fatalf("unmatched resources after exclusion = %+v", result.Unmatched)
	}
}

func TestUnifiedSearchPrefersCanonicalMetadataAndSupplementsResources(t *testing.T) {
	resources := &recordingUnifiedResources{result: Result{Items: []VodItem{
		{SourceKey: "fresh", VodId: "2", VodName: "资源标题", MediaID: 7, SampleCount: 5, AvgSpeedMs: 90},
		{SourceKey: "unknown", VodId: "3", VodName: "未确认资源"},
	}}}
	catalog := &recordingUnifiedCatalog{
		items: []UnifiedItem{{MediaID: 7, Title: "规范标题", OriginalTitle: "Canonical Title", Year: "2026", MediaType: "movie", Poster: "canonical-poster"}},
		resources: []VodItem{
			{SourceKey: "stored", VodId: "1", VodName: "旧资源标题", MediaID: 7, SampleCount: 5, AvgSpeedMs: 300},
			{SourceKey: "fresh", VodId: "2", VodName: "重复缓存资源", MediaID: 7, SampleCount: 5, AvgSpeedMs: 90},
		},
	}
	service := NewUnifiedSearchService(resources, WithUnifiedCatalog(catalog))
	result, err := service.SearchUnified(t.Context(), UnifiedQuery{Keyword: "别名", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].Title != "规范标题" || result.Items[0].Poster != "canonical-poster" {
		t.Fatalf("canonical item = %+v", result.Items)
	}
	if result.Items[0].ResourceCount != 2 || result.Items[0].BestResource == nil || result.Items[0].BestResource.SourceKey != "fresh" {
		t.Fatalf("supplemented resources = %+v", result.Items[0].Resources)
	}
	if len(catalog.mediaIDs) != 1 || catalog.mediaIDs[0] != 7 {
		t.Fatalf("catalog resource IDs = %v", catalog.mediaIDs)
	}
	if len(result.Unmatched) != 1 || result.Unmatched[0].SourceKey != "unknown" || result.CatalogFallback {
		t.Fatalf("fallback result = %+v", result)
	}
}

func TestUnifiedSearchStillReturnsCatalogResultsWhenResourceSearchFails(t *testing.T) {
	resources := &recordingUnifiedResources{err: errors.New("source timeout")}
	catalog := &recordingUnifiedCatalog{items: []UnifiedItem{{MediaID: 7, Title: "本地规范标题"}}}
	result, err := NewUnifiedSearchService(resources, WithUnifiedCatalog(catalog)).SearchUnified(t.Context(), UnifiedQuery{Keyword: "标题", Limit: 20})
	if err != nil {
		t.Fatalf("SearchUnified() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].MediaID != 7 || !result.ResourceUnavailable || result.CatalogFallback {
		t.Fatalf("result = %+v", result)
	}
}

func TestUnifiedSearchUsesCanonicalAliasForMissingResourceCard(t *testing.T) {
	resources := &keywordUnifiedResources{results: map[string][]VodItem{
		"羊毛战记": {{SourceKey: "source", VodId: "1", VodName: "羊毛战记 第一季", VodDoubanId: "35468745"}},
	}}
	catalog := &recordingUnifiedCatalog{items: []UnifiedItem{{
		MediaID: 7, DoubanID: "35468745", Title: "末日地堡 第一季", SearchAliases: []string{"羊毛战记", "羊毛记"},
	}}}
	result, err := NewUnifiedSearchService(resources, WithUnifiedCatalog(catalog)).SearchUnified(t.Context(), UnifiedQuery{Keyword: "末日地堡", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ResourceCount != 0 || len(result.Unmatched) != 1 || result.Unmatched[0].VodId != "1" {
		t.Fatalf("alias resource result = %+v", result)
	}
	if len(resources.keywords) != 2 || resources.keywords[0] != "末日地堡" || resources.keywords[1] != "羊毛战记" {
		t.Fatalf("resource keywords = %v", resources.keywords)
	}
}

type recordingUnifiedResources struct {
	result  Result
	keyword string
	bypass  bool
	err     error
}

type keywordUnifiedResources struct {
	results  map[string][]VodItem
	keywords []string
}

func (resources *keywordUnifiedResources) Search(_ context.Context, keyword string, _ bool) (*Result, error) {
	resources.keywords = append(resources.keywords, keyword)
	return &Result{Items: append([]VodItem(nil), resources.results[keyword]...)}, nil
}

func (resources *recordingUnifiedResources) Search(_ context.Context, keyword string, bypass bool) (*Result, error) {
	resources.keyword, resources.bypass = keyword, bypass
	result := resources.result
	result.Items = append([]VodItem(nil), resources.result.Items...)
	return &result, resources.err
}

type recordingUnifiedCatalog struct {
	items     []UnifiedItem
	resources []VodItem
	mediaIDs  []int
	err       error
}

func (catalog *recordingUnifiedCatalog) SearchUnifiedMedia(context.Context, UnifiedQuery) ([]UnifiedItem, error) {
	return append([]UnifiedItem(nil), catalog.items...), catalog.err
}

func (catalog *recordingUnifiedCatalog) ListUnifiedResources(_ context.Context, mediaIDs []int) ([]VodItem, error) {
	catalog.mediaIDs = append([]int(nil), mediaIDs...)
	return append([]VodItem(nil), catalog.resources...), catalog.err
}
