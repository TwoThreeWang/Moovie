package catalog

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/content"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// PostgresStore 是 catalog 各种存储接口的唯一实现。
type PostgresStore struct{ database database.Executor }

// NewPostgresStore 创建存储实现。
func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

// Count 返回媒体总数（后台首页用）。
func (store *PostgresStore) Count(ctx context.Context) (int, error) {
	var count int
	if err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM media`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count media: %w", err)
	}
	return count, nil
}

// UpdateEmbedding 写入向量及其来源文本和语义哈希。
func (store *PostgresStore) UpdateEmbedding(ctx context.Context, doubanID, content, semanticHash string, embedding []float32) error {
	vector, err := vectorLiteral(embedding)
	if err != nil {
		return err
	}
	_, err = store.database.Exec(ctx, `UPDATE media SET embedding_content = $2,
semantic_hash = $3, embedding = $4::vector,
updated_at = NOW() WHERE douban_id = $1`, doubanID, content, semanticHash, vector)
	if err != nil {
		return fmt.Errorf("update media embedding: %w", err)
	}
	return nil
}

// vectorLiteral 把 float32 切片转成 pgvector 的文本字面量，并校验维度和数值合法性。
func vectorLiteral(embedding []float32) (string, error) {
	if len(embedding) != 768 {
		return "", fmt.Errorf("embedding dimension mismatch: want 768, got %d", len(embedding))
	}
	values := make([]string, len(embedding))
	for index, value := range embedding {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return "", fmt.Errorf("embedding contains non-finite value at index %d", index)
		}
		values[index] = strconv.FormatFloat(float64(value), 'g', -1, 32)
	}
	return "[" + strings.Join(values, ",") + "]", nil
}

// movieColumns 把规范 media 表适配成页面层沿用的 Movie 结构。
// IMDb ID 保存在带命名空间的外部 ID 表中，避免在媒体主表继续堆来源专属字段。
const movieColumns = `m.id, m.douban_id, m.title, m.original_title, m.year, m.poster, m.rating_douban,
m.genres, m.countries, m.directors, m.actors, m.summary, m.duration,
COALESCE((SELECT external_id FROM media_external_ids x WHERE x.media_id = m.id AND x.provider = 'imdb'
ORDER BY x.is_primary DESC, x.updated_at DESC LIMIT 1), ''),
m.media_type, m.series_status, m.backdrops, m.embedding_content, m.semantic_hash, m.reviews_json,
m.reviews_updated_at, m.metadata_status, m.completeness_score, m.next_refresh_at, m.updated_at,
COALESCE(m.embedding::text, '')`

// FindByDoubanID 按豆瓣 ID 取一部影片，不存在时返回 (nil, nil)。
func (store *PostgresStore) FindByDoubanID(ctx context.Context, doubanID string) (*Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT `+movieColumns+` FROM media m WHERE m.douban_id = $1 LIMIT 1`, doubanID)
	if err != nil {
		return nil, fmt.Errorf("find movie: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	movie, err := scanMovie(rows)
	if err != nil {
		return nil, fmt.Errorf("scan movie: %w", err)
	}
	return &movie, nil
}

// FindByID 按 media.id 取一部影片，不存在时返回 (nil, nil)。
func (store *PostgresStore) FindByID(ctx context.Context, id int) (*Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT `+movieColumns+` FROM media m WHERE m.id = $1 LIMIT 1`, id)
	if err != nil {
		return nil, fmt.Errorf("find movie: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	movie, err := scanMovie(rows)
	if err != nil {
		return nil, fmt.Errorf("scan movie: %w", err)
	}
	return &movie, nil
}

// FindSimilar 用向量距离找相似影片（详情页的「相关推荐」）。
func (store *PostgresStore) FindSimilar(ctx context.Context, doubanID string, limit int) ([]Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT m.id, m.douban_id, m.title, m.original_title, m.year, m.poster, m.rating_douban,
m.genres, m.countries, m.directors, m.actors, m.summary, m.duration,
COALESCE((SELECT external_id FROM media_external_ids x WHERE x.media_id = m.id AND x.provider = 'imdb'
ORDER BY x.is_primary DESC, x.updated_at DESC LIMIT 1), ''),
m.media_type, m.series_status, m.backdrops, '' AS embedding_content, '' AS semantic_hash, '[]' AS reviews_json,
m.reviews_updated_at, m.metadata_status, m.completeness_score, m.next_refresh_at, m.updated_at,
''
FROM media m
JOIN LATERAL (SELECT embedding FROM media WHERE douban_id = $1 AND embedding IS NOT NULL) target ON true
WHERE m.douban_id != $1 AND m.embedding IS NOT NULL
ORDER BY m.embedding <-> target.embedding LIMIT $2`, doubanID, limit)
	if err != nil {
		return nil, fmt.Errorf("find similar movies: %w", err)
	}
	defer rows.Close()
	movies := make([]Movie, 0)
	for rows.Next() {
		movie, err := scanMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("scan similar movie: %w", err)
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate similar movies: %w", err)
	}
	return movies, nil
}

