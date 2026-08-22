package mediaidentity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// Store 是规范媒体的核心读写接口。
type Store interface {
	Upsert(ctx context.Context, media Media) (Media, error)
	FindByID(ctx context.Context, id int) (Media, error)
	FindByDoubanID(ctx context.Context, doubanID string) (Media, error)
	UpsertExternalID(ctx context.Context, external ExternalID) error
	LinkResource(ctx context.Context, link ResourceLink) error
	FindResourceLink(ctx context.Context, sourceKey, vodID string) (ResourceLink, error)
	WriteSourceSnapshot(ctx context.Context, mediaID int, provider string, payload []byte, success bool, errorMessage string) error
}

// ErrExternalIDConflict 表示这个外部 ID 已经绑在另一部作品上了，不允许悄悄改绑。
var ErrExternalIDConflict = errors.New("external ID is already linked to another media")

// Resolver 是播放和历史适配器使用的最小只读接口，单独定义可避免这些包依赖写入 API。
type Resolver interface {
	FindByDoubanID(ctx context.Context, doubanID string) (Media, error)
}

// SnapshotWriter 保存 Provider 的最新原始数据，让刷新状态只依赖规范媒体模型。
type SnapshotWriter interface {
	WriteSourceSnapshot(ctx context.Context, mediaID int, provider string, payload []byte, success bool, errorMessage string) error
}

// EpisodeWriter 写入资源的剧集候选。
type EpisodeWriter interface {
	UpsertEpisodes(ctx context.Context, episodes []Episode) error
}

// EpisodeInfo 是播放页使用的轻量剧集描述，不加载完整候选也能渲染选集网格。
type EpisodeInfo struct {
	SeasonNumber int
	EpisodeKey   string
	EpisodeLabel string
	SourceCount  int
}

// EpisodeReader 读取播放候选和选集列表。
type EpisodeReader interface {
	ListResourceCandidates(ctx context.Context, mediaID, seasonNumber int, episodeKey string) ([]ResourceCandidate, error)
	ListAllEpisodes(ctx context.Context, mediaID int) ([]EpisodeInfo, error)
}

// UnitEpisodeReader 按季集 ID 读取播放候选。
type UnitEpisodeReader interface {
	ListUnitResourceCandidates(ctx context.Context, mediaUnitID int) ([]ResourceCandidate, error)
}

// PlaybackEventWriter 写入播放埋点事件。
type PlaybackEventWriter interface {
	RecordPlaybackEvent(ctx context.Context, event PlaybackAttemptEvent) (bool, error)
}

// AliasWriter 写入别名。
type AliasWriter interface {
	UpsertAlias(ctx context.Context, alias Alias) error
}

// sourceField 是一次字段级合并的输入：写哪一列、写什么值、这个来源在该列上的优先级。
type sourceField struct {
	column   string
	value    any
	text     string
	priority int
}

// mergeRuleVersion 是合并规则的版本号，改优先级表时应当同步递增，便于识别历史数据。
const mergeRuleVersion = 1

// MergeSource 是规范数据的第二阶段写入路径。它为每个字段分别保留获胜来源，
// 不允许最后完成的豆瓣/TMDB 任务覆盖无关字段。空输入会被忽略；优先级相同时，
// 较新的成功刷新可以替换旧数据。
func (store *PostgresStore) MergeSource(ctx context.Context, provider string, media Media, payload []byte, externalIDs ...ExternalID) (Media, error) {
	canonical, err := store.ensureMergeBase(ctx, media)
	if err != nil {
		return Media{}, err
	}
	for _, field := range sourceFields(provider, media) {
		if field.priority <= 0 || field.text == "" {
			continue
		}
		updated, err := store.database.Exec(ctx, `UPDATE media
SET `+field.column+` = $2, metadata_version = GREATEST(metadata_version, $3),
updated_at = CASE WHEN `+field.column+` IS DISTINCT FROM $2 THEN NOW() ELSE updated_at END
WHERE id = $1 AND (
    NOT EXISTS (SELECT 1 FROM media_field_sources WHERE media_id = $1 AND field_name = $4)
    OR $5 >= (SELECT priority FROM media_field_sources WHERE media_id = $1 AND field_name = $4)
)`, canonical.ID, field.value, maxInt(canonical.MetadataVersion, 2), field.column, field.priority)
		if err != nil {
			return Media{}, fmt.Errorf("merge media field %s: %w", field.column, err)
		}
		if updated == 0 {
			continue
		}
		hash := sha256.Sum256([]byte(field.text))
		if _, err := store.database.Exec(ctx, `INSERT INTO media_field_sources
(media_id, field_name, provider, priority, value_hash, merge_rule_version, observed_at)
VALUES ($1,$2,$3,$4,$5,$6,NOW())
ON CONFLICT (media_id, field_name) DO UPDATE SET provider = EXCLUDED.provider,
priority = EXCLUDED.priority, value_hash = EXCLUDED.value_hash,
merge_rule_version = EXCLUDED.merge_rule_version, observed_at = EXCLUDED.observed_at
WHERE EXCLUDED.priority >= media_field_sources.priority`, canonical.ID, field.column, provider, field.priority, hex.EncodeToString(hash[:]), mergeRuleVersion); err != nil {
			return Media{}, fmt.Errorf("record media field source %s: %w", field.column, err)
		}
	}
	for _, external := range externalIDs {
		if external.ExternalID == "" {
			continue
		}
		external.MediaID = canonical.ID
		if external.ExternalType == "" {
			external.ExternalType = media.MediaType
		}
		if err := store.UpsertExternalID(ctx, external); err != nil {
			return Media{}, err
		}
	}
	for _, alias := range []Alias{
		{MediaID: canonical.ID, Alias: media.Title, Source: provider, AliasType: "title"},
		{MediaID: canonical.ID, Alias: media.OriginalTitle, Source: provider, AliasType: "original_title"},
	} {
		if strings.TrimSpace(alias.Alias) != "" {
			if err := store.UpsertAlias(ctx, alias); err != nil {
				return Media{}, err
			}
		}
	}
	if normalizeMediaType(media.MediaType) == "movie" {
		if _, err := store.EnsureMediaUnit(ctx, MediaUnit{
			MediaID: canonical.ID, UnitType: "feature", EpisodeKey: "feature", Title: media.Title,
		}); err != nil {
			return Media{}, err
		}
	}
	if err := store.updateRefreshState(ctx, canonical.ID); err != nil {
		return Media{}, err
	}
	if err := store.WriteSourceSnapshot(ctx, canonical.ID, provider, payload, true, ""); err != nil {
		return Media{}, err
	}
	return store.FindByID(ctx, canonical.ID)
}

