package search

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresStoreSearchUsesPlaybackQualityAndPreservesMapping(t *testing.T) {
	visitedAt := time.Date(2026, time.July, 29, 1, 2, 3, 0, time.UTC)
	database := &fakeSQLDatabase{rows: &fakeSQLRows{values: [][]any{{
		"source", "42", "肖申克", "副标题", "Shawshank", "tag", "剧情",
		"poster", "actor", "director", "blurb", "完结", "1994-01-01",
		"1", "1", "美国", "英语", "1994", "142分钟", "today", "1292052",
		"content", "a$m3u8", "电影", visitedAt, int64(800), int64(3), int64(1), "active", int64(0), float64(0), "",
	}}}}
	store := NewPostgresStore(database)
	items, err := store.Search(context.Background(), "肖申克")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(items) != 1 || items[0].VodId != "42" || items[0].AvgSpeedMs != 800 || !items[0].LastVisitedAt.Equal(visitedAt) {
		t.Fatalf("items = %+v", items)
	}
	if !strings.Contains(database.query, "resource.vod_name LIKE $1 OR resource.vod_sub LIKE $1 OR resource.vod_en LIKE $1") ||
		!strings.Contains(database.query, "ORDER BY resource.last_visited_at DESC") || !strings.Contains(database.query, "playback_quality_rollups") {
		t.Fatalf("resource search query changed: %s", database.query)
	}
	if len(database.arguments) != 1 || database.arguments[0] != "%肖申克%" {
		t.Fatalf("query arguments = %v", database.arguments)
	}
}

func TestPostgresStoreSearchesCanonicalMediaAndAliases(t *testing.T) {
	database := &fakeSQLDatabase{rows: &fakeSQLRows{values: [][]any{{int64(7), "流浪地球", "The Wandering Earth", []string{"流浪地球别名"}, "2019", "movie", "poster", "26266893", 9.7, "一部关于..."}}}}
	store := NewPostgresStore(database)
	items, err := store.SearchUnifiedMedia(t.Context(), UnifiedQuery{Keyword: "流浪", Year: "2019", MediaType: "film", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MediaID != 7 || items[0].Title != "流浪地球" || len(items[0].SearchAliases) != 1 || items[0].Resources == nil {
		t.Fatalf("items = %+v", items)
	}
	for _, expected := range []string{"FROM media", "media_aliases", "$2 <> '' AND alias.normalized_alias LIKE $2", "media.media_type = $4", "LIMIT $6"} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("canonical query missing %q: %s", expected, database.query)
		}
	}
	if !reflect.DeepEqual(database.arguments, []any{"%流浪%", "%流浪%", "2019", "movie", "流浪", 20}) {
		t.Fatalf("arguments = %#v", database.arguments)
	}
}

func TestPostgresStoreListsOnlyLinkedNonRemovedResources(t *testing.T) {
	visitedAt := time.Date(2026, time.August, 3, 1, 2, 3, 0, time.UTC)
	database := &fakeSQLDatabase{rows: &fakeSQLRows{values: [][]any{{
		int64(7), "source", "42", "流浪地球", "副标题", "Wandering Earth", "tag", "科幻",
		"poster", "actor", "director", "blurb", "完结", "2019-01-01", "1", "1",
		"中国", "国语", "2019", "125分钟", "today", "26266893", "content", "正片$url",
		"电影", visitedAt, int64(120), int64(10), int64(1), "active", int64(7), float64(0), "",
	}}}}
	store := NewPostgresStore(database)
	items, err := store.ListUnifiedResources(t.Context(), []int{7})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].MediaID != 7 || items[0].VodId != "42" || items[0].AvgSpeedMs != 120 {
		t.Fatalf("resources = %+v", items)
	}
	for _, expected := range []string{"resource_media_links", "link.media_id = ANY($1::bigint[])", "resource.resource_status", "<> 'removed'"} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("resource query missing %q: %s", expected, database.query)
		}
	}
}

