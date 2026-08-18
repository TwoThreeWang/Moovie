package catalog

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresStorePreservesMovieIdentityAndExistingEmbeddingOnMetadataUpdate(t *testing.T) {
	fake := &catalogFakeDatabase{}
	store := NewPostgresStore(fake)
	if err := store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克", EmbeddingContent: "推荐语"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.execQuery, "ON CONFLICT (douban_id)") || strings.Contains(fake.execQuery, "embedding_content=EXCLUDED.embedding_content") {
		t.Fatalf("unsafe upsert query: %s", fake.execQuery)
	}
	if len(fake.arguments) != 19 || fake.arguments[0] != "1292052" || fake.arguments[1] != "肖申克" || fake.arguments[14] != "推荐语" || fake.arguments[18] != "" {
		t.Fatalf("upsert arguments = %#v", fake.arguments)
	}
	if updated, ok := fake.arguments[16].(time.Time); !ok || updated.IsZero() {
		t.Fatalf("reviews_updated_at was not normalized: %#v", fake.arguments[16])
	}
}

func TestPostgresStoreScopesIMDbIdentityForSeasonPages(t *testing.T) {
	fake := &catalogFakeDatabase{}
	store := NewPostgresStore(fake)
	if err := store.Upsert(t.Context(), Movie{DoubanID: "36444323", Title: "末日地堡 第二季", IMDbID: "tt14688458"}); err != nil {
		t.Fatal(err)
	}
	if fake.arguments[18] != "tv_season_2" || !strings.Contains(fake.execQuery, "WHEN $19 <> '' THEN $19") {
		t.Fatalf("season external identity = %#v/query=%s", fake.arguments[18], fake.execQuery)
	}
}

func TestPostgresStoreFindAndSitemapOrdering(t *testing.T) {
	updatedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	reviewsAt := updatedAt.Add(-time.Hour)
	nextRefreshAt := updatedAt.Add(24 * time.Hour)
	// 列顺序与 movieColumns 一致：… imdb_id, media_type, series_status, backdrops, embedding_content, semantic_hash, reviews_json,
	// reviews_updated_at, metadata_status, completeness_score, next_refresh_at, updated_at, embedding::text。
	values := []any{1, "1292052", "肖申克", "Original", "1994", "poster", 9.7, "剧情", "美国", "[]", "[]", "简介", "142分钟", "tt0111161", "movie", "Ended", "", "推荐语", "semantic-hash", "[]", reviewsAt, "ready", 92, &nextRefreshAt, updatedAt, ""}
	fake := &catalogFakeDatabase{rows: &catalogFakeRows{values: [][]any{values}}}
	store := NewPostgresStore(fake)
	movie, err := store.FindByDoubanID(t.Context(), "1292052")
	if err != nil || movie == nil || movie.Title != "肖申克" || movie.Rating != 9.7 {
		t.Fatalf("movie/error = %+v/%v", movie, err)
	}
	if movie.SeriesStatus != "Ended" {
		t.Fatalf("series status = %q", movie.SeriesStatus)
	}
	if movie.MetadataStatus != "ready" || movie.CompletenessScore != 92 || movie.NextRefreshAt == nil || !movie.NextRefreshAt.Equal(nextRefreshAt) {
		t.Fatalf("metadata refresh state = %q/%d/%v", movie.MetadataStatus, movie.CompletenessScore, movie.NextRefreshAt)
	}
	if !strings.Contains(fake.query, "WHERE m.douban_id = $1 LIMIT 1") || !reflect.DeepEqual(fake.arguments, []any{"1292052"}) {
		t.Fatalf("find query/args = %s/%#v", fake.query, fake.arguments)
	}

	fake.rows = &catalogFakeRows{values: [][]any{values}}
	movies, err := store.Latest(t.Context(), 1000)
	if err != nil || len(movies) != 1 {
		t.Fatalf("latest/error = %+v/%v", movies, err)
	}
	if !strings.Contains(fake.query, "ORDER BY m.updated_at DESC LIMIT $1") || !reflect.DeepEqual(fake.arguments, []any{1000}) {
		t.Fatalf("latest query/args = %s/%#v", fake.query, fake.arguments)
	}

	fake.rows = &catalogFakeRows{values: [][]any{{"1292052", updatedAt}}}
	sitemapMovies, err := store.LatestForSitemap(t.Context(), 1000)
	if err != nil || len(sitemapMovies) != 1 || sitemapMovies[0].DoubanID != "1292052" || !sitemapMovies[0].UpdatedAt.Equal(updatedAt) {
		t.Fatalf("sitemap movies/error = %+v/%v", sitemapMovies, err)
	}
	if !strings.Contains(fake.query, "SELECT douban_id, updated_at FROM media") || !strings.Contains(fake.query, "ORDER BY updated_at DESC LIMIT $1") || !reflect.DeepEqual(fake.arguments, []any{1000}) {
		t.Fatalf("sitemap query/args = %s/%#v", fake.query, fake.arguments)
	}
}