// sourceFields 是字段级来源优先级表，也是整个合并机制的核心：
// 谁在哪个字段上更权威，就由这张表说了算（数字越大越权威）。
// 例如片名信豆瓣（100 > TMDB 的 50），原名和剧照信 TMDB，人工修改一律 1000 压过所有来源。
func sourceFields(provider string, media Media) []sourceField {
	provider = strings.ToLower(strings.TrimSpace(provider))
	priorities := map[string]map[string]int{
		"douban": {"media_type": 100, "title": 100, "original_title": 70, "year": 80, "poster": 100, "summary": 100, "genres": 100, "countries": 100, "directors": 100, "actors": 100, "duration": 80, "rating_douban": 100},
		"tmdb":   {"media_type": 100, "title": 50, "original_title": 100, "year": 90, "poster": 60, "backdrops": 100, "summary": 70, "genres": 70, "countries": 60, "duration": 100, "rating_tmdb": 100, "vote_count_tmdb": 100, "series_status": 100},
		"manual": {"media_type": 1000, "title": 1000, "original_title": 1000, "year": 1000, "poster": 1000, "backdrops": 1000, "summary": 1000, "genres": 1000, "countries": 1000, "directors": 1000, "actors": 1000, "duration": 1000, "rating_douban": 1000, "rating_tmdb": 1000, "vote_count_tmdb": 1000, "series_status": 1000},
	}
	pick := func(column string, value any, text string) sourceField {
		return sourceField{column: column, value: value, text: text, priority: priorities[provider][column]}
	}
	mediaType := ""
	if strings.TrimSpace(media.MediaType) != "" {
		mediaType = normalizeMediaType(media.MediaType)
	}
	ratingDoubanText := ""
	if media.RatingDouban > 0 {
		ratingDoubanText = fmt.Sprintf("%.4f", media.RatingDouban)
	}
	ratingTMDBText := ""
	if media.RatingTMDB > 0 {
		ratingTMDBText = fmt.Sprintf("%.4f", media.RatingTMDB)
	}
	voteCountTMDBText := ""
	if media.VoteCountTMDB > 0 {
		voteCountTMDBText = fmt.Sprintf("%d", media.VoteCountTMDB)
	}
	return []sourceField{
		pick("media_type", mediaType, mediaType),
		pick("title", media.Title, media.Title), pick("original_title", media.OriginalTitle, media.OriginalTitle),
		pick("year", media.Year, media.Year), pick("poster", media.Poster, media.Poster),
		pick("backdrops", media.Backdrops, media.Backdrops), pick("summary", media.Summary, media.Summary),
		pick("genres", media.Genres, media.Genres), pick("countries", media.Countries, media.Countries),
		pick("directors", media.Directors, media.Directors), pick("actors", media.Actors, media.Actors),
		pick("duration", media.Duration, media.Duration), pick("rating_douban", media.RatingDouban, ratingDoubanText),
		pick("rating_tmdb", media.RatingTMDB, ratingTMDBText), pick("vote_count_tmdb", media.VoteCountTMDB, voteCountTMDBText),
		pick("series_status", media.SeriesStatus, media.SeriesStatus),
	}
}

// normalizeMediaType 把各种类型写法收敛成 movie / tv 两类，不认识的按电影处理。
func normalizeMediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "tv", "series", "season", "show", "animation", "cartoon":
		return "tv"
	default:
		return "movie"
	}
}

// maxInt 取较大值。
func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

// nullableMediaID 把 0 转成 NULL 写库。
func nullableMediaID(id int) any {
	if id <= 0 {
		return nil
	}
	return id
}

// PostgresStore 是 mediaidentity 全部存储接口的唯一实现。
type PostgresStore struct{ database database.Executor }

// NewPostgresStore 创建存储实现。
func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

// ensureMergeBase 在不替换任何既有 Provider 数据的前提下创建规范行。
// MergeSource 会在之后应用字段级优先级；若此处使用普通 Upsert，低优先级刷新可能在
// 优先级判断之前就覆盖整行。
func (store *PostgresStore) ensureMergeBase(ctx context.Context, media Media) (Media, error) {
	if strings.TrimSpace(media.DoubanID) == "" {
		return store.Upsert(ctx, media)
	}
	prepareMedia(&media)
	inserted, err := store.database.Exec(ctx, `INSERT INTO media (
media_type, douban_id, title, original_title, year, poster, backdrops, summary, genres,
countries, directors, actors, duration, rating_douban, rating_tmdb,
vote_count_tmdb, series_status, metadata_version, metadata_status, last_metadata_sync_at,
updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$21,$17,$18,$19,$20)
ON CONFLICT (douban_id) WHERE douban_id <> '' DO NOTHING`, media.MediaType, media.DoubanID,
		media.Title, media.OriginalTitle, media.Year, media.Poster, media.Backdrops, media.Summary,
		media.Genres, media.Countries, media.Directors, media.Actors, media.Duration,
		media.RatingDouban, media.RatingTMDB, media.VoteCountTMDB, media.MetadataVersion,
		media.MetadataStatus, nullableTime(media.LastMetadataSyncAt), media.UpdatedAt,
		media.SeriesStatus)
	if err != nil {
		return Media{}, fmt.Errorf("ensure merge media: %w", err)
	}
	canonical, err := store.FindByDoubanID(ctx, media.DoubanID)
	if err != nil {
		return Media{}, fmt.Errorf("find merge media: %w", err)
	}
	if inserted == 0 {
		if err := store.seedExistingFieldSources(ctx, canonical); err != nil {
			return Media{}, err
		}
	}
	return canonical, nil
}

// prepareMedia 补齐写库前的默认值。
func prepareMedia(media *Media) {
	if media.MediaType == "" {
		media.MediaType = "movie"
	}
	if media.MetadataVersion == 0 {
		media.MetadataVersion = 1
	}
	if media.MetadataStatus == "" {
		media.MetadataStatus = "partial"
	}
	if media.UpdatedAt.IsZero() {
		media.UpdatedAt = time.Now()
	}
}

// 已有 media 行可能早于字段来源记录。这里仅一次性把现有字段视为豆瓣基线，
// 避免第一次 TMDB 刷新仅因缺少来源记录就替换稳定值。
func (store *PostgresStore) seedExistingFieldSources(ctx context.Context, media Media) error {
	for _, field := range sourceFields("douban", media) {
		if field.priority <= 0 || field.text == "" {
			continue
		}
		hash := sha256.Sum256([]byte(field.text))
		if _, err := store.database.Exec(ctx, `INSERT INTO media_field_sources
(media_id, field_name, provider, priority, value_hash, merge_rule_version, observed_at)
VALUES ($1,$2,'douban',$3,$4,$5,COALESCE($6,NOW()))
ON CONFLICT (media_id, field_name) DO NOTHING`, media.ID, field.column, field.priority,
			hex.EncodeToString(hash[:]), mergeRuleVersion, nullableTime(media.UpdatedAt)); err != nil {
			return fmt.Errorf("seed existing media field %s: %w", field.column, err)
		}
	}
	return nil
}