func TestPostgresStoreRequiresIndexedActivePlaybackForDirectPlay(t *testing.T) {
	database := &fakeSQLDatabase{row: fakeSQLRow{value: true}}
	store := NewPostgresStore(database)
	playable, err := store.HasPlayableResource(t.Context(), 7)
	if err != nil || !playable {
		t.Fatalf("playable/error = %t/%v", playable, err)
	}
	for _, expected := range []string{"resource_episode_candidates", "resource_play_lines", "vod_items", "candidate.media_id = $1", "vod_play_url", "resource_status NOT IN"} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("playable query missing %q: %s", expected, database.query)
		}
	}
	if !reflect.DeepEqual(database.arguments, []any{7}) {
		t.Fatalf("playable arguments = %#v", database.arguments)
	}
}

func TestPostgresStoreUpsertOnlyRefreshesSourceMetadata(t *testing.T) {
	database := &fakeSQLDatabase{}
	store := NewPostgresStore(database)
	item := VodItem{SourceKey: "source", VodId: "42", VodName: "name"}
	if err := store.Upsert(context.Background(), item); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if len(database.arguments) != 28 {
		t.Fatalf("upsert arguments = %d, want 28", len(database.arguments))
	}
	for _, expected := range []string{"vod_name = EXCLUDED.vod_name", "vod_sub = EXCLUDED.vod_sub", "vod_remarks = EXCLUDED.vod_remarks", "vod_time = EXCLUDED.vod_time", "vod_play_url = EXCLUDED.vod_play_url", "last_visited_at = EXCLUDED.last_visited_at", "metadata_hash = EXCLUDED.metadata_hash", "metadata_version = CASE"} {
		if !strings.Contains(database.execQuery, expected) {
			t.Fatalf("upsert missing %q: %s", expected, database.execQuery)
		}
	}
	if database.arguments[26] != StableResourceHash(item) || database.arguments[27] == nil {
		t.Fatalf("upsert lifecycle arguments = %#v", database.arguments[26:])
	}
	if strings.Contains(database.execQuery, "vod_pic = EXCLUDED.vod_pic") || strings.Contains(database.execQuery, "vod_actor = EXCLUDED.vod_actor") {
		t.Fatalf("upsert overwrites fields maintained by the resource repository: %s", database.execQuery)
	}
}

func TestStableResourceHashIgnoresVolatileUsageFields(t *testing.T) {
	first := VodItem{SourceKey: "source", VodId: "42", VodName: "影片", VodPlayUrl: "第1集$u", LastVisitedAt: time.Now(), AvgSpeedMs: 1000, SampleCount: 3, FailedCount: 1}
	second := first
	second.LastVisitedAt = first.LastVisitedAt.Add(24 * time.Hour)
	second.AvgSpeedMs = 200
	second.SampleCount = 30
	second.FailedCount = 2
	if StableResourceHash(first) != StableResourceHash(second) {
		t.Fatal("usage fields changed stable metadata hash")
	}
	second.VodPlayUrl = "第1集$new-url"
	if StableResourceHash(first) == StableResourceHash(second) {
		t.Fatal("source playback content did not change stable metadata hash")
	}
}

func TestPostgresStoreLoadsEnabledSitesAndFilters(t *testing.T) {
	database := &fakeSQLDatabase{}
	store := NewPostgresStore(database)
	database.rows = &fakeSQLRows{values: [][]any{{"a", "https://a", true}, {"b", "https://b", true}}}
	sites, err := store.ListEnabled(context.Background())
	if err != nil || len(sites) != 2 || sites[0].Key != "a" {
		t.Fatalf("sites/error = %+v/%v", sites, err)
	}
	if !strings.Contains(database.query, "WHERE enabled = true ORDER BY id") {
		t.Fatalf("site query changed: %s", database.query)
	}
	database.rows = &fakeSQLRows{values: [][]any{{"版权词"}}}
	keywords, err := store.CopyrightKeywords(context.Background())
	if err != nil || len(keywords) != 1 || keywords[0] != "版权词" {
		t.Fatalf("keywords/error = %v/%v", keywords, err)
	}
}

