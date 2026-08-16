package datamigrate

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
)

type canonicalMedia struct {
	DoubanID, MediaType, Title, OriginalTitle, Year, Poster, Backdrops string
	Summary, Genres, Countries, Directors, Actors, Duration            string
	EmbeddingContent, SemanticHash, Embedding                          string
	IMDbID, ReviewsJSON                                                string
	Rating                                                             float64
	ReviewsUpdatedAt, CreatedAt, UpdatedAt                             time.Time
	TypeCertain                                                        bool
}

type canonicalHistory struct {
	UserID                             int64
	DoubanID, SourceKey, VodID         string
	Title, Poster, Episode, EpisodeKey string
	Season, Progress                   int
	PositionSeconds, DurationSeconds   float64
	Completed                          bool
	ActivityAt                         time.Time
	DeletedAt                          *time.Time
}

type canonicalResource struct {
	SourceKey, VodID, DoubanID string
}

type mediaTypeEvidence struct {
	MediaType string
	Certain   bool
}

func (importer Importer) inspectCanonical(ctx context.Context, schema string) ([]TablePlan, error) {
	sourceMedia, err := importer.loadSourceMedia(ctx, schema)
	if err != nil {
		return nil, err
	}
	targetMedia, err := loadTargetMedia(ctx, importer.Target, schema)
	if err != nil {
		return nil, err
	}
	mediaPlan := TablePlan{Table: "media", Keys: []string{"douban_id"}, Available: true,
		SourceRows: len(sourceMedia), TargetRows: len(targetMedia),
		CopiedColumns: []string{"douban_id", "title", "original_title", "year", "poster", "backdrops", "summary",
			"genres", "countries", "directors", "actors", "duration", "rating_douban", "embedding", "reviews_json", "imdb"}}
	for key, source := range sourceMedia {
		target, exists := targetMedia[key]
		switch {
		case !exists:
			mediaPlan.InsertRows++
		case canonicalMediaChanged(source, target):
			mediaPlan.UpdateRows++
		default:
			mediaPlan.SkipRows++
		}
	}
	for key := range targetMedia {
		if _, exists := sourceMedia[key]; !exists {
			mediaPlan.TargetOnlyRows++
		}
	}

	sourceHistory, err := importer.loadSourceHistory(ctx, schema)
	if err != nil {
		return nil, err
	}
	targetHistory, err := loadTargetHistory(ctx, importer.Target, schema)
	if err != nil {
		return nil, err
	}
	historyPlan := TablePlan{Table: "playback_positions", Keys: []string{"user_id", "media_or_resource", "episode_key"},
		Available: true, SourceRows: len(sourceHistory), TargetRows: len(targetHistory),
		CopiedColumns: []string{"user_id", "media_id", "position_seconds", "duration_seconds", "progress_percent",
			"last_source_key", "last_vod_id", "title", "poster", "episode", "activity_at"}}
	for key, source := range sourceHistory {
		target, exists := targetHistory[key]
		switch {
		case !exists:
			historyPlan.InsertRows++
		case canonicalHistoryChanged(source, target):
			historyPlan.UpdateRows++
		default:
			historyPlan.SkipRows++
		}
	}
	for key := range targetHistory {
		if _, exists := sourceHistory[key]; !exists {
			historyPlan.TargetOnlyRows++
		}
	}

	sourceResources, err := loadSourceResources(ctx, importer.Source, schema)
	if err != nil {
		return nil, err
	}
	targetResources, err := loadTargetResources(ctx, importer.Target, schema)
	if err != nil {
		return nil, err
	}
	resourcePlan := TablePlan{Table: "resource_media_links", Keys: []string{"source_key", "vod_id"}, Available: true,
		SourceRows: len(sourceResources), TargetRows: len(targetResources), CopiedColumns: []string{"source_key", "vod_id", "media_id"}}
	for key, source := range sourceResources {
		target, exists := targetResources[key]
		switch {
		case !exists:
			resourcePlan.InsertRows++
		case target.DoubanID != source.DoubanID:
			resourcePlan.UpdateRows++
		default:
			resourcePlan.SkipRows++
		}
	}
	for key := range targetResources {
		if _, exists := sourceResources[key]; !exists {
			resourcePlan.TargetOnlyRows++
		}
	}

	userMediaPlan, err := inspectUserMovieMediaLinks(ctx, importer.Target, schema)
	if err != nil {
		return nil, err
	}
	return []TablePlan{mediaPlan, historyPlan, resourcePlan, userMediaPlan}, nil
}