func TestPostgresSimilarQueryUsesVectorDistanceAndExcludesSource(t *testing.T) {
	fake := &catalogFakeDatabase{rows: &catalogFakeRows{}}
	store := NewPostgresStore(fake)
	movies, err := store.FindSimilar(t.Context(), "1292052", 12)
	if err != nil || len(movies) != 0 {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
	for _, expected := range []string{"embedding IS NOT NULL", "m.douban_id != $1", "ORDER BY m.embedding <-> target.embedding", "LIMIT $2"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("similar query missing %q: %s", expected, fake.query)
		}
	}
	if !reflect.DeepEqual(fake.arguments, []any{"1292052", 12}) {
		t.Fatalf("arguments = %#v", fake.arguments)
	}
}

func TestPostgresSeriesSeasonsUseExactTMDBSeriesIdentity(t *testing.T) {
	fake := &catalogFakeDatabase{rows: &catalogFakeRows{values: [][]any{
		{"35468745", "末日地堡 第一季", "2023", 7.8, "tv_season_1"},
		{"36857449", "末日地堡 第三季", "2026", 0.0, "tv_season_3"},
	}}}
	store := NewPostgresStore(fake)
	seasons, err := store.FindSeriesSeasons(t.Context(), "36857449")
	if err != nil || len(seasons) != 2 || seasons[0].SeasonNumber != 1 || seasons[1].SeasonNumber != 3 || !seasons[1].Current {
		t.Fatalf("series seasons/error = %+v/%v", seasons, err)
	}
	for _, expected := range []string{"external.provider = 'tmdb'", "external.external_id = target.external_id", "tv_season_", "ORDER BY CAST"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("series query missing %q: %s", expected, fake.query)
		}
	}
	if !reflect.DeepEqual(fake.arguments, []any{"36857449"}) {
		t.Fatalf("series arguments = %#v", fake.arguments)
	}
}

func TestPostgresSuggestRanksTitleMatchThenYear(t *testing.T) {
	fake := &catalogFakeDatabase{rows: &catalogFakeRows{}}
	store := NewPostgresStore(fake)
	if _, err := store.Suggest(t.Context(), "末日地堡", 5); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ILIKE $1", "LOWER(m.title) = LOWER($2)", "m.title ILIKE $3", "NULLIF(m.year, '') ASC", "LIMIT $4"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("suggest query missing %q: %s", expected, fake.query)
		}
	}
	if !reflect.DeepEqual(fake.arguments, []any{"%末日地堡%", "末日地堡", "末日地堡%", 5}) {
		t.Fatalf("suggest arguments = %#v", fake.arguments)
	}
}

func TestPostgresUpdateEmbeddingUsesValidatedVectorCast(t *testing.T) {
	fake := &catalogFakeDatabase{}
	store := NewPostgresStore(fake)
	vector := make([]float32, 768)
	vector[1] = 0.25
	if err := store.UpdateEmbedding(t.Context(), "1292052", "语义文本", "semantic-hash", vector); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.execQuery, "embedding = $4::vector") || !strings.Contains(fake.execQuery, "semantic_hash = $3") || !strings.Contains(fake.execQuery, "updated_at = NOW()") {
		t.Fatalf("update query = %s", fake.execQuery)
	}
	if len(fake.arguments) != 4 || fake.arguments[0] != "1292052" || fake.arguments[1] != "语义文本" || fake.arguments[2] != "semantic-hash" {
		t.Fatalf("arguments = %#v", fake.arguments)
	}
	encoded, ok := fake.arguments[3].(string)
	if !ok || !strings.HasPrefix(encoded, "[0,0.25,") || strings.Count(encoded, ",") != 767 {
		t.Fatalf("vector literal = %T %.80v", fake.arguments[3], fake.arguments[3])
	}
	bad := make([]float32, 768)
	bad[3] = float32(math.NaN())
	if err := store.UpdateEmbedding(t.Context(), "1292052", "bad", "hash", bad); err == nil {
		t.Fatal("non-finite vector was accepted")
	}
}