func TestPostgresStoreSupportsPlaybackLookups(t *testing.T) {
	visitedAt := time.Date(2026, time.July, 29, 1, 2, 3, 0, time.UTC)
	vodRow := []any{
		"source", "42", "肖申克", "副标题", "Shawshank", "tag", "剧情",
		"poster", "actor", "director", "blurb", "完结", "1994-01-01",
		"1", "1", "美国", "英语", "1994", "142分钟", "today", "1292052",
		"content", "a$m3u8", "电影", visitedAt, int64(800), int64(3), int64(1), "active", int64(0), float64(0), "",
	}
	database := &fakeSQLDatabase{rows: &fakeSQLRows{values: [][]any{vodRow}}}
	store := NewPostgresStore(database)
	item, err := store.FindBySourceID(context.Background(), "source", "42")
	if err != nil || item == nil || item.VodDoubanId != "1292052" {
		t.Fatalf("item/error = %+v/%v", item, err)
	}
	if !strings.Contains(database.query, "resource.source_key = $1 AND resource.vod_id = $2") || !reflect.DeepEqual(database.arguments, []any{"source", "42"}) {
		t.Fatalf("detail query/args = %s / %v", database.query, database.arguments)
	}

	database.rows = &fakeSQLRows{values: [][]any{vodRow}}
	items, err := store.SearchByDoubanID(context.Background(), "1292052")
	if err != nil || len(items) != 1 {
		t.Fatalf("douban items/error = %+v/%v", items, err)
	}
	if !strings.Contains(database.query, "vod_douban_id = $1") {
		t.Fatalf("douban query = %s", database.query)
	}

	database.rows = &fakeSQLRows{values: [][]any{{"source", "https://source.example/api", true}}}
	site, err := store.FindSiteByKey(context.Background(), "source")
	if err != nil || site == nil || site.BaseURL != "https://source.example/api" {
		t.Fatalf("site/error = %+v/%v", site, err)
	}
}

func TestPostgresStoreReadsLoadSpeedFromQualityRollups(t *testing.T) {
	database := &fakeSQLDatabase{rows: &fakeSQLRows{values: [][]any{{int64(1200), int64(4), int64(1)}}}}
	store := NewPostgresStore(database)
	stats, err := store.LoadStats(context.Background(), "source", "42")
	if err != nil || stats.AvgSpeedMs != 1200 || stats.SampleCount != 4 || stats.FailedCount != 1 || stats.SuccessRate != 75 {
		t.Fatalf("stats/error = %+v/%v", stats, err)
	}
	for _, expected := range []string{"playback_quality_rollups", "resource_episode_candidates", "resource_play_lines", "INTERVAL '7 days'"} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("load stats query missing %q: %s", expected, database.query)
		}
	}
}

func TestPostgresStoreLogsAndAggregatesTrendingKeywords(t *testing.T) {
	database := &fakeSQLDatabase{}
	store := NewPostgresStore(database)
	if err := store.Log(context.Background(), "肖申克", nil, "hash"); err != nil {
		t.Fatalf("Log() error = %v", err)
	}
	if len(database.execQueries) != 2 || !strings.Contains(database.execQueries[0], "INSERT INTO search_logs") || !strings.Contains(database.execQueries[1], "ON CONFLICT (keyword)") {
		t.Fatalf("log queries = %v", database.execQueries)
	}
	now := time.Now()
	database.rows = &fakeSQLRows{values: [][]any{{"肖申克", int64(3), now}}}
	items, err := store.Trending(context.Background(), 24, 20)
	if err != nil || len(items) != 1 || items[0].Count != 3 {
		t.Fatalf("trending/error = %+v/%v", items, err)
	}
	if !strings.Contains(database.query, "FROM search_logs") || !reflect.DeepEqual(database.arguments, []any{24, 20}) {
		t.Fatalf("24h query/args = %s / %v", database.query, database.arguments)
	}
}