func (importer Importer) loadSourceMedia(ctx context.Context, schema string) (map[string]canonicalMedia, error) {
	types, err := loadSourceMediaTypes(ctx, importer.Source, schema)
	if err != nil {
		return nil, err
	}
	sourceColumns, err := columns(ctx, importer.Source, schema, "movies")
	if err != nil {
		return nil, fmt.Errorf("inspect source movie columns: %w", err)
	}
	optionalText := func(column string) string {
		if sourceColumns[column] {
			return "COALESCE(" + quote(column) + ",'')"
		}
		return `''`
	}
	embedding := `''`
	if sourceColumns["embedding"] {
		embedding = `COALESCE(embedding::text,'')`
	}
	reviewsUpdatedAt := "TO_TIMESTAMP(0)"
	if sourceColumns["reviews_updated_at"] {
		reviewsUpdatedAt = "COALESCE(reviews_updated_at,TO_TIMESTAMP(0))"
	}
	imdbID := optionalText("imdb_id")
	if sourceColumns["im_db_id"] {
		imdbID = fmt.Sprintf("COALESCE(NULLIF(%s,''),%s)", imdbID, optionalText("im_db_id"))
	}
	rows, err := importer.Source.Query(ctx, fmt.Sprintf(`SELECT %s,%s,%s,%s,%s,COALESCE(rating,0),
%s,%s,%s,%s,%s,%s,%s,%s,%s,
%s,%s,%s,%s,COALESCE(updated_at,NOW())
FROM %s.movies WHERE douban_id<>''`, optionalText("douban_id"), optionalText("title"),
		optionalText("original_title"), optionalText("year"), optionalText("poster"), optionalText("genres"),
		optionalText("countries"), optionalText("directors"), optionalText("actors"), optionalText("summary"),
		optionalText("duration"), imdbID, optionalText("backdrops"), optionalText("embedding_content"),
		embedding, optionalText("embedding_semantic_hash"), optionalText("reviews_json"), reviewsUpdatedAt, quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read source movies: %w", err)
	}
	result := make(map[string]canonicalMedia)
	for rows.Next() {
		var item canonicalMedia
		if err := rows.Scan(&item.DoubanID, &item.Title, &item.OriginalTitle, &item.Year, &item.Poster, &item.Rating,
			&item.Genres, &item.Countries, &item.Directors, &item.Actors, &item.Summary, &item.Duration,
			&item.IMDbID, &item.Backdrops, &item.EmbeddingContent, &item.Embedding, &item.SemanticHash,
			&item.ReviewsJSON, &item.ReviewsUpdatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan source movie: %w", err)
		}
		item.CreatedAt = item.UpdatedAt
		evidence := types[item.DoubanID]
		item.MediaType, item.TypeCertain = evidence.MediaType, evidence.Certain
		if item.MediaType == "" {
			item.MediaType = "movie"
		}
		result[item.DoubanID] = item
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source movies: %w", err)
	}

	// 片单可能引用已被旧目录清理的豆瓣条目；用用户保存的快照创建最小媒体行。
	rows, err = importer.Source.Query(ctx, fmt.Sprintf(`SELECT movie_id,MAX(title),MAX(poster),MAX(year),
MIN(created_at),MAX(updated_at) FROM %s.user_movies WHERE movie_id<>'' GROUP BY movie_id`, quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read source user movie identities: %w", err)
	}
	for rows.Next() {
		var item canonicalMedia
		if err := rows.Scan(&item.DoubanID, &item.Title, &item.Poster, &item.Year, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan source user movie identity: %w", err)
		}
		if _, exists := result[item.DoubanID]; exists {
			continue
		}
		evidence := types[item.DoubanID]
		item.MediaType, item.TypeCertain = evidence.MediaType, evidence.Certain
		if item.MediaType == "" {
			item.MediaType = "movie"
		}
		item.ReviewsUpdatedAt = time.Unix(0, 0).UTC()
		result[item.DoubanID] = item
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source user movie identities: %w", err)
	}

	// 极少数历史记录引用了已不在目录和片单中的豆瓣条目。
	// 用历史快照补最小媒体行，否则迁移后无法稳定识别同一部影片。
	rows, err = importer.Source.Query(ctx, fmt.Sprintf(`SELECT douban_id,MAX(COALESCE(title,'')),MAX(COALESCE(poster,'')),
MIN(watched_at),MAX(watched_at) FROM %s.watch_histories
WHERE douban_id<>'' GROUP BY douban_id`, quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read source history media identities: %w", err)
	}
	for rows.Next() {
		var doubanID, title, poster string
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&doubanID, &title, &poster, &createdAt, &updatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan source history media identity: %w", err)
		}
		doubanID = normalizeMigratedDoubanID(doubanID)
		if doubanID == "" {
			continue
		}
		if _, exists := result[doubanID]; exists {
			continue
		}
		evidence := types[doubanID]
		mediaType := evidence.MediaType
		if mediaType == "" {
			mediaType = "movie"
		}
		result[doubanID] = canonicalMedia{DoubanID: doubanID, MediaType: mediaType, TypeCertain: evidence.Certain,
			Title: title, Poster: poster, ReviewsUpdatedAt: time.Unix(0, 0).UTC(), CreatedAt: createdAt, UpdatedAt: updatedAt}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate source history media identities: %w", err)
	}
	return result, nil
}

func normalizeMigratedDoubanID(value string) string {
	value = strings.TrimSpace(value)
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return ""
	}
	return value
}

func loadSourceMediaTypes(ctx context.Context, source Querier, schema string) (map[string]mediaTypeEvidence, error) {
	rows, err := source.Query(ctx, fmt.Sprintf(`SELECT vod_douban_id,type_name FROM %s.vod_items
WHERE vod_douban_id<>'' AND type_name<>''`, quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read source media types: %w", err)
	}
	defer rows.Close()
	result := make(map[string]mediaTypeEvidence)
	for rows.Next() {
		var doubanID, raw string
		if err := rows.Scan(&doubanID, &raw); err != nil {
			return nil, fmt.Errorf("scan source media type: %w", err)
		}
		mediaType := normalizeMigratedMediaType(raw)
		if mediaType == "" {
			continue
		}
		// 任一来源明确识别为剧集时优先按 tv 处理；电影证据只在没有 tv 证据时使用。
		current := result[doubanID]
		if current.MediaType != "tv" || mediaType == "tv" {
			result[doubanID] = mediaTypeEvidence{MediaType: mediaType, Certain: true}
		}
	}
	return result, rows.Err()
}

func normalizeMigratedMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "movie" || value == "film" || strings.Contains(value, "电影"):
		return "movie"
	case value == "tv" || value == "series" || value == "season" || value == "show" || value == "animation" ||
		strings.Contains(value, "电视") || strings.Contains(value, "连续剧") || strings.Contains(value, "动漫") ||
		strings.Contains(value, "综艺") || strings.HasSuffix(value, "剧"):
		return "tv"
	default:
		return ""
	}
}