// Upsert 是第一阶段的写入路径：只填空，不覆盖已有的非空值（评分和票数取更优的那个）。
// 需要按来源优先级覆盖时用 MergeSource。
func (store *PostgresStore) Upsert(ctx context.Context, media Media) (Media, error) {
	prepareMedia(&media)
	row := store.database.QueryRow(ctx, `INSERT INTO media (
media_type, douban_id, title, original_title, year, poster, backdrops, summary, genres,
countries, directors, actors, duration, rating_douban, rating_tmdb,
vote_count_tmdb, series_status, metadata_version, metadata_status, last_metadata_sync_at,
updated_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$21,$17,$18,$19,$20)
ON CONFLICT (douban_id) WHERE douban_id <> '' DO UPDATE SET
media_type = COALESCE(NULLIF(media.media_type, ''), EXCLUDED.media_type),
title = COALESCE(NULLIF(media.title, ''), EXCLUDED.title),
original_title = COALESCE(NULLIF(media.original_title, ''), EXCLUDED.original_title),
year = COALESCE(NULLIF(media.year, ''), EXCLUDED.year),
poster = COALESCE(NULLIF(media.poster, ''), EXCLUDED.poster),
backdrops = COALESCE(NULLIF(media.backdrops, ''), EXCLUDED.backdrops),
summary = COALESCE(NULLIF(media.summary, ''), EXCLUDED.summary),
genres = COALESCE(NULLIF(media.genres, ''), EXCLUDED.genres),
countries = COALESCE(NULLIF(media.countries, ''), EXCLUDED.countries),
directors = COALESCE(NULLIF(media.directors, ''), EXCLUDED.directors),
actors = COALESCE(NULLIF(media.actors, ''), EXCLUDED.actors),
duration = COALESCE(NULLIF(media.duration, ''), EXCLUDED.duration),
rating_douban = CASE WHEN EXCLUDED.rating_douban > 0 THEN EXCLUDED.rating_douban ELSE media.rating_douban END,
rating_tmdb = CASE WHEN EXCLUDED.rating_tmdb > 0 THEN EXCLUDED.rating_tmdb ELSE media.rating_tmdb END,
vote_count_tmdb = GREATEST(media.vote_count_tmdb, EXCLUDED.vote_count_tmdb),
series_status = CASE WHEN EXCLUDED.series_status <> '' THEN EXCLUDED.series_status ELSE media.series_status END,
metadata_version = GREATEST(media.metadata_version, EXCLUDED.metadata_version),
metadata_status = CASE WHEN EXCLUDED.metadata_status <> 'partial' THEN EXCLUDED.metadata_status ELSE media.metadata_status END,
last_metadata_sync_at = COALESCE(EXCLUDED.last_metadata_sync_at, media.last_metadata_sync_at),
updated_at = GREATEST(media.updated_at, EXCLUDED.updated_at)
RETURNING id, media_type, douban_id, title, original_title, year, poster, backdrops, summary,
genres, countries, directors, actors, duration, rating_douban, rating_tmdb,
vote_count_tmdb, series_status, metadata_version, metadata_status, last_metadata_sync_at,
created_at, updated_at`, media.MediaType, media.DoubanID, media.Title, media.OriginalTitle,
		media.Year, media.Poster, media.Backdrops, media.Summary, media.Genres, media.Countries, media.Directors,
		media.Actors, media.Duration, media.RatingDouban, media.RatingTMDB, media.VoteCountTMDB,
		media.MetadataVersion, media.MetadataStatus, nullableTime(media.LastMetadataSyncAt), media.UpdatedAt,
		media.SeriesStatus)
	if err := scanMedia(row, &media); err != nil {
		return Media{}, fmt.Errorf("upsert media: %w", err)
	}
	return media, nil
}

// FindByID 按主键取媒体。
func (store *PostgresStore) FindByID(ctx context.Context, id int) (Media, error) {
	row := store.database.QueryRow(ctx, mediaSelect+` WHERE id = $1`, id)
	var media Media
	if err := scanMedia(row, &media); err != nil {
		return Media{}, fmt.Errorf("find media %d: %w", id, err)
	}
	return media, nil
}

// FindByDoubanID 按豆瓣 ID 取媒体。
func (store *PostgresStore) FindByDoubanID(ctx context.Context, doubanID string) (Media, error) {
	row := store.database.QueryRow(ctx, mediaSelect+` WHERE douban_id = $1`, doubanID)
	var media Media
	if err := scanMedia(row, &media); err != nil {
		return Media{}, fmt.Errorf("find media douban %s: %w", doubanID, err)
	}
	return media, nil
}

// FindByTitleYear 按「标题 + 年份」取媒体，标题会同时在正名和别名里找。
func (store *PostgresStore) FindByTitleYear(ctx context.Context, title, year string) (Media, error) {
	row := store.database.QueryRow(ctx, mediaSelect+` WHERE year = $2 AND (
LOWER(title) = LOWER($1) OR id IN (
    SELECT media_id FROM media_aliases WHERE normalized_alias = $3 OR LOWER(alias) = LOWER($1)
)) ORDER BY updated_at DESC LIMIT 1`, strings.TrimSpace(title), strings.TrimSpace(year), NormalizeTitle(title))
	var media Media
	if err := scanMedia(row, &media); err != nil {
		return Media{}, fmt.Errorf("find media title/year %s/%s: %w", title, year, err)
	}
	return media, nil
}

// UpsertAlias 写入别名，归一化后的键 (media_id, normalized_alias) 唯一。
func (store *PostgresStore) UpsertAlias(ctx context.Context, alias Alias) error {
	alias.Alias = strings.TrimSpace(alias.Alias)
	alias.Source = strings.ToLower(strings.TrimSpace(alias.Source))
	alias.AliasType = strings.ToLower(strings.TrimSpace(alias.AliasType))
	alias.Language = strings.TrimSpace(alias.Language)
	if alias.NormalizedAlias == "" {
		alias.NormalizedAlias = NormalizeTitle(alias.Alias)
	} else {
		alias.NormalizedAlias = NormalizeTitle(alias.NormalizedAlias)
	}
	if alias.MediaID <= 0 || alias.Alias == "" || alias.NormalizedAlias == "" {
		return fmt.Errorf("invalid media alias")
	}
	if alias.Source == "" {
		alias.Source = "manual"
	}
	if alias.AliasType == "" {
		alias.AliasType = "alias"
	}
	_, err := store.database.Exec(ctx, `INSERT INTO media_aliases
(media_id, alias, normalized_alias, language, source, alias_type)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (media_id, normalized_alias) DO UPDATE SET
alias = EXCLUDED.alias, language = EXCLUDED.language, source = EXCLUDED.source,
alias_type = EXCLUDED.alias_type, updated_at = NOW()`,
		alias.MediaID, alias.Alias, alias.NormalizedAlias, alias.Language, alias.Source, alias.AliasType)
	if err != nil {
		return fmt.Errorf("upsert media alias: %w", err)
	}
	return nil
}

// EnsureMediaUnit 确保某一集（或电影正片）在 media_units 里存在，返回它的 ID。
func (store *PostgresStore) EnsureMediaUnit(ctx context.Context, unit MediaUnit) (MediaUnit, error) {
	unit.UnitType = strings.ToLower(strings.TrimSpace(unit.UnitType))
	unit.EpisodeKey = strings.ToUpper(strings.TrimSpace(unit.EpisodeKey))
	unit.Title = strings.TrimSpace(unit.Title)
	if unit.MediaID <= 0 {
		return MediaUnit{}, fmt.Errorf("invalid media unit media_id")
	}
	switch unit.UnitType {
	case "feature":
		unit.SeasonNumber, unit.EpisodeNumber, unit.EpisodeKey = 0, 0, "feature"
	case "episode", "special", "trailer":
		if unit.SeasonNumber < 0 || unit.EpisodeKey == "" {
			return MediaUnit{}, fmt.Errorf("invalid %s media unit identity", unit.UnitType)
		}
		if unit.UnitType == "episode" && unit.SeasonNumber < 1 {
			unit.SeasonNumber = 1
		}
		if unit.EpisodeNumber <= 0 {
			unit.EpisodeNumber = episodeNumberFromKey(unit.EpisodeKey)
		}
	default:
		return MediaUnit{}, fmt.Errorf("invalid media unit type %q", unit.UnitType)
	}
	row := store.database.QueryRow(ctx, `INSERT INTO media_units
(media_id, unit_type, season_number, episode_number, absolute_number, episode_key, title, air_date, runtime_minutes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (media_id, unit_type, season_number, episode_key) DO UPDATE SET
episode_number = COALESCE(EXCLUDED.episode_number, media_units.episode_number),
absolute_number = COALESCE(EXCLUDED.absolute_number, media_units.absolute_number),
title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE media_units.title END,
air_date = COALESCE(EXCLUDED.air_date, media_units.air_date),
runtime_minutes = COALESCE(EXCLUDED.runtime_minutes, media_units.runtime_minutes),
updated_at = NOW()
RETURNING id`, unit.MediaID, unit.UnitType, unit.SeasonNumber, nullableUnitInt(unit.EpisodeNumber),
		nullableUnitInt(unit.AbsoluteNumber), unit.EpisodeKey, unit.Title, nullableTime(unit.AirDate), nullableUnitInt(unit.RuntimeMinutes))
	if err := row.Scan(&unit.ID); err != nil {
		return MediaUnit{}, fmt.Errorf("ensure media unit: %w", err)
	}
	return unit, nil
}