func TestPostgresStoreUpsertsHealthStatsInOneStatement(t *testing.T) {
	database := &fakeSQLDatabase{}
	store := NewPostgresStore(database)
	stats := []HealthStat{{SiteKey: "a", Bucket: time.Now(), OKCount: 1}, {SiteKey: "b", Bucket: time.Now(), ErrorCount: 2}}
	if err := store.AddHealthStats(context.Background(), stats); err != nil {
		t.Fatalf("AddHealthStats() error = %v", err)
	}
	if len(database.arguments) != 14 || !strings.Contains(database.execQuery, "ON CONFLICT (site_key, bucket) DO UPDATE") || !strings.Contains(database.execQuery, "site_stats.ok_count + EXCLUDED.ok_count") {
		t.Fatalf("health upsert query/args = %s / %d", database.execQuery, len(database.arguments))
	}
}

func TestPostgresStoreSummarizesRecentHealthStats(t *testing.T) {
	since := time.Now().Add(-24 * time.Hour)
	database := &fakeSQLDatabase{rows: &fakeSQLRows{values: [][]any{{"source", 3, 1, 1, 0, int64(750)}}}}
	store := NewPostgresStore(database)
	summaries, err := store.SummaryHealthSince(context.Background(), since)
	if err != nil {
		t.Fatalf("SummaryHealthSince() error = %v", err)
	}
	summary := summaries["source"]
	if summary == nil || summary.Total() != 5 || summary.OKRate() != 60 || summary.EmptyRate() != 20 || summary.FailRate() != 20 || summary.AvgMs() != 150 || summary.Level() != "warn" {
		t.Fatalf("summary = %+v", summary)
	}
	if !strings.Contains(database.query, "WHERE bucket >= $1") || !strings.Contains(database.query, "GROUP BY site_key") || !reflect.DeepEqual(database.arguments, []any{since}) {
		t.Fatalf("summary query/args = %s / %v", database.query, database.arguments)
	}
}

func TestPostgresStoreCleanupUsesBoundedRetentionPredicates(t *testing.T) {
	database := &fakeSQLDatabase{}
	store := NewPostgresStore(database)
	if _, err := store.DeleteOldKeywords(context.Background(), 30); err != nil || !strings.Contains(database.execQuery, "DELETE FROM trending_keywords WHERE last_searched_at <") || !reflect.DeepEqual(database.arguments, []any{30}) {
		t.Fatalf("keyword cleanup = %s / %v / %v", database.execQuery, database.arguments, err)
	}
	if _, err := store.DeleteOldSearchLogs(context.Background(), 30); err != nil || !strings.Contains(database.execQuery, "DELETE FROM search_logs WHERE created_at <") || !reflect.DeepEqual(database.arguments, []any{30}) {
		t.Fatalf("log cleanup = %s / %v / %v", database.execQuery, database.arguments, err)
	}
	before := time.Now().Add(-7 * 24 * time.Hour)
	if _, err := store.DeleteHealthBefore(context.Background(), before); err != nil || database.execQuery != "DELETE FROM site_stats WHERE bucket < $1" || !reflect.DeepEqual(database.arguments, []any{before}) {
		t.Fatalf("health cleanup = %s / %v / %v", database.execQuery, database.arguments, err)
	}
}