func TestPostgresMetadataRefreshQueueUsesUnifiedWorkerJobs(t *testing.T) {
	fake := &catalogFakeDatabase{row: catalogFakeRow{values: []any{42}}}
	store := NewPostgresStore(fake)
	jobID, err := store.EnqueueRefresh(t.Context(), "1292052", RefreshProviderReviews, "manual", 7)
	if err != nil || jobID != 42 {
		t.Fatalf("enqueue = %d/%v", jobID, err)
	}
	for _, expected := range []string{"INSERT INTO worker_jobs", "ON CONFLICT (task_type, subject_key)", "status IN ('pending', 'running')", "RETURNING id"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("enqueue query missing %q: %s", expected, fake.query)
		}
	}
	if len(fake.arguments) != 8 || fake.arguments[0] != RefreshProviderReviews || fake.arguments[1] != "1292052" || fake.arguments[3] != "manual" || fake.arguments[4] != 7 {
		t.Fatalf("enqueue arguments = %#v", fake.arguments)
	}
	if err := store.ScheduleDueRefreshes(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"next_refresh_at <= NOW()", "worker_jobs", "ON CONFLICT (task_type, subject_key)", "next_refresh_at = NOW() + INTERVAL '24 hours'"} {
		if !strings.Contains(fake.execQuery, expected) {
			t.Fatalf("schedule query missing %q: %s", expected, fake.execQuery)
		}
	}
	if err := store.ScheduleActiveContentRefreshes(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"playback_attempt_events", "played_10s", "INTERVAL '24 hours'", "INTERVAL '3 days'", "active_content"} {
		if !strings.Contains(fake.execQuery, expected) {
			t.Fatalf("active refresh query missing %q: %s", expected, fake.execQuery)
		}
	}
}

func TestPostgresPersonalizationUsesCanonicalMediaAndPositions(t *testing.T) {
	fake := &catalogFakeDatabase{rows: &catalogFakeRows{}}
	store := NewPostgresStore(fake)
	movies, err := store.UserRecommendations(t.Context(), 7, 60)
	if err != nil || len(movies) != 0 {
		t.Fatalf("movies/error = %+v/%v", movies, err)
	}
	for _, expected := range []string{"1.0 AS weight", "2.0 AS weight", "0.8 AS weight", "position.progress_percent>5", "AVG(embedding)", "m.id NOT IN", "ORDER BY m.embedding <-> uv.avg_embedding"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("query missing %q: %s", expected, fake.query)
		}
	}
	if !reflect.DeepEqual(fake.arguments, []any{7, 60}) {
		t.Fatalf("arguments = %#v", fake.arguments)
	}
	fake.rows = &catalogFakeRows{}
	_, err = store.ReliveClassics(t.Context(), 7, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"um.status='watched'", "m.rating_douban>=5", "INTERVAL '30 day'", "ORDER BY RANDOM()"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("relive query missing %q: %s", expected, fake.query)
		}
	}
}

type catalogFakeDatabase struct {
	query     string
	execQuery string
	arguments []any
	rows      database.Rows
	row       database.Row
}

func (fake *catalogFakeDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query, fake.arguments = query, arguments
	return fake.rows, nil
}

func (fake *catalogFakeDatabase) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	fake.query, fake.arguments = query, arguments
	return fake.row
}

func (fake *catalogFakeDatabase) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	fake.execQuery, fake.arguments = query, arguments
	return 1, nil
}

type catalogFakeRows struct {
	values [][]any
	index  int
}

func (rows *catalogFakeRows) Next() bool { return rows.index < len(rows.values) }

func (rows *catalogFakeRows) Scan(destinations ...any) error {
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
		if !source.Type().ConvertibleTo(destination.Type()) {
			return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
		}
		destination.Set(source.Convert(destination.Type()))
	}
	return nil
}

func (rows *catalogFakeRows) Err() error { return nil }
func (rows *catalogFakeRows) Close()     {}

type catalogFakeRow struct{ values []any }

func (row catalogFakeRow) Scan(destinations ...any) error {
	rows := &catalogFakeRows{values: [][]any{row.values}}
	return rows.Scan(destinations...)
}