// FindByExternalID 根据 Provider ID 反查规范媒体。热门数据源使用它来避免：
// 当作品尚未接入新数据库时，纯 TMDB 排名生成无法访问的 `/movie` 链接。
func (store *PostgresStore) FindByExternalID(ctx context.Context, provider, externalType, externalID string) (Media, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	externalType = normalizeExternalType(provider, externalType, "")
	externalID = strings.TrimSpace(externalID)
	row := store.database.QueryRow(ctx, mediaSelect+` m JOIN media_external_ids x ON x.media_id = m.id
WHERE x.provider = $1 AND x.external_type = $2 AND x.external_id = $3 LIMIT 1`, provider, externalType, externalID)
	var media Media
	if err := scanMedia(row, &media); err != nil {
		return Media{}, fmt.Errorf("find media external id %s/%s/%s: %w", provider, externalType, externalID, err)
	}
	return media, nil
}

// UpsertExternalID 写入外部 ID 映射。已经绑在别的媒体上时返回 ErrExternalIDConflict，不做改绑。
func (store *PostgresStore) UpsertExternalID(ctx context.Context, external ExternalID) error {
	external.Provider = strings.ToLower(strings.TrimSpace(external.Provider))
	external.ExternalType = normalizeExternalType(external.Provider, external.ExternalType, "")
	external.ExternalID = strings.TrimSpace(external.ExternalID)
	if external.MediaID <= 0 || external.Provider == "" || external.ExternalID == "" {
		return fmt.Errorf("invalid media external id")
	}
	if external.Confidence <= 0 {
		external.Confidence = 1
	}
	updated, err := store.database.Exec(ctx, `INSERT INTO media_external_ids (media_id, provider, external_type, external_id, confidence, is_primary, verified_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (provider, external_type, external_id) DO UPDATE SET media_id = EXCLUDED.media_id, confidence = EXCLUDED.confidence,
is_primary = EXCLUDED.is_primary, verified_at = EXCLUDED.verified_at, updated_at = NOW()
WHERE media_external_ids.media_id = EXCLUDED.media_id`,
		external.MediaID, external.Provider, external.ExternalType, external.ExternalID, external.Confidence, external.IsPrimary, nullableTime(external.VerifiedAt))
	if err != nil {
		return fmt.Errorf("upsert media external id: %w", err)
	}
	if updated == 0 {
		return fmt.Errorf("%w: %s/%s/%s", ErrExternalIDConflict, external.Provider, external.ExternalType, external.ExternalID)
	}
	return nil
}

// normalizeExternalType 归一外部 ID 的命名空间；tv_season_N 这种分季命名空间原样保留。
func normalizeExternalType(provider, externalType, mediaType string) string {
	value := strings.ToLower(strings.TrimSpace(externalType))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(mediaType))
	}
	if strings.HasPrefix(value, "tv_season_") {
		return value
	}
	if strings.EqualFold(strings.TrimSpace(provider), "tmdb") {
		if value == "movie" || value == "feature" {
			return "movie"
		}
		return "tv"
	}
	switch value {
	case "tv", "series", "season", "show", "animation":
		return value
	case "movie", "feature":
		return "movie"
	default:
		// Provider 没有返回明确类型时按电影处理，避免生成无法查询的空命名空间。
		return "movie"
	}
}