func loadTargetMedia(ctx context.Context, target Querier, schema string) (map[string]canonicalMedia, error) {
	mediaColumns, err := columns(ctx, target, schema, "media")
	if err != nil {
		return nil, fmt.Errorf("inspect target media columns: %w", err)
	}
	reviewsJSON := `''`
	if mediaColumns["reviews_json"] {
		reviewsJSON = "media.reviews_json"
	}
	reviewsUpdatedAt := "TO_TIMESTAMP(0)"
	if mediaColumns["reviews_updated_at"] {
		reviewsUpdatedAt = "media.reviews_updated_at"
	}
	rows, err := target.Query(ctx, fmt.Sprintf(`SELECT media.douban_id,media.media_type,media.title,
media.original_title,media.year,media.poster,media.backdrops,media.summary,media.genres,media.countries,
media.directors,media.actors,media.duration,media.embedding_content,media.semantic_hash,
COALESCE(media.embedding::text,''),media.rating_douban,%s,%s,media.updated_at,
COALESCE((SELECT external_id FROM %s.media_external_ids external
          WHERE external.media_id=media.id AND external.provider='imdb'
          ORDER BY external.is_primary DESC,external.updated_at DESC LIMIT 1),'')
FROM %s.media media WHERE media.douban_id<>''`, reviewsJSON, reviewsUpdatedAt, quote(schema), quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read target media; 请先让新系统完成最终 schema migration: %w", err)
	}
	defer rows.Close()
	result := make(map[string]canonicalMedia)
	for rows.Next() {
		var item canonicalMedia
		if err := rows.Scan(&item.DoubanID, &item.MediaType, &item.Title, &item.OriginalTitle, &item.Year,
			&item.Poster, &item.Backdrops, &item.Summary, &item.Genres, &item.Countries, &item.Directors,
			&item.Actors, &item.Duration, &item.EmbeddingContent, &item.SemanticHash, &item.Embedding,
			&item.Rating, &item.ReviewsJSON, &item.ReviewsUpdatedAt, &item.UpdatedAt, &item.IMDbID); err != nil {
			return nil, fmt.Errorf("scan target media: %w", err)
		}
		result[item.DoubanID] = item
	}
	return result, rows.Err()
}

func canonicalMediaChanged(source, target canonicalMedia) bool {
	if source.TypeCertain && source.MediaType != target.MediaType {
		return true
	}
	for _, pair := range [][2]string{
		{source.Title, target.Title}, {source.OriginalTitle, target.OriginalTitle}, {source.Year, target.Year},
		{source.Poster, target.Poster}, {source.Backdrops, target.Backdrops}, {source.Summary, target.Summary},
		{source.Genres, target.Genres}, {source.Countries, target.Countries}, {source.Directors, target.Directors},
		{source.Actors, target.Actors}, {source.Duration, target.Duration},
		{source.EmbeddingContent, target.EmbeddingContent}, {source.SemanticHash, target.SemanticHash},
		{source.Embedding, target.Embedding}, {source.ReviewsJSON, target.ReviewsJSON}, {source.IMDbID, target.IMDbID},
	} {
		if pair[0] != "" && pair[0] != pair[1] {
			return true
		}
	}
	return source.Rating > 0 && source.Rating != target.Rating
}

func (importer Importer) loadSourceHistory(ctx context.Context, schema string) (map[string]canonicalHistory, error) {
	rows, err := importer.Source.Query(ctx, fmt.Sprintf(`SELECT user_id,douban_id,source,vod_id,title,poster,
episode,progress,last_time,duration,watched_at FROM %s.watch_histories`, quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read source watch histories: %w", err)
	}
	defer rows.Close()
	result := make(map[string]canonicalHistory)
	for rows.Next() {
		var item canonicalHistory
		if err := rows.Scan(&item.UserID, &item.DoubanID, &item.SourceKey, &item.VodID, &item.Title,
			&item.Poster, &item.Episode, &item.Progress, &item.PositionSeconds, &item.DurationSeconds,
			&item.ActivityAt); err != nil {
			return nil, fmt.Errorf("scan source watch history: %w", err)
		}
		item.DoubanID = normalizeMigratedDoubanID(item.DoubanID)
		item.Season, item.EpisodeKey = mediaidentity.NormalizeEpisodeLabel(item.Episode)
		item.Progress = clampProgress(item.Progress)
		item.Completed = item.Progress >= 100
		key := canonicalHistoryKey(item)
		if current, exists := result[key]; !exists || item.ActivityAt.After(current.ActivityAt) {
			result[key] = item
		}
	}
	return result, rows.Err()
}

func loadTargetHistory(ctx context.Context, target Querier, schema string) (map[string]canonicalHistory, error) {
	rows, err := target.Query(ctx, fmt.Sprintf(`SELECT position.user_id,COALESCE(media.douban_id,''),
position.last_source_key,position.last_vod_id,position.title,position.poster,position.episode,
position.season_number,position.episode_key,position.progress_percent,position.position_seconds,
position.duration_seconds,position.completed,position.activity_at,position.deleted_at
FROM %s.playback_positions position
LEFT JOIN %s.media media ON media.id=position.media_id`, quote(schema), quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read target playback positions: %w", err)
	}
	defer rows.Close()
	result := make(map[string]canonicalHistory)
	for rows.Next() {
		var item canonicalHistory
		if err := rows.Scan(&item.UserID, &item.DoubanID, &item.SourceKey, &item.VodID, &item.Title,
			&item.Poster, &item.Episode, &item.Season, &item.EpisodeKey, &item.Progress,
			&item.PositionSeconds, &item.DurationSeconds, &item.Completed, &item.ActivityAt, &item.DeletedAt); err != nil {
			return nil, fmt.Errorf("scan target playback position: %w", err)
		}
		key := canonicalHistoryKey(item)
		if current, exists := result[key]; !exists || item.ActivityAt.After(current.ActivityAt) {
			result[key] = item
		}
	}
	return result, rows.Err()
}

func canonicalHistoryKey(item canonicalHistory) string {
	if item.DoubanID != "" {
		return fmt.Sprintf("%d\x00media\x00%s\x00%d\x00%s", item.UserID, item.DoubanID, item.Season, item.EpisodeKey)
	}
	return fmt.Sprintf("%d\x00resource\x00%s\x00%s\x00%d\x00%s", item.UserID, item.SourceKey, item.VodID, item.Season, item.EpisodeKey)
}

func canonicalHistoryChanged(source, target canonicalHistory) bool {
	// 新库在迁移窗口中产生的更新优先，旧快照不能倒退用户进度或复活已删除记录。
	if target.ActivityAt.After(source.ActivityAt) {
		return false
	}
	return target.DeletedAt != nil || source.Progress != target.Progress || source.PositionSeconds != target.PositionSeconds ||
		source.DurationSeconds != target.DurationSeconds || source.Completed != target.Completed ||
		source.SourceKey != target.SourceKey || source.VodID != target.VodID || source.Title != target.Title ||
		source.Poster != target.Poster || source.Episode != target.Episode
}

func clampProgress(value int) int {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func loadSourceResources(ctx context.Context, source Querier, schema string) (map[string]canonicalResource, error) {
	rows, err := source.Query(ctx, fmt.Sprintf(`SELECT source_key,vod_id,vod_douban_id FROM %s.vod_items
WHERE vod_douban_id<>''`, quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read source resource identities: %w", err)
	}
	defer rows.Close()
	result := make(map[string]canonicalResource)
	for rows.Next() {
		var item canonicalResource
		if err := rows.Scan(&item.SourceKey, &item.VodID, &item.DoubanID); err != nil {
			return nil, fmt.Errorf("scan source resource identity: %w", err)
		}
		result[item.SourceKey+"\x00"+item.VodID] = item
	}
	return result, rows.Err()
}

func loadTargetResources(ctx context.Context, target Querier, schema string) (map[string]canonicalResource, error) {
	rows, err := target.Query(ctx, fmt.Sprintf(`SELECT link.source_key,link.vod_id,media.douban_id
FROM %s.resource_media_links link JOIN %s.media media ON media.id=link.media_id`, quote(schema), quote(schema)))
	if err != nil {
		return nil, fmt.Errorf("read target resource identities: %w", err)
	}
	defer rows.Close()
	result := make(map[string]canonicalResource)
	for rows.Next() {
		var item canonicalResource
		if err := rows.Scan(&item.SourceKey, &item.VodID, &item.DoubanID); err != nil {
			return nil, fmt.Errorf("scan target resource identity: %w", err)
		}
		result[item.SourceKey+"\x00"+item.VodID] = item
	}
	return result, rows.Err()
}

func inspectUserMovieMediaLinks(ctx context.Context, target Querier, schema string) (TablePlan, error) {
	plan := TablePlan{Table: "user_movies.media_id", Keys: []string{"user_id", "movie_id"}, Available: true,
		CopiedColumns: []string{"media_id"}}
	userMovieColumns, err := columns(ctx, target, schema, "user_movies")
	if err != nil {
		return plan, fmt.Errorf("inspect user movie columns: %w", err)
	}
	if !userMovieColumns["media_id"] {
		if err := target.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s.user_movies WHERE movie_id<>''`, quote(schema))).Scan(&plan.SourceRows); err != nil {
			return plan, fmt.Errorf("inspect pending user movie media links: %w", err)
		}
		plan.UpdateRows = plan.SourceRows
		plan.Note = "schema migration 0030 will add media_id before apply"
		return plan, nil
	}
	if err := target.QueryRow(ctx, fmt.Sprintf(`SELECT COUNT(*),COUNT(media_id) FROM %s.user_movies
WHERE movie_id<>''`, quote(schema))).Scan(&plan.SourceRows, &plan.TargetRows); err != nil {
		return plan, fmt.Errorf("inspect user movie media links: %w", err)
	}
	plan.UpdateRows = plan.SourceRows - plan.TargetRows
	plan.SkipRows = plan.TargetRows
	return plan, nil
}

// CanonicalBackfill 直接把旧库事实转换成最终模型，不在目标库创建任何兼容表。
func (importer Importer) CanonicalBackfill(ctx context.Context, target Querier, schema string) (int, error) {
	if _, err := target.Exec(ctx, fmt.Sprintf("SET LOCAL search_path TO %s", quote(schema))); err != nil {
		return 0, err
	}
	mediaRows, err := importer.loadSourceMedia(ctx, schema)
	if err != nil {
		return 0, err
	}
	total, err := applyCanonicalMedia(ctx, target, schema, mediaRows)
	if err != nil {
		return total, err
	}

	// favorites 已在前一步写入 user_movies；此处统一补齐所有片单记录的规范外键。
	tag, err := target.Exec(ctx, fmt.Sprintf(`UPDATE %s.user_movies user_movie SET media_id=media.id
FROM %s.media media WHERE media.douban_id=user_movie.movie_id
AND user_movie.media_id IS DISTINCT FROM media.id`, quote(schema), quote(schema)))
	if err != nil {
		return total, fmt.Errorf("link user movies to media: %w", err)
	}
	total += int(tag.RowsAffected())

	resources, err := loadSourceResources(ctx, importer.Source, schema)
	if err != nil {
		return total, err
	}
	changed, err := applyCanonicalResources(ctx, target, schema, resources)
	if err != nil {
		return total, err
	}
	total += changed

	histories, err := importer.loadSourceHistory(ctx, schema)
	if err != nil {
		return total, err
	}
	changed, err = applyCanonicalHistory(ctx, target, schema, histories)
	if err != nil {
		return total, err
	}
	total += changed

	// 标题别名是搜索能力，不是旧系统兼容层；来源明确记为 douban。
	for _, statement := range []string{
		fmt.Sprintf(`UPDATE %s.media_aliases SET source='douban',updated_at=NOW() WHERE source='legacy'`, quote(schema)),
		fmt.Sprintf(`INSERT INTO %s.media_aliases (media_id,alias,normalized_alias,source,alias_type)
SELECT id,title,LOWER(REGEXP_REPLACE(NORMALIZE(title,NFKC),'[[:space:][:punct:]]+','','g')),'douban','title'
FROM %s.media WHERE title<>'' ON CONFLICT (media_id,normalized_alias) DO UPDATE SET
alias=EXCLUDED.alias,source=CASE WHEN media_aliases.source='legacy' THEN 'douban' ELSE media_aliases.source END,updated_at=NOW()`, quote(schema), quote(schema)),
		fmt.Sprintf(`INSERT INTO %s.media_aliases (media_id,alias,normalized_alias,source,alias_type)
SELECT id,original_title,LOWER(REGEXP_REPLACE(NORMALIZE(original_title,NFKC),'[[:space:][:punct:]]+','','g')),'douban','original_title'
FROM %s.media WHERE original_title<>'' ON CONFLICT (media_id,normalized_alias) DO UPDATE SET
alias=EXCLUDED.alias,source=CASE WHEN media_aliases.source='legacy' THEN 'douban' ELSE media_aliases.source END,updated_at=NOW()`, quote(schema), quote(schema)),
	} {
		tag, err := target.Exec(ctx, statement)
		if err != nil {
			return total, fmt.Errorf("upsert media aliases: %w", err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

func applyCanonicalMedia(ctx context.Context, target Querier, schema string, mediaRows map[string]canonicalMedia) (int, error) {
	total := 0
	for _, item := range mediaRows {
		if item.ReviewsUpdatedAt.IsZero() {
			item.ReviewsUpdatedAt = time.Unix(0, 0).UTC()
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		var vector any
		if item.Embedding != "" {
			vector = item.Embedding
		}
		tag, err := target.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.media
(media_type,douban_id,title,original_title,year,poster,backdrops,summary,genres,countries,directors,actors,
 duration,embedding_content,semantic_hash,embedding,rating_douban,reviews_json,reviews_updated_at,
 metadata_status,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::vector,$17,$18,$19,
        CASE WHEN $3<>'' THEN 'partial' ELSE 'empty' END,$20,$21)
ON CONFLICT (douban_id) WHERE douban_id<>'' DO UPDATE SET
media_type=CASE WHEN $22 THEN EXCLUDED.media_type ELSE media.media_type END,
title=CASE WHEN EXCLUDED.title<>'' THEN EXCLUDED.title ELSE media.title END,
original_title=CASE WHEN EXCLUDED.original_title<>'' THEN EXCLUDED.original_title ELSE media.original_title END,
year=CASE WHEN EXCLUDED.year<>'' THEN EXCLUDED.year ELSE media.year END,
poster=CASE WHEN EXCLUDED.poster<>'' THEN EXCLUDED.poster ELSE media.poster END,
backdrops=CASE WHEN EXCLUDED.backdrops<>'' THEN EXCLUDED.backdrops ELSE media.backdrops END,
summary=CASE WHEN EXCLUDED.summary<>'' THEN EXCLUDED.summary ELSE media.summary END,
genres=CASE WHEN EXCLUDED.genres<>'' THEN EXCLUDED.genres ELSE media.genres END,
countries=CASE WHEN EXCLUDED.countries<>'' THEN EXCLUDED.countries ELSE media.countries END,
directors=CASE WHEN EXCLUDED.directors<>'' THEN EXCLUDED.directors ELSE media.directors END,
actors=CASE WHEN EXCLUDED.actors<>'' THEN EXCLUDED.actors ELSE media.actors END,
duration=CASE WHEN EXCLUDED.duration<>'' THEN EXCLUDED.duration ELSE media.duration END,
embedding_content=CASE WHEN EXCLUDED.embedding_content<>'' THEN EXCLUDED.embedding_content ELSE media.embedding_content END,
semantic_hash=CASE WHEN EXCLUDED.semantic_hash<>'' THEN EXCLUDED.semantic_hash ELSE media.semantic_hash END,
embedding=COALESCE(EXCLUDED.embedding,media.embedding),
rating_douban=CASE WHEN EXCLUDED.rating_douban>0 THEN EXCLUDED.rating_douban ELSE media.rating_douban END,
reviews_json=CASE WHEN EXCLUDED.reviews_json<>'' THEN EXCLUDED.reviews_json ELSE media.reviews_json END,
reviews_updated_at=CASE WHEN EXCLUDED.reviews_json<>'' THEN EXCLUDED.reviews_updated_at ELSE media.reviews_updated_at END,
updated_at=GREATEST(media.updated_at,EXCLUDED.updated_at)`, quote(schema)),
			item.MediaType, item.DoubanID, item.Title, item.OriginalTitle, item.Year, item.Poster, item.Backdrops,
			item.Summary, item.Genres, item.Countries, item.Directors, item.Actors, item.Duration,
			item.EmbeddingContent, item.SemanticHash, vector, item.Rating, item.ReviewsJSON,
			item.ReviewsUpdatedAt, item.CreatedAt, item.UpdatedAt, item.TypeCertain)
		if err != nil {
			return total, fmt.Errorf("upsert canonical media %s: %w", item.DoubanID, err)
		}
		total += int(tag.RowsAffected())
		if item.IMDbID == "" {
			continue
		}
		tag, err = target.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.media_external_ids
(media_id,provider,external_type,external_id,is_primary,verified_at)
SELECT id,'imdb',CASE WHEN media_type='tv' THEN 'tv' ELSE 'movie' END,$2,TRUE,NOW()
FROM %s.media WHERE douban_id=$1
ON CONFLICT (provider,external_type,external_id) DO UPDATE SET
media_id=EXCLUDED.media_id,is_primary=TRUE,verified_at=NOW(),updated_at=NOW()`, quote(schema), quote(schema)), item.DoubanID, item.IMDbID)
		if err != nil {
			return total, fmt.Errorf("upsert imdb id for %s: %w", item.DoubanID, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

func loadTargetMediaIDs(ctx context.Context, target Querier, schema string) (map[string]int64, error) {
	rows, err := target.Query(ctx, fmt.Sprintf(`SELECT douban_id,id FROM %s.media WHERE douban_id<>''`, quote(schema)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]int64)
	for rows.Next() {
		var doubanID string
		var id int64
		if err := rows.Scan(&doubanID, &id); err != nil {
			return nil, err
		}
		result[doubanID] = id
	}
	return result, rows.Err()
}

func applyCanonicalResources(ctx context.Context, target Querier, schema string, resources map[string]canonicalResource) (int, error) {
	mediaIDs, err := loadTargetMediaIDs(ctx, target, schema)
	if err != nil {
		return 0, fmt.Errorf("load media ids for resources: %w", err)
	}
	total := 0
	for _, item := range resources {
		mediaID := mediaIDs[item.DoubanID]
		if mediaID == 0 {
			continue
		}
		tag, err := target.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.resource_media_links
(source_key,vod_id,media_id,confidence,matched_by,verified_at)
VALUES ($1,$2,$3,1.0000,'douban_id',NOW())
ON CONFLICT (source_key,vod_id) DO UPDATE SET
media_id=CASE WHEN resource_media_links.is_locked THEN resource_media_links.media_id ELSE EXCLUDED.media_id END,
confidence=CASE WHEN resource_media_links.is_locked THEN resource_media_links.confidence ELSE EXCLUDED.confidence END,
matched_by=CASE WHEN resource_media_links.is_locked THEN resource_media_links.matched_by ELSE EXCLUDED.matched_by END,
verified_at=NOW(),updated_at=NOW()`, quote(schema)), item.SourceKey, item.VodID, mediaID)
		if err != nil {
			return total, fmt.Errorf("link resource %s/%s: %w", item.SourceKey, item.VodID, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

func applyCanonicalHistory(ctx context.Context, target Querier, schema string, histories map[string]canonicalHistory) (int, error) {
	mediaIDs, err := loadTargetMediaIDs(ctx, target, schema)
	if err != nil {
		return 0, fmt.Errorf("load media ids for history: %w", err)
	}
	total := 0
	for _, item := range histories {
		mediaID := mediaIDs[item.DoubanID]
		arguments := []any{item.UserID, nullableInt64(mediaID), item.PositionSeconds, item.DurationSeconds,
			item.Progress, item.Completed, item.SourceKey, item.VodID, item.Title, item.Poster,
			item.Episode, item.Season, item.EpisodeKey, item.ActivityAt}
		conflictTarget := `(user_id,last_source_key,last_vod_id,season_number,episode_key)
WHERE media_unit_id IS NULL AND media_id IS NULL`
		if mediaID > 0 {
			conflictTarget = `(user_id,media_id,season_number,episode_key)
WHERE media_unit_id IS NULL AND media_id IS NOT NULL`
		}
		tag, err := target.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.playback_positions
(user_id,media_id,position_seconds,duration_seconds,progress_percent,completed,last_source_key,last_vod_id,
 title,poster,episode,season_number,episode_key,activity_at,deleted_at,created_at,updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,NULL,$14,$14)
ON CONFLICT %s DO UPDATE SET
position_seconds=EXCLUDED.position_seconds,duration_seconds=EXCLUDED.duration_seconds,
progress_percent=EXCLUDED.progress_percent,completed=EXCLUDED.completed,
last_source_key=EXCLUDED.last_source_key,last_vod_id=EXCLUDED.last_vod_id,
title=EXCLUDED.title,poster=EXCLUDED.poster,episode=EXCLUDED.episode,
activity_at=EXCLUDED.activity_at,deleted_at=NULL,
server_version=nextval('playback_position_version_seq'),updated_at=NOW()
WHERE EXCLUDED.activity_at>=playback_positions.activity_at`, quote(schema), conflictTarget), arguments...)
		if err != nil {
			return total, fmt.Errorf("upsert playback position for user %d: %w", item.UserID, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

func nullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