// FindSeriesSeasons 只使用相同的 TMDB TV ID 关联季度，不用标题猜测系列关系。
func (store *PostgresStore) FindSeriesSeasons(ctx context.Context, doubanID string) ([]SeriesSeason, error) {
	rows, err := store.database.Query(ctx, `WITH target_series AS (
    SELECT external.external_id
    FROM media current_media
    JOIN media_external_ids external ON external.media_id = current_media.id
    WHERE current_media.douban_id = $1 AND external.provider = 'tmdb'
      AND external.external_type ~ '^tv_season_[0-9]+$'
    ORDER BY external.is_primary DESC, external.updated_at DESC
    LIMIT 1
)
SELECT m.douban_id, m.title, m.year, m.rating_douban, external.external_type
FROM target_series target
JOIN media_external_ids external ON external.provider = 'tmdb'
  AND external.external_id = target.external_id
  AND external.external_type ~ '^tv_season_[0-9]+$'
JOIN media m ON m.id = external.media_id
ORDER BY CAST(SUBSTRING(external.external_type FROM '^tv_season_([0-9]+)$') AS INTEGER)`, doubanID)
	if err != nil {
		return nil, fmt.Errorf("find series seasons: %w", err)
	}
	defer rows.Close()
	seasons := make([]SeriesSeason, 0)
	for rows.Next() {
		var season SeriesSeason
		var externalType string
		if err := rows.Scan(&season.DoubanID, &season.Title, &season.Year, &season.Rating, &externalType); err != nil {
			return nil, fmt.Errorf("scan series season: %w", err)
		}
		season.SeasonNumber, _ = strconv.Atoi(strings.TrimPrefix(externalType, "tv_season_"))
		season.Current = season.DoubanID == doubanID
		if season.SeasonNumber > 0 {
			seasons = append(seasons, season)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series seasons: %w", err)
	}
	return seasons, nil
}

// Upsert 写入或更新一部影片，同时把 IMDb ID 写进外部 ID 表。
// 标题里带「第 N 季」时，external_type 会写成 tv_season_N，季度导航靠它归拢。
func (store *PostgresStore) Upsert(ctx context.Context, movie Movie) error {
	if movie.ReviewsUpdatedAt.IsZero() {
		movie.ReviewsUpdatedAt = time.Unix(0, 0).UTC()
	}
	externalType := ""
	if season := mediaidentity.TitleSeasonNumber(movie.Title, movie.OriginalTitle); season > 0 {
		externalType = fmt.Sprintf("tv_season_%d", season)
	}
	_, err := store.database.Exec(ctx, `WITH upserted AS (
INSERT INTO media
(media_type, douban_id, title, original_title, year, poster, rating_douban, genres, countries, directors, actors,
summary, duration, backdrops, embedding_content, reviews_json, reviews_updated_at, series_status, metadata_status, updated_at)
VALUES ('movie',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$14,$15,$16,$17,$18,
CASE WHEN $2 <> '' THEN 'partial' ELSE 'empty' END,NOW())
ON CONFLICT (douban_id) WHERE douban_id <> '' DO UPDATE SET title=EXCLUDED.title,
original_title=EXCLUDED.original_title, year=EXCLUDED.year, poster=EXCLUDED.poster,
rating_douban=EXCLUDED.rating_douban, genres=EXCLUDED.genres, countries=EXCLUDED.countries,
directors=EXCLUDED.directors, actors=EXCLUDED.actors, summary=EXCLUDED.summary,
duration=EXCLUDED.duration, backdrops=EXCLUDED.backdrops,
reviews_json=EXCLUDED.reviews_json, reviews_updated_at=EXCLUDED.reviews_updated_at,
series_status=CASE WHEN EXCLUDED.series_status <> '' THEN EXCLUDED.series_status ELSE media.series_status END,
updated_at=NOW()
RETURNING id, media_type
)
INSERT INTO media_external_ids (media_id, provider, external_type, external_id, is_primary, verified_at)
SELECT id, 'imdb', CASE WHEN $19 <> '' THEN $19
    WHEN media_type IN ('tv','series','season','show','animation') THEN 'tv' ELSE 'movie' END,
$13, TRUE, NOW() FROM upserted WHERE $13 <> ''
ON CONFLICT (provider, external_type, external_id) DO UPDATE SET
media_id=EXCLUDED.media_id, is_primary=TRUE, verified_at=NOW(), updated_at=NOW()`,
		movie.DoubanID, movie.Title, movie.OriginalTitle, movie.Year, movie.Poster, movie.Rating,
		movie.Genres, movie.Countries, movie.Directors, movie.Actors, movie.Summary, movie.Duration,
		movie.IMDbID, movie.Backdrops, movie.EmbeddingContent, movie.ReviewsJSON, movie.ReviewsUpdatedAt,
		movie.SeriesStatus, externalType)
	if err != nil {
		return fmt.Errorf("upsert media: %w", err)
	}
	return nil
}

// imdbCandidatePredicate 是两个阶段共用的候选条件。
// 豆瓣 ID 的格式约束与 validDoubanID 必须逐字一致：不合规的值在 wikidataQuery 里
// 会被静默丢掉，留在候选里只会让「查了没命中」和「根本没查」在日志上无法区分。
// 索引 media_imdb_batch_lookup_idx / media_imdb_fallback_lookup_idx 依赖同一个谓词。
const imdbCandidatePredicate = `m.douban_id ~ '^[0-9]{6,9}$'
  AND NOT EXISTS (SELECT 1 FROM media_external_ids x WHERE x.media_id = m.id AND x.provider = 'imdb')`

// PendingIMDbBatchLookups 返回该交给批量源（Wikidata）的豆瓣 ID。
// 批量查询一次问 200 条只算一个请求，所以这个队列可以扫得又快又频繁；
// retryAfter 存在的意义是给 Wikidata 后来新增的映射一个被发现的机会。
func (store *PostgresStore) PendingIMDbBatchLookups(ctx context.Context, limit int, retryAfter time.Duration) ([]string, error) {
	if limit <= 0 {
		limit = 200
	}
	if retryAfter <= 0 {
		retryAfter = 7 * 24 * time.Hour
	}
	return store.pendingIMDbLookups(ctx, `SELECT m.douban_id FROM media m
WHERE `+imdbCandidatePredicate+`
  AND (m.imdb_batch_lookup_at IS NULL OR m.imdb_batch_lookup_at < NOW() - $2 * INTERVAL '1 second')
ORDER BY m.imdb_batch_lookup_at NULLS FIRST, m.id LIMIT $1`, limit, retryAfter)
}

// PendingIMDbFallbackLookups 返回该交给逐条兜底源（wmdb）的豆瓣 ID。
// 只有批量源已经给过结论的条目才有资格进这个队列——兜底源限流极严，
// 不能把批量源本来就能解决的条目也喂给它。
func (store *PostgresStore) PendingIMDbFallbackLookups(ctx context.Context, limit int, retryAfter time.Duration) ([]string, error) {
	if limit <= 0 {
		limit = 32
	}
	if retryAfter <= 0 {
		retryAfter = 30 * 24 * time.Hour
	}
	return store.pendingIMDbLookups(ctx, `SELECT m.douban_id FROM media m
WHERE `+imdbCandidatePredicate+`
  AND m.imdb_batch_lookup_at IS NOT NULL
  AND (m.imdb_lookup_at IS NULL OR m.imdb_lookup_at < NOW() - $2 * INTERVAL '1 second')
ORDER BY m.imdb_lookup_at NULLS FIRST, m.id LIMIT $1`, limit, retryAfter)
}

// pendingIMDbLookups 是上面两个待办队列的公共查询。
func (store *PostgresStore) pendingIMDbLookups(ctx context.Context, query string, limit int, retryAfter time.Duration) ([]string, error) {
	rows, err := store.database.Query(ctx, query, limit, retryAfter.Seconds())
	if err != nil {
		return nil, fmt.Errorf("list pending IMDb lookups: %w", err)
	}
	defer rows.Close()
	doubanIDs := make([]string, 0, limit)
	for rows.Next() {
		var doubanID string
		if err := rows.Scan(&doubanID); err != nil {
			return nil, fmt.Errorf("scan pending IMDb lookup: %w", err)
		}
		doubanIDs = append(doubanIDs, doubanID)
	}
	return doubanIDs, rows.Err()
}

// SaveIMDbID 只写映射关系，不碰媒体主资料。external_type 的取值规则与 Upsert 保持一致：
// 同一个 IMDb ID 在电影和剧集命名空间下是两条记录。
func (store *PostgresStore) SaveIMDbID(ctx context.Context, doubanID, imdbID string) error {
	if doubanID == "" || imdbID == "" {
		return fmt.Errorf("douban ID and IMDb ID are required")
	}
	_, err := store.database.Exec(ctx, `INSERT INTO media_external_ids
(media_id, provider, external_type, external_id, is_primary, verified_at)
SELECT m.id, 'imdb',
    CASE WHEN m.media_type IN ('tv','series','season','show','animation') THEN 'tv' ELSE 'movie' END,
    $2, TRUE, NOW()
FROM media m WHERE m.douban_id = $1
ON CONFLICT (provider, external_type, external_id) DO UPDATE SET
media_id=EXCLUDED.media_id, is_primary=TRUE, verified_at=NOW(), updated_at=NOW()`, doubanID, imdbID)
	if err != nil {
		return fmt.Errorf("save IMDb mapping: %w", err)
	}
	return nil
}

// MarkIMDbBatchAttempt 记录这批豆瓣 ID 刚被批量源问过，无论是否命中。
// SPARQL 返回 200 而某个 ID 没有 binding 是一个确定的结论：Wikidata 上就是没有
// P4529→P345 的连通路径。把它当成「不确定」而不记账，队首就永远不会往前滚动。
func (store *PostgresStore) MarkIMDbBatchAttempt(ctx context.Context, doubanIDs []string) error {
	return store.markIMDbAttempt(ctx, `UPDATE media SET imdb_batch_lookup_at = NOW() WHERE douban_id = ANY($1)`, doubanIDs)
}

// MarkIMDbLookupAttempt 记录这批豆瓣 ID 刚被兜底源问过，无论是否命中。
func (store *PostgresStore) MarkIMDbLookupAttempt(ctx context.Context, doubanIDs []string) error {
	return store.markIMDbAttempt(ctx, `UPDATE media SET imdb_lookup_at = NOW() WHERE douban_id = ANY($1)`, doubanIDs)
}

// markIMDbAttempt 是上面两个记账方法的公共实现。
func (store *PostgresStore) markIMDbAttempt(ctx context.Context, query string, doubanIDs []string) error {
	if len(doubanIDs) == 0 {
		return nil
	}
	if _, err := store.database.Exec(ctx, query, doubanIDs); err != nil {
		return fmt.Errorf("mark IMDb lookup attempt: %w", err)
	}
	return nil
}

// DeleteByDoubanID 删除一部影片（详情页发现标题为空的脏数据时会调用）。
func (store *PostgresStore) DeleteByDoubanID(ctx context.Context, doubanID string) error {
	if _, err := store.database.Exec(ctx, `DELETE FROM media WHERE douban_id = $1`, doubanID); err != nil {
		return fmt.Errorf("delete media: %w", err)
	}
	return nil
}

// Latest 按更新时间倒序取影片。
func (store *PostgresStore) Latest(ctx context.Context, limit int) ([]Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT `+movieColumns+` FROM media m ORDER BY m.updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("latest movies: %w", err)
	}
	defer rows.Close()
	movies := make([]Movie, 0)
	for rows.Next() {
		movie, err := scanMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("scan movie: %w", err)
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate movies: %w", err)
	}
	return movies, nil
}