func TestPostgresStoreCoolingRequiresFrozenPreviewAndConfirmation(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	database := &fakeSQLDatabase{row: fakeSQLRow{values: []any{int64(17), 2, []byte(`{"demo":2}`), []byte(`{"stale":2}`), 3, 1, []byte(`[{"source_key":"demo","vod_id":"42"}]`), int64(4096), expiresAt}}}
	store := NewPostgresStore(database)
	preview, err := store.PreviewCooling(context.Background(), 90)
	if err != nil || preview.Eligible != 2 || preview.BatchID != 17 || preview.SourceDistribution["demo"] != 2 || preview.EstimatedBytes != 4096 {
		t.Fatalf("preview = %+v/%v", preview, err)
	}
	if !strings.Contains(database.query, "resource_lifecycle_batches") || !strings.Contains(database.query, "resource_lifecycle_batch_items") || !strings.Contains(database.query, "playback_positions") || !strings.Contains(database.query, "is_unique") || !strings.Contains(database.query, "last_success_at") {
		t.Fatalf("preview query lost safety predicates: %s", database.query)
	}
	if _, err := store.ApplyCooling(context.Background(), 17, false); err == nil {
		t.Fatal("ApplyCooling() without confirmation succeeded")
	}
	database.row = fakeSQLRow{value: 1}
	if affected, err := store.ApplyCooling(context.Background(), 17, true); err != nil || affected != 1 {
		t.Fatalf("cool = %d/%v", affected, err)
	}
	if !strings.Contains(database.query, "resource_status = 'cold'") || !strings.Contains(database.query, "status = 'previewed'") || !strings.Contains(database.query, "expires_at > NOW()") || !strings.Contains(database.query, "item.previous_status") || !strings.Contains(database.query, "applied_count") {
		t.Fatalf("cool query lost frozen-batch safety: %s", database.query)
	}
	if affected, err := store.RestoreCold(context.Background(), "source", "42"); err != nil || affected != 1 {
		t.Fatalf("restore = %d/%v", affected, err)
	}
	if !strings.Contains(database.execQuery, "resource_status = 'active'") || !strings.Contains(database.execQuery, "cold_at = NULL") || !strings.Contains(database.execQuery, "lifecycle_batch_id = NULL") {
		t.Fatalf("restore query = %s", database.execQuery)
	}
}

type fakeSQLDatabase struct {
	query       string
	execQuery   string
	execQueries []string
	arguments   []any
	rows        database.Rows
	row         database.Row
	err         error
}

func (fake *fakeSQLDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query = query
	fake.arguments = arguments
	return fake.rows, fake.err
}

func (fake *fakeSQLDatabase) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	fake.query = query
	fake.arguments = arguments
	return fake.row
}

func (fake *fakeSQLDatabase) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	fake.execQuery = query
	fake.execQueries = append(fake.execQueries, query)
	fake.arguments = arguments
	return 1, fake.err
}

type fakeSQLRows struct {
	values [][]any
	index  int
}

type fakeSQLRow struct {
	value  any
	values []any
}

func (row fakeSQLRow) Scan(destination ...any) error {
	values := row.values
	if values == nil {
		values = []any{row.value}
	}
	if len(destination) != len(values) {
		return fmt.Errorf("destinations/values = %d/%d", len(destination), len(values))
	}
	for index, raw := range values {
		target := reflect.ValueOf(destination[index]).Elem()
		value := reflect.ValueOf(raw)
		if !value.Type().ConvertibleTo(target.Type()) {
			return fmt.Errorf("cannot assign %T to %s", raw, target.Type())
		}
		target.Set(value.Convert(target.Type()))
	}
	return nil
}

func (rows *fakeSQLRows) Next() bool { return rows.index < len(rows.values) }

func (rows *fakeSQLRows) Scan(destinations ...any) error {
	if rows.index >= len(rows.values) {
		return fmt.Errorf("scan after end")
	}
	values := rows.values[rows.index]
	rows.index++
	if len(values) != len(destinations) {
		return fmt.Errorf("values/destinations = %d/%d", len(values), len(destinations))
	}
	for index, value := range values {
		destination := reflect.ValueOf(destinations[index]).Elem()
		source := reflect.ValueOf(value)
		if source.Type().ConvertibleTo(destination.Type()) {
			destination.Set(source.Convert(destination.Type()))
			continue
		}
		return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
	}
	return nil
}

func (rows *fakeSQLRows) Err() error { return nil }
func (rows *fakeSQLRows) Close()     {}