// mergedMetadataState 是合并完成后的资料快照，用来算内容哈希和完整度评分。
type mergedMetadataState struct {
	MediaType     string  `json:"media_type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          string  `json:"year"`
	Poster        string  `json:"poster"`
	Backdrops     string  `json:"backdrops"`
	Summary       string  `json:"summary"`
	Genres        string  `json:"genres"`
	Countries     string  `json:"countries"`
	Directors     string  `json:"directors"`
	Actors        string  `json:"actors"`
	Duration      string  `json:"duration"`
	RatingDouban  float64 `json:"rating_douban"`
	RatingTMDB    float64 `json:"rating_tmdb"`
	VoteCountTMDB int     `json:"vote_count_tmdb"`
	ExternalIDKey string  `json:"external_ids"`
	ExternalIDs   int     `json:"external_id_count"`
	MediaUnitKey  string  `json:"media_units"`
	MediaUnits    int     `json:"media_unit_count"`
}

// updateRefreshState 每次合并后重算：内容有没有变、完整度多少分、下次什么时候再刷。
// 内容连续多次没变就逐步拉长间隔，今年/去年的新片则强制不超过 3 天/7 天。
func (store *PostgresStore) updateRefreshState(ctx context.Context, mediaID int) error {
	var state mergedMetadataState
	var previousHash string
	var unchangedCount int
	if err := store.database.QueryRow(ctx, `SELECT media_type, title, original_title, year, poster, backdrops,
summary, genres, countries, directors, actors, duration, rating_douban, rating_tmdb,
vote_count_tmdb,
(SELECT COALESCE(string_agg(provider || ':' || external_type || ':' || external_id, '|' ORDER BY provider, external_type, external_id), '') FROM media_external_ids WHERE media_id = media.id),
(SELECT COUNT(*) FROM media_external_ids WHERE media_id = media.id),
(SELECT COALESCE(string_agg(unit_type || ':' || season_number::text || ':' || episode_key, '|' ORDER BY unit_type, season_number, episode_key), '') FROM media_units WHERE media_id = media.id),
(SELECT COUNT(*) FROM media_units WHERE media_id = media.id),
content_hash, unchanged_refresh_count FROM media WHERE id = $1`, mediaID).Scan(
		&state.MediaType, &state.Title, &state.OriginalTitle, &state.Year, &state.Poster, &state.Backdrops,
		&state.Summary, &state.Genres, &state.Countries, &state.Directors, &state.Actors, &state.Duration,
		&state.RatingDouban, &state.RatingTMDB, &state.VoteCountTMDB, &state.ExternalIDKey, &state.ExternalIDs,
		&state.MediaUnitKey, &state.MediaUnits,
		&previousHash, &unchangedCount,
	); err != nil {
		return fmt.Errorf("read merged metadata state: %w", err)
	}
	contentHash := stableJSONHash(state)
	semanticHash := stableJSONHash(struct {
		Title, OriginalTitle, Year, Summary, Genres, Countries, Directors, Actors string
	}{state.Title, state.OriginalTitle, state.Year, state.Summary, state.Genres, state.Countries, state.Directors, state.Actors})
	changed := previousHash == "" || previousHash != contentHash
	if changed {
		unchangedCount = 0
	} else {
		unchangedCount++
	}
	completeness := metadataCompleteness(state)
	delay := metadataRefreshDelay(unchangedCount, completeness)
	// 新上映内容需要更频繁刷新，才能及时获得更新后的剧集、纠正资料和新增剧照/评分。
	currentYear := time.Now().Year()
	if yearInt := parseMediaYear(state.Year); yearInt >= currentYear {
		if delay > 3*24*time.Hour {
			delay = 3 * 24 * time.Hour
		}
	} else if yearInt == currentYear-1 {
		if delay > 7*24*time.Hour {
			delay = 7 * 24 * time.Hour
		}
	}
	nextRefresh := time.Now().Add(delay)
	_, err := store.database.Exec(ctx, `UPDATE media SET
metadata_status = CASE WHEN title <> '' AND (summary <> '' OR original_title <> '') THEN 'ready' ELSE 'partial' END,
content_hash = $2, semantic_hash = $3, completeness_score = $4,
merge_rule_version = $5, unchanged_refresh_count = $6, next_refresh_at = $7,
last_content_change_at = CASE WHEN $8 THEN NOW() ELSE last_content_change_at END,
last_metadata_sync_at = NOW(), updated_at = CASE WHEN $8 THEN NOW() ELSE updated_at END
WHERE id = $1`, mediaID, contentHash,
		semanticHash, completeness, mergeRuleVersion, unchangedCount, nextRefresh, changed)
	if err != nil {
		return fmt.Errorf("update media refresh state: %w", err)
	}
	return nil
}

// stableJSONHash 对结构体取稳定哈希（字段顺序固定，所以 JSON 序列化结果稳定）。
func stableJSONHash(value any) string {
	encoded, _ := json.Marshal(value)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:])
}

// metadataRefreshDelay 退避表：连续没变化的次数越多，下次刷新隔得越久（1 天 → 3 → 7 → 14 → 30 → 90 天）。
func metadataRefreshDelay(unchangedCount, completeness int) time.Duration {
	var delay time.Duration
	switch unchangedCount {
	case 0:
		delay = 24 * time.Hour
	case 1:
		delay = 3 * 24 * time.Hour
	case 2:
		delay = 7 * 24 * time.Hour
	case 3:
		delay = 14 * 24 * time.Hour
	case 4:
		delay = 30 * 24 * time.Hour
	default:
		delay = 90 * 24 * time.Hour
	}
	// 资料没凑齐时值得比正常节奏更勤地重试，但 unchangedCount 已经说明连续几次抓回来的
	// 都是同一份数据——上游就这么多，再天天抓也变不出新字段（比如豆瓣本来就没有简介/片长，
	// 或者 TMDB 映射一直没解析出来拿不到那 8 分）。所以加急只给前几次，之后退回正常退避；
	// 不留这个出口的话，永远达不到 70 分的条目会每天重新入队，永不停止。
	if completeness < 70 && unchangedCount < 3 && delay > 24*time.Hour {
		delay = 24 * time.Hour
	}
	return delay
}

// metadataCompleteness 给资料完整度打分（满分 100）：标题 15 分，海报/简介各 10 分，
// 有两个以上外部 ID 加 8 分，有季集数据再加 8 分。低于 70 分算不完整。
func metadataCompleteness(state mergedMetadataState) int {
	score := 0
	for _, field := range []struct {
		value  string
		weight int
	}{
		{state.Title, 15}, {state.Year, 8}, {state.MediaType, 7}, {state.Poster, 10},
		{state.Summary, 10}, {state.Genres, 8}, {state.Countries, 5}, {state.Directors, 8},
		{state.Actors, 8}, {state.Duration, 5},
	} {
		if metadataValuePresent(field.value) {
			score += field.weight
		}
	}
	if state.ExternalIDs >= 2 {
		score += 8
	}
	if state.MediaUnits > 0 {
		score += 8
	}
	return score
}

// parseMediaYear 取年份的前四位数字。
func parseMediaYear(year string) int {
	year = strings.TrimSpace(year)
	if len(year) < 4 {
		return 0
	}
	y := 0
	for _, c := range year[:4] {
		if c < '0' || c > '9' {
			return 0
		}
		y = y*10 + int(c-'0')
	}
	return y
}

// metadataValuePresent 判断字段是否真有内容（空 JSON 数组/对象不算）。
func metadataValuePresent(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "[]", "{}", "null":
		return false
	default:
		return true
	}
}

// WriteSourceSnapshot 记录某个来源的最近一次抓取结果，并按内容是否变化调整该来源的下次抓取时间。
func (store *PostgresStore) WriteSourceSnapshot(ctx context.Context, mediaID int, provider string, payload []byte, success bool, errorMessage string) error {
	if mediaID <= 0 || provider == "" {
		return fmt.Errorf("invalid source snapshot identity")
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}
	if !json.Valid(payload) {
		return fmt.Errorf("source snapshot payload is not valid JSON")
	}
	// 只有 payload_hash 有用：unchanged_count 靠它比对，而 hash 是在这里算好的。
	// payload_json 本身全代码库没有任何 SELECT 读回去，却要为每个 media × 每个 provider
	// 存一份完整的上游 JSON。这里改存空对象，列先留着——UPSERT 会让存量行在下次刷新时
	// 自然缩小，不需要全表 UPDATE，也避免滚动发布期间旧进程写已删除的列。
	// ponytail: payload_json 列还在，确认所有实例都升级后再单独发一个迁移 DROP COLUMN。
	//
	// 这里原本还算一条 3/7/14/30/90 天的 next_refresh_at 阶梯，但全代码库没有任何地方
	// 读它——真正在调度的是 media.next_refresh_at（catalog/refresh.go 读的那条）。
	// 两条阶梯并存只会让人以为刷新节奏是这条定的。写入和它的索引已随迁移 0045 删除。
	hash := sha256.Sum256(payload)
	_, err := store.database.Exec(ctx, `INSERT INTO media_source_snapshots
(media_id, provider, payload_json, payload_hash, fetched_at, last_success_at, error_message,
 unchanged_count, changed_at)
VALUES ($1,$2,$3::jsonb,$4,NOW(),CASE WHEN $5 THEN NOW() ELSE NULL END,$6,0,
 CASE WHEN $5 THEN NOW() ELSE NULL END)
ON CONFLICT (media_id, provider) DO UPDATE SET
payload_json = CASE WHEN $5 THEN EXCLUDED.payload_json ELSE media_source_snapshots.payload_json END,
payload_hash = CASE WHEN $5 THEN EXCLUDED.payload_hash ELSE media_source_snapshots.payload_hash END,
fetched_at = EXCLUDED.fetched_at,
last_success_at = CASE WHEN $5 THEN EXCLUDED.last_success_at ELSE media_source_snapshots.last_success_at END,
error_message = EXCLUDED.error_message,
unchanged_count = CASE
    WHEN NOT $5 THEN media_source_snapshots.unchanged_count
    WHEN media_source_snapshots.payload_hash = EXCLUDED.payload_hash THEN media_source_snapshots.unchanged_count + 1
    ELSE 0 END,
changed_at = CASE
    WHEN $5 AND media_source_snapshots.payload_hash <> EXCLUDED.payload_hash THEN NOW()
    ELSE media_source_snapshots.changed_at END`, mediaID, provider, "{}", hex.EncodeToString(hash[:]), success, errorMessage)
	if err != nil {
		return fmt.Errorf("write source snapshot: %w", err)
	}
	return nil
}

// LinkResource 建立「资源 → 媒体」的关联，并把该资源下所有剧集候选的 media_id 一起补上。
// 已锁定（人工确认过）的关联不会被自动匹配改掉。
func (store *PostgresStore) LinkResource(ctx context.Context, link ResourceLink) error {
	if link.Confidence <= 0 {
		link.Confidence = 1
	}
	_, err := store.database.Exec(ctx, `INSERT INTO resource_media_links
(source_key, vod_id, media_id, confidence, matched_by, is_locked, verified_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (source_key, vod_id) DO UPDATE SET
media_id = CASE WHEN resource_media_links.is_locked THEN resource_media_links.media_id ELSE EXCLUDED.media_id END,
confidence = CASE WHEN resource_media_links.is_locked THEN resource_media_links.confidence ELSE EXCLUDED.confidence END,
matched_by = CASE WHEN resource_media_links.is_locked THEN resource_media_links.matched_by ELSE EXCLUDED.matched_by END,
verified_at = COALESCE(EXCLUDED.verified_at, resource_media_links.verified_at), updated_at = NOW()`,
		link.SourceKey, link.VodID, link.MediaID, link.Confidence, link.MatchedBy, link.IsLocked, nullableTime(link.VerifiedAt))
	if err != nil {
		return fmt.Errorf("link resource media: %w", err)
	}
	if link.MediaID > 0 {
		if _, err := store.database.Exec(ctx, `UPDATE resource_episode_candidates candidate
SET media_id = $3, updated_at = NOW()
FROM resource_play_lines line
WHERE candidate.line_id = line.id AND line.source_key = $1 AND line.vod_id = $2`, link.SourceKey, link.VodID, link.MediaID); err != nil {
			return fmt.Errorf("bind structured resource candidates: %w", err)
		}
	}
	return nil
}

// RecordMatchCandidate 记录一条待复核候选（理由只有方法名和置信度）。
func (store *PostgresStore) RecordMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error {
	reason, err := json.Marshal(map[string]any{"match_method": matchedBy, "confidence": confidence})
	if err != nil {
		return fmt.Errorf("encode resource match reason: %w", err)
	}
	return store.RecordDetailedMatchCandidate(ctx, sourceKey, vodID, mediaID, confidence, matchedBy, "review", string(reason))
}

// RecordDetailedMatchCandidate 记录带完整打分理由的待复核候选。
// 已经人工判过（verified/rejected）的候选不会被自动写入改回 review。
func (store *PostgresStore) RecordDetailedMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy, status, reasonJSON string) error {
	sourceKey, vodID = strings.TrimSpace(sourceKey), strings.TrimSpace(vodID)
	matchedBy = strings.ToLower(strings.TrimSpace(matchedBy))
	status = strings.ToLower(strings.TrimSpace(status))
	if sourceKey == "" || vodID == "" || mediaID <= 0 || confidence < 0 || confidence > 1 || matchedBy == "" ||
		(status != "review" && status != "rejected") || !json.Valid([]byte(reasonJSON)) {
		return fmt.Errorf("invalid resource match candidate")
	}
	_, err := store.database.Exec(ctx, `INSERT INTO resource_match_candidates
(source_key, vod_id, media_id, confidence, match_method, status, reason)
VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb)
ON CONFLICT (source_key, vod_id, media_id) DO UPDATE SET
confidence = EXCLUDED.confidence, match_method = EXCLUDED.match_method,
status = CASE WHEN resource_match_candidates.status IN ('verified', 'rejected')
    THEN resource_match_candidates.status ELSE EXCLUDED.status END,
reason = EXCLUDED.reason, updated_at = NOW()`, sourceKey, vodID, mediaID, confidence, matchedBy, status, reasonJSON)
	if err != nil {
		return fmt.Errorf("record resource match candidate: %w", err)
	}
	return nil
}

// UpsertEpisodes 写入资源的播放线路和分集候选：
// 先按需建 media_units，再写 resource_play_lines，最后写 resource_episode_candidates。
func (store *PostgresStore) UpsertEpisodes(ctx context.Context, episodes []Episode) error {
	for _, episode := range episodes {
		if episode.SourceKey == "" || episode.VodID == "" || episode.EpisodeKey == "" || episode.PlayURL == "" {
			continue
		}
		season := episode.SeasonNumber
		if season < 1 {
			season = 1
		}
		status := episode.ResourceStatus
		if status == "" {
			status = "active"
		}
		if episode.MediaID > 0 {
			unitType := strings.ToLower(strings.TrimSpace(episode.UnitType))
			if unitType == "" {
				unitType = "episode"
			}
			unit, err := store.EnsureMediaUnit(ctx, MediaUnit{MediaID: episode.MediaID, UnitType: unitType,
				SeasonNumber: season, EpisodeKey: episode.EpisodeKey, Title: episode.EpisodeLabel})
			if err != nil {
				return fmt.Errorf("ensure resource media unit %s/%s/%s: %w", episode.SourceKey, episode.VodID, episode.EpisodeKey, err)
			}
			episode.MediaUnitID = unit.ID
		}
		lineKey := strings.ToLower(strings.TrimSpace(episode.LineKey))
		if lineKey == "" {
			if episode.LineOrder == 0 {
				lineKey = "default"
			} else {
				lineKey = fmt.Sprintf("line-%02d", episode.LineOrder+1)
			}
		}
		lineLabel := strings.TrimSpace(episode.LineLabel)
		if lineLabel == "" {
			if episode.LineOrder == 0 {
				lineLabel = "默认源"
			} else {
				lineLabel = fmt.Sprintf("备用源 %c", 'A'+episode.LineOrder)
			}
		}

		var lineID int
		if err := store.database.QueryRow(ctx, `INSERT INTO resource_play_lines
(source_key, vod_id, line_key, line_label, sort_order, resource_status, last_seen_at, updated_at)
VALUES ($1,$2,$3,$4,$5,'active',COALESCE($6,NOW()),NOW())
ON CONFLICT (source_key, vod_id, line_key) DO UPDATE SET
line_label = EXCLUDED.line_label, sort_order = EXCLUDED.sort_order,
resource_status = 'active', last_seen_at = EXCLUDED.last_seen_at, updated_at = NOW()
RETURNING id`, episode.SourceKey, episode.VodID, lineKey, lineLabel, episode.LineOrder, nullableTime(episode.LastSeenAt)).Scan(&lineID); err != nil {
			return fmt.Errorf("upsert resource play line %s/%s/%s: %w", episode.SourceKey, episode.VodID, lineKey, err)
		}
		episode.LineID = lineID
		if _, err := store.database.Exec(ctx, `INSERT INTO resource_episode_candidates
(line_id, media_id, media_unit_id, season_number, episode_key, episode_label, play_url,
 format, quality, sort_order, resource_status, last_seen_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,COALESCE($12,NOW()),NOW())
ON CONFLICT (line_id, season_number, episode_key) DO UPDATE SET
media_id = COALESCE(EXCLUDED.media_id, resource_episode_candidates.media_id),
media_unit_id = COALESCE(EXCLUDED.media_unit_id, resource_episode_candidates.media_unit_id),
episode_label = EXCLUDED.episode_label, play_url = EXCLUDED.play_url,
format = EXCLUDED.format, quality = EXCLUDED.quality, sort_order = EXCLUDED.sort_order,
resource_status = 'active', last_seen_at = EXCLUDED.last_seen_at, updated_at = NOW()`,
			lineID, nullableMediaID(episode.MediaID), nullableMediaID(episode.MediaUnitID), season,
			episode.EpisodeKey, episode.EpisodeLabel, episode.PlayURL, episode.Format, episode.Quality,
			episode.SortOrder, status, nullableTime(episode.LastSeenAt)); err != nil {
			return fmt.Errorf("upsert resource episode candidate %s/%s/%s/%s: %w", episode.SourceKey, episode.VodID, lineKey, episode.EpisodeKey, err)
		}
	}
	return nil
}

// nullableUnitInt 把非正数转成 NULL 写库。
func nullableUnitInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

// ListResourceCandidates 取某一集的所有播放候选（播放页换源用）。
func (store *PostgresStore) ListResourceCandidates(ctx context.Context, mediaID, seasonNumber int, episodeKey string) ([]ResourceCandidate, error) {
	if mediaID <= 0 || seasonNumber < 1 || episodeKey == "" {
		return []ResourceCandidate{}, nil
	}
	return store.listResourceCandidates(ctx, resourceCandidateSelect+`
WHERE candidate.media_id = $1 AND candidate.season_number = $2 AND candidate.episode_key = $3
  AND candidate.resource_status NOT IN ('retired', 'deleted')
  AND line.resource_status NOT IN ('retired', 'deleted')
ORDER BY line.sort_order ASC, candidate.sort_order ASC`, mediaID, seasonNumber, episodeKey)
}

// ListAllEpisodes 取某部作品的全部集次和各自的可用线路数（播放页选集网格用）。
func (store *PostgresStore) ListAllEpisodes(ctx context.Context, mediaID int) ([]EpisodeInfo, error) {
	if mediaID <= 0 {
		return nil, nil
	}
	rows, err := store.database.Query(ctx, `SELECT season_number, episode_key,
		MIN(episode_label) AS episode_label, COUNT(DISTINCT line_id) AS source_count
		FROM resource_episode_candidates
		WHERE media_id = $1 AND resource_status NOT IN ('retired','deleted')
		GROUP BY season_number, episode_key
		ORDER BY season_number ASC, episode_key ASC`, mediaID)
	if err != nil {
		return nil, fmt.Errorf("list all episodes: %w", err)
	}
	defer rows.Close()
	var result []EpisodeInfo
	for rows.Next() {
		var ep EpisodeInfo
		if err := rows.Scan(&ep.SeasonNumber, &ep.EpisodeKey, &ep.EpisodeLabel, &ep.SourceCount); err != nil {
			return nil, fmt.Errorf("scan episode info: %w", err)
		}
		result = append(result, ep)
	}
	return result, rows.Err()
}

// ListUnitResourceCandidates 按季集 ID 取播放候选。
func (store *PostgresStore) ListUnitResourceCandidates(ctx context.Context, mediaUnitID int) ([]ResourceCandidate, error) {
	if mediaUnitID <= 0 {
		return []ResourceCandidate{}, nil
	}
	return store.listResourceCandidates(ctx, resourceCandidateSelect+`
WHERE candidate.media_unit_id = $1
  AND candidate.resource_status NOT IN ('retired', 'deleted')
  AND line.resource_status NOT IN ('retired', 'deleted')
ORDER BY line.sort_order ASC, candidate.sort_order ASC`, mediaUnitID)
}

const resourceCandidateSelect = `SELECT candidate.id, line.id, line.line_key, line.line_label, line.sort_order,
line.source_key, line.vod_id, candidate.media_id, candidate.media_unit_id, candidate.season_number,
candidate.episode_key, candidate.episode_label, candidate.play_url, candidate.sort_order, candidate.format, candidate.quality,
candidate.resource_status, candidate.last_seen_at, candidate.last_accessed_at,
COALESCE(resource.success_count, 0)::INTEGER,
COALESCE(resource.failure_count, 0)::INTEGER,
COALESCE(resource.avg_speed_ms, 0)::INTEGER,
COALESCE(link.confidence, 0)
FROM resource_episode_candidates candidate
JOIN resource_play_lines line ON line.id = candidate.line_id
LEFT JOIN vod_items resource ON resource.source_key = line.source_key AND resource.vod_id = line.vod_id
LEFT JOIN resource_media_links link ON link.source_key = line.source_key AND link.vod_id = line.vod_id
`

// listResourceCandidates 是上面几个查询的公共扫描逻辑。
func (store *PostgresStore) listResourceCandidates(ctx context.Context, query string, arguments ...any) ([]ResourceCandidate, error) {
	rows, err := store.database.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list resource candidates: %w", err)
	}
	defer rows.Close()
	result := make([]ResourceCandidate, 0)
	for rows.Next() {
		var candidate ResourceCandidate
		var mediaID, mediaUnitID *int
		var lastSeen, lastAccessed *time.Time
		if err := rows.Scan(&candidate.CandidateID, &candidate.LineID, &candidate.LineKey, &candidate.LineLabel, &candidate.LineOrder,
			&candidate.SourceKey, &candidate.VodID, &mediaID, &mediaUnitID, &candidate.SeasonNumber,
			&candidate.EpisodeKey, &candidate.EpisodeLabel, &candidate.PlayURL, &candidate.SortOrder,
			&candidate.Format, &candidate.Quality, &candidate.ResourceStatus, &lastSeen, &lastAccessed,
			&candidate.SuccessCount, &candidate.FailureCount, &candidate.AvgLoadMs, &candidate.MappingConfidence); err != nil {
			return nil, fmt.Errorf("scan resource candidate: %w", err)
		}
		if mediaID != nil {
			candidate.MediaID = *mediaID
		}
		if mediaUnitID != nil {
			candidate.MediaUnitID = *mediaUnitID
		}
		if lastSeen != nil {
			candidate.LastSeenAt = *lastSeen
		}
		if lastAccessed != nil {
			candidate.LastAccessedAt = *lastAccessed
		}
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource candidates: %w", err)
	}
	return result, nil
}

// FindResourceLink 取某条资源的媒体关联。
func (store *PostgresStore) FindResourceLink(ctx context.Context, sourceKey, vodID string) (ResourceLink, error) {
	row := store.database.QueryRow(ctx, `SELECT source_key, vod_id, media_id, confidence, matched_by, is_locked, verified_at
FROM resource_media_links WHERE source_key = $1 AND vod_id = $2`, sourceKey, vodID)
	var link ResourceLink
	var verifiedAt *time.Time
	if err := row.Scan(&link.SourceKey, &link.VodID, &link.MediaID, &link.Confidence, &link.MatchedBy, &link.IsLocked, &verifiedAt); err != nil {
		return ResourceLink{}, fmt.Errorf("find resource media link: %w", err)
	}
	if verifiedAt != nil {
		link.VerifiedAt = *verifiedAt
	}
	return link, nil
}

// FindResourceLinkID 是 search.Service 使用的小型适配方法，
// 使 search 包无需依赖 mediaidentity 的模型类型。
func (store *PostgresStore) FindResourceLinkID(ctx context.Context, sourceKey, vodID string) (int, float64, string, error) {
	link, err := store.FindResourceLink(ctx, sourceKey, vodID)
	if err != nil {
		return 0, 0, "", err
	}
	return link.MediaID, link.Confidence, link.MatchedBy, nil
}

// FindMediaIDByDoubanID 按豆瓣 ID 取 media_id。
func (store *PostgresStore) FindMediaIDByDoubanID(ctx context.Context, doubanID string) (int, error) {
	media, err := store.FindByDoubanID(ctx, doubanID)
	if err != nil {
		return 0, err
	}
	return media.ID, nil
}

// FindMediaIDByTitleYear 按标题+年份取 media_id。
func (store *PostgresStore) FindMediaIDByTitleYear(ctx context.Context, title, year string) (int, error) {
	media, err := store.FindByTitleYear(ctx, title, year)
	if err != nil {
		return 0, err
	}
	return media.ID, nil
}

// FindMediaIDByProviderID 把 Provider 外部 ID（例如 IMDb 的 "tt1234567"）解析为规范媒体 ID。
// 查询刻意忽略 external_type 以提高召回率，但共享整剧 ID 命中多个豆瓣季度时不猜测；
// 返回未命中，让调用方继续用带季数的标题和年份匹配。
func (store *PostgresStore) FindMediaIDByProviderID(ctx context.Context, provider, externalID string) (int, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	externalID = strings.TrimSpace(externalID)
	if provider == "" || externalID == "" {
		return 0, fmt.Errorf("provider and external_id required")
	}
	var mediaID int
	if err := store.database.QueryRow(ctx, `SELECT MIN(media_id) FROM media_external_ids
WHERE provider = $1 AND external_id = $2
HAVING COUNT(DISTINCT media_id) = 1`, provider, externalID).Scan(&mediaID); err != nil {
		return 0, fmt.Errorf("find media by provider id %s/%s: %w", provider, externalID, err)
	}
	return mediaID, nil
}

// FindMediaIDByTitleYearType 对规范标题、年份和媒体类型执行严格精确匹配。
// 这是匹配层级的第 3 层：置信度高于加权评分，但低于直接 ID 匹配。
func (store *PostgresStore) FindMediaIDByTitleYearType(ctx context.Context, title, year, mediaType string) (int, error) {
	normalizedTitle := NormalizeTitle(title)
	if normalizedTitle == "" || strings.TrimSpace(year) == "" {
		return 0, fmt.Errorf("title and year required for exact match")
	}
	var query string
	var args []any
	if mediaType != "" {
		query = mediaSelect + ` WHERE year = $2 AND media_type = $4 AND (
    LOWER(title) = LOWER($1) OR id IN (
        SELECT media_id FROM media_aliases WHERE normalized_alias = $3 OR LOWER(alias) = LOWER($1)
    )) ORDER BY updated_at DESC LIMIT 1`
		args = []any{strings.TrimSpace(title), strings.TrimSpace(year), normalizedTitle, mediaType}
	} else {
		query = mediaSelect + ` WHERE year = $2 AND (
    LOWER(title) = LOWER($1) OR id IN (
        SELECT media_id FROM media_aliases WHERE normalized_alias = $3 OR LOWER(alias) = LOWER($1)
    )) ORDER BY updated_at DESC LIMIT 1`
		args = []any{strings.TrimSpace(title), strings.TrimSpace(year), normalizedTitle}
	}
	row := store.database.QueryRow(ctx, query, args...)
	var media Media
	if err := scanMedia(row, &media); err != nil {
		return 0, fmt.Errorf("find media title/year/type %s/%s/%s: %w", title, year, mediaType, err)
	}
	return media.ID, nil
}

// LinkResourceIdentity 是 LinkResource 的扁平参数版本，供 search 包调用。
func (store *PostgresStore) LinkResourceIdentity(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error {
	return store.LinkResource(ctx, ResourceLink{SourceKey: sourceKey, VodID: vodID, MediaID: mediaID, Confidence: confidence, MatchedBy: matchedBy})
}

// SearchAdapter 把 PostgresStore 包装成 search 包声明的那组接口，
// 这样 search 不必知道 mediaidentity 的模型类型。
type SearchAdapter struct{ Store *PostgresStore }

// 下面这些方法都是一行转发，只为满足 search 包的接口签名。
func (adapter SearchAdapter) FindResourceLink(ctx context.Context, sourceKey, vodID string) (int, float64, string, error) {
	return adapter.Store.FindResourceLinkID(ctx, sourceKey, vodID)
}

// FindByDoubanID 按豆瓣 ID 找媒体。
func (adapter SearchAdapter) FindByDoubanID(ctx context.Context, doubanID string) (int, error) {
	return adapter.Store.FindMediaIDByDoubanID(ctx, doubanID)
}

// FindByExternalID 按外部平台 ID 找媒体。
func (adapter SearchAdapter) FindByExternalID(ctx context.Context, provider, externalID string) (int, error) {
	return adapter.Store.FindMediaIDByProviderID(ctx, provider, externalID)
}

// FindByTitleYearType 按片名 + 年份 + 类型找媒体。
func (adapter SearchAdapter) FindByTitleYearType(ctx context.Context, title, year, mediaType string) (int, error) {
	return adapter.Store.FindMediaIDByTitleYearType(ctx, title, year, mediaType)
}

// FindByTitleYear 按片名 + 年份找媒体。
func (adapter SearchAdapter) FindByTitleYear(ctx context.Context, title, year string) (int, error) {
	return adapter.Store.FindMediaIDByTitleYear(ctx, title, year)
}

// LinkResource 把资源和媒体绑定起来。
func (adapter SearchAdapter) LinkResource(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error {
	return adapter.Store.LinkResourceIdentity(ctx, sourceKey, vodID, mediaID, confidence, matchedBy)
}

// RecordMatchCandidate 记一条待人工复核的匹配候选。
func (adapter SearchAdapter) RecordMatchCandidate(ctx context.Context, sourceKey, vodID string, mediaID int, confidence float64, matchedBy string) error {
	return adapter.Store.RecordMatchCandidate(ctx, sourceKey, vodID, mediaID, confidence, matchedBy)
}

// mediaSelect 是媒体主表的公共查询列，字段顺序必须与 scanMedia 一致。
const mediaSelect = `SELECT id, media_type, douban_id, title, original_title, year, poster, backdrops, summary,
genres, countries, directors, actors, duration, rating_douban, rating_tmdb,
vote_count_tmdb, series_status, metadata_version, metadata_status, last_metadata_sync_at,
created_at, updated_at FROM media`

// scanMedia 扫描一行媒体记录。
func scanMedia(row database.Row, media *Media) error {
	var lastSync *time.Time
	if err := row.Scan(&media.ID, &media.MediaType, &media.DoubanID, &media.Title, &media.OriginalTitle,
		&media.Year, &media.Poster, &media.Backdrops, &media.Summary, &media.Genres, &media.Countries, &media.Directors,
		&media.Actors, &media.Duration, &media.RatingDouban, &media.RatingTMDB, &media.VoteCountTMDB,
		&media.SeriesStatus, &media.MetadataVersion, &media.MetadataStatus, &lastSync,
		&media.CreatedAt, &media.UpdatedAt); err != nil {
		return err
	}
	if lastSync != nil {
		media.LastMetadataSyncAt = *lastSync
	}
	return nil
}

// nullableTime 把零值时间转成 NULL 写库。
func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