// LatestForSitemap 刻意只查询生成 XML 所需的两个字段。
// 普通 Latest 还会加载简介、演员、剧照和评论；若 sitemap 也使用它，数据增长后 SEO 端点会无谓变重。
func (store *PostgresStore) LatestForSitemap(ctx context.Context, limit int) ([]content.SitemapMovie, error) {
	rows, err := store.database.Query(ctx, `SELECT douban_id, updated_at FROM media ORDER BY updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("latest sitemap movies: %w", err)
	}
	defer rows.Close()
	movies := make([]content.SitemapMovie, 0)
	for rows.Next() {
		var movie content.SitemapMovie
		if err := rows.Scan(&movie.DoubanID, &movie.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sitemap movie: %w", err)
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sitemap movies: %w", err)
	}
	return movies, nil
}

// Suggest 按标题模糊搜索，完全相同 > 前缀匹配 > 其他，同档再按年份和评分排。
func (store *PostgresStore) Suggest(ctx context.Context, keyword string, limit int) ([]Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT `+movieColumns+` FROM media m
	WHERE m.title ILIKE $1 OR m.original_title ILIKE $1
	ORDER BY CASE
	    WHEN LOWER(m.title) = LOWER($2) OR LOWER(m.original_title) = LOWER($2) THEN 0
	    WHEN m.title ILIKE $3 OR m.original_title ILIKE $3 THEN 1
	    ELSE 2
	END, NULLIF(m.year, '') ASC NULLS LAST, m.rating_douban DESC, m.updated_at DESC
	LIMIT $4`, "%"+keyword+"%", strings.TrimSpace(keyword), strings.TrimSpace(keyword)+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("suggest movies: %w", err)
	}
	defer rows.Close()
	movies := make([]Movie, 0)
	for rows.Next() {
		movie, err := scanMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("scan movie: %w", err)
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate movie suggestions: %w", err)
	}
	return movies, nil
}

// Popular 取有评分且已生成向量的影片，按评分倒序（上游热门接口挂了时兜底用）。
func (store *PostgresStore) Popular(ctx context.Context, limit int) ([]Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT `+movieColumns+` FROM media m
WHERE m.rating_douban > 0 AND m.embedding IS NOT NULL ORDER BY m.rating_douban DESC, m.updated_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("popular movies: %w", err)
	}
	defer rows.Close()
	movies := make([]Movie, 0)
	for rows.Next() {
		movie, err := scanMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("scan popular movie: %w", err)
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate popular movies: %w", err)
	}
	return movies, nil
}

// scanMovie 按 movieColumns 的顺序扫描一行，字段顺序必须与该常量保持一致。
func scanMovie(row interface{ Scan(...any) error }) (Movie, error) {
	var movie Movie
	var embeddingText string
	err := row.Scan(&movie.ID, &movie.DoubanID, &movie.Title, &movie.OriginalTitle, &movie.Year,
		&movie.Poster, &movie.Rating, &movie.Genres, &movie.Countries, &movie.Directors, &movie.Actors,
		&movie.Summary, &movie.Duration, &movie.IMDbID, &movie.MediaType, &movie.SeriesStatus, &movie.Backdrops,
		&movie.EmbeddingContent, &movie.EmbeddingSemanticHash, &movie.ReviewsJSON,
		&movie.ReviewsUpdatedAt, &movie.MetadataStatus, &movie.CompletenessScore,
		&movie.NextRefreshAt, &movie.UpdatedAt, &embeddingText)
	if err != nil {
		return movie, err
	}
	movie.Embedding, err = parseEmbedding(embeddingText)
	return movie, err
}

// parseEmbedding 解析 pgvector 的文本表示。没有注册 pgvector 的 pgx 类型，
// 所以读取侧统一走 embedding::text。
func parseEmbedding(text string) ([]float32, error) {
	if text == "" {
		return nil, nil
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"), ",")
	vector := make([]float32, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("parse embedding value %q: %w", part, err)
		}
		vector = append(vector, float32(value))
	}
	return vector, nil
}
