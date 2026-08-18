package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type PostgresStore struct {
	database database.Executor
	beginner database.Beginner
}

const vodItemColumns = `resource.source_key, resource.vod_id, resource.vod_name, resource.vod_sub,
       resource.vod_en, resource.vod_tag, resource.vod_class, resource.vod_pic, resource.vod_actor,
       resource.vod_director, resource.vod_blurb, resource.vod_remarks, resource.vod_pubdate,
       resource.vod_total, resource.vod_serial, resource.vod_area, resource.vod_lang, resource.vod_year,
       resource.vod_duration, resource.vod_time, resource.vod_douban_id, resource.vod_content,
       resource.vod_play_url, resource.type_name, resource.last_visited_at,
       COALESCE(resource_quality.avg_speed_ms, 0)::INTEGER,
       COALESCE(resource_quality.sample_count, 0)::INTEGER,
       COALESCE(resource_quality.failed_count, 0)::INTEGER,
       COALESCE(resource.resource_status, 'active'),
       COALESCE(media_link.media_id, 0),
       COALESCE(media_link.confidence, 0)::double precision,
       COALESCE(media_link.matched_by, '')`

// resourceQualityJoin 从唯一的播放质量汇总表读取资源级统计，避免维护第二套计数器。
const resourceQualityJoin = `LEFT JOIN LATERAL (
    SELECT CASE WHEN SUM(quality.first_frame_count) > 0
                THEN SUM(quality.first_frame_total_ms) / SUM(quality.first_frame_count) ELSE 0 END AS avg_speed_ms,
           SUM(quality.success_count + quality.failure_count) AS sample_count,
           SUM(quality.failure_count) AS failed_count
    FROM playback_quality_rollups quality
    JOIN resource_episode_candidates candidate ON candidate.id = quality.candidate_id
    JOIN resource_play_lines line ON line.id = candidate.line_id
    WHERE line.source_key = resource.source_key AND line.vod_id = resource.vod_id
      AND quality.bucket >= NOW() - INTERVAL '7 days'
) resource_quality ON TRUE`

// resourceMediaLinkJoin 把已确认/锁定的规范媒体身份读到资源行上，
// 这样直接读 vod_items 也能看到人工复核后的关联，不必再走一次服务层匹配。
const resourceMediaLinkJoin = `LEFT JOIN LATERAL (
    SELECT media_id, confidence, matched_by
    FROM resource_media_links link
    WHERE link.source_key = resource.source_key AND link.vod_id = resource.vod_id
    LIMIT 1
) media_link ON TRUE`

func (store *PostgresStore) Log(ctx context.Context, keyword string, userID *int, ipHash string) error {
	if _, err := store.database.Exec(ctx, `INSERT INTO search_logs (keyword, user_id, ip_hash, created_at) VALUES ($1, $2, $3, NOW())`, keyword, userID, ipHash); err != nil {
		return fmt.Errorf("insert search log: %w", err)
	}
	if _, err := store.database.Exec(ctx, `
INSERT INTO trending_keywords (keyword, count, last_searched_at)
VALUES ($1, 1, NOW())
ON CONFLICT (keyword) DO UPDATE SET
    count = trending_keywords.count + 1,
    last_searched_at = EXCLUDED.last_searched_at`, keyword); err != nil {
		return fmt.Errorf("update trending keyword: %w", err)
	}
	return nil
}

func (store *PostgresStore) Trending(ctx context.Context, hours, limit int) ([]TrendingKeyword, error) {
	query := `SELECT keyword, count, last_searched_at FROM trending_keywords ORDER BY count DESC LIMIT $1`
	arguments := []any{limit}
	if hours > 0 {
		query = `
SELECT keyword, COUNT(*) AS count, MAX(created_at) AS last_searched_at
FROM search_logs
WHERE created_at > NOW() - INTERVAL '1 hour' * $1
GROUP BY keyword
ORDER BY count DESC
LIMIT $2`
		arguments = []any{hours, limit}
	}
	rows, err := store.database.Query(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query trending keywords: %w", err)
	}
	defer rows.Close()
	items := make([]TrendingKeyword, 0)
	for rows.Next() {
		var item TrendingKeyword
		if err := rows.Scan(&item.Keyword, &item.Count, &item.LastSearchedAt); err != nil {
			return nil, fmt.Errorf("scan trending keyword: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trending keywords: %w", err)
	}
	return items, nil
}

func (store *PostgresStore) AddHealthStats(ctx context.Context, stats []HealthStat) error {
	if len(stats) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString(`INSERT INTO site_stats (site_key, bucket, ok_count, empty_count, timeout_count, error_count, total_ms) VALUES `)
	arguments := make([]any, 0, len(stats)*7)
	for index, stat := range stats {
		if index > 0 {
			query.WriteString(",")
		}
		base := index*7 + 1
		fmt.Fprintf(&query, "($%d,$%d,$%d,$%d,$%d,$%d,$%d)", base, base+1, base+2, base+3, base+4, base+5, base+6)
		arguments = append(arguments, stat.SiteKey, stat.Bucket, stat.OKCount, stat.EmptyCount, stat.TimeoutCount, stat.ErrorCount, stat.TotalMs)
	}
	query.WriteString(` ON CONFLICT (site_key, bucket) DO UPDATE SET
ok_count = site_stats.ok_count + EXCLUDED.ok_count,
empty_count = site_stats.empty_count + EXCLUDED.empty_count,
timeout_count = site_stats.timeout_count + EXCLUDED.timeout_count,
error_count = site_stats.error_count + EXCLUDED.error_count,
total_ms = site_stats.total_ms + EXCLUDED.total_ms`)
	if _, err := store.database.Exec(ctx, query.String(), arguments...); err != nil {
		return fmt.Errorf("upsert site health stats: %w", err)
	}
	return nil
}

func (store *PostgresStore) SummaryHealthSince(ctx context.Context, since time.Time) (map[string]*HealthSummary, error) {
	rows, err := store.database.Query(ctx, `SELECT site_key,
       SUM(ok_count), SUM(empty_count), SUM(timeout_count), SUM(error_count), SUM(total_ms)
FROM site_stats
WHERE bucket >= $1
GROUP BY site_key`, since)
	if err != nil {
		return nil, fmt.Errorf("summarize site health stats: %w", err)
	}
	defer rows.Close()
	summaries := make(map[string]*HealthSummary)
	for rows.Next() {
		summary := &HealthSummary{}
		if err := rows.Scan(&summary.SiteKey, &summary.OKCount, &summary.EmptyCount, &summary.TimeoutCount, &summary.ErrorCount, &summary.TotalMs); err != nil {
			return nil, fmt.Errorf("scan site health summary: %w", err)
		}
		summaries[summary.SiteKey] = summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate site health summaries: %w", err)
	}
	return summaries, nil
}

func (store *PostgresStore) DeleteOldKeywords(ctx context.Context, days int) (int, error) {
	affected, err := store.database.Exec(ctx, `DELETE FROM trending_keywords WHERE last_searched_at < NOW() - ($1 * INTERVAL '1 day')`, days)
	if err != nil {
		return 0, fmt.Errorf("delete old trending keywords: %w", err)
	}
	return int(affected), nil
}

func (store *PostgresStore) DeleteOldSearchLogs(ctx context.Context, days int) (int, error) {
	affected, err := store.database.Exec(ctx, `DELETE FROM search_logs WHERE created_at < NOW() - ($1 * INTERVAL '1 day')`, days)
	if err != nil {
		return 0, fmt.Errorf("delete old search logs: %w", err)
	}
	return int(affected), nil
}

func (store *PostgresStore) DeleteHealthBefore(ctx context.Context, before time.Time) (int, error) {
	affected, err := store.database.Exec(ctx, `DELETE FROM site_stats WHERE bucket < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("delete old site health stats: %w", err)
	}
	return int(affected), nil
}

func NewPostgresStore(executor database.Executor) *PostgresStore {
	store := &PostgresStore{database: executor}
	if beginner, ok := executor.(database.Beginner); ok {
		store.beginner = beginner
	}
	return store
}

func (store *PostgresStore) Search(ctx context.Context, keyword string) ([]VodItem, error) {
	pattern := "%" + keyword + "%"
	rows, err := store.database.Query(ctx, `SELECT `+vodItemColumns+`
FROM vod_items resource `+resourceMediaLinkJoin+` `+resourceQualityJoin+`
WHERE resource.vod_name LIKE $1 OR resource.vod_sub LIKE $1 OR resource.vod_en LIKE $1
ORDER BY resource.last_visited_at DESC`, pattern)
	if err != nil {
		return nil, fmt.Errorf("search vod items: %w", err)
	}
	return scanVodItems(rows)
}

func (store *PostgresStore) FindBySourceID(ctx context.Context, sourceKey, vodID string) (*VodItem, error) {
	rows, err := store.database.Query(ctx, `SELECT `+vodItemColumns+` FROM vod_items resource `+resourceMediaLinkJoin+` `+resourceQualityJoin+`
WHERE resource.source_key = $1 AND resource.vod_id = $2 LIMIT 1`, sourceKey, vodID)
	if err != nil {
		return nil, fmt.Errorf("find vod item: %w", err)
	}
	items, err := scanVodItems(rows)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (store *PostgresStore) SearchByDoubanID(ctx context.Context, doubanID string) ([]VodItem, error) {
	rows, err := store.database.Query(ctx, `SELECT `+vodItemColumns+` FROM vod_items resource `+resourceMediaLinkJoin+` `+resourceQualityJoin+`
WHERE resource.vod_douban_id = $1 ORDER BY resource.last_visited_at DESC`, doubanID)
	if err != nil {
		return nil, fmt.Errorf("search vod items by douban id: %w", err)
	}
	return scanVodItems(rows)
}

func (store *PostgresStore) LoadStats(ctx context.Context, sourceKey, vodID string) (*LoadStats, error) {
	rows, err := store.database.Query(ctx, `SELECT
COALESCE(CASE WHEN SUM(quality.first_frame_count) > 0
              THEN SUM(quality.first_frame_total_ms) / SUM(quality.first_frame_count) ELSE 0 END, 0)::INTEGER,
COALESCE(SUM(quality.success_count + quality.failure_count), 0)::INTEGER,
COALESCE(SUM(quality.failure_count), 0)::INTEGER
FROM playback_quality_rollups quality
JOIN resource_episode_candidates candidate ON candidate.id = quality.candidate_id
JOIN resource_play_lines line ON line.id = candidate.line_id
WHERE line.source_key = $1 AND line.vod_id = $2
  AND quality.bucket >= NOW() - INTERVAL '7 days'`, sourceKey, vodID)
	if err != nil {
		return nil, fmt.Errorf("get load stats: %w", err)
	}
	defer rows.Close()
	stats := &LoadStats{}
	if rows.Next() {
		if err := rows.Scan(&stats.AvgSpeedMs, &stats.SampleCount, &stats.FailedCount); err != nil {
			return nil, fmt.Errorf("scan load stats: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate load stats: %w", err)
	}
	if stats.SampleCount > 0 {
		stats.SuccessRate = float64(stats.SampleCount-stats.FailedCount) / float64(stats.SampleCount) * 100
	}
	return stats, nil
}

func nullableMediaID(id int) any {
	if id <= 0 {
		return nil
	}
	return id
}

func scanVodItems(rows database.Rows) ([]VodItem, error) {
	defer rows.Close()
	items := make([]VodItem, 0)
	for rows.Next() {
		var item VodItem
		if err := rows.Scan(
			&item.SourceKey, &item.VodId, &item.VodName, &item.VodSub, &item.VodEn,
			&item.VodTag, &item.VodClass, &item.VodPic, &item.VodActor, &item.VodDirector,
			&item.VodBlurb, &item.VodRemarks, &item.VodPubdate, &item.VodTotal,
			&item.VodSerial, &item.VodArea, &item.VodLang, &item.VodYear,
			&item.VodDuration, &item.VodTime, &item.VodDoubanId, &item.VodContent,
			&item.VodPlayUrl, &item.TypeName, &item.LastVisitedAt, &item.AvgSpeedMs,
			&item.SampleCount, &item.FailedCount, &item.ResourceStatus,
			&item.MediaID, &item.MediaConfidence, &item.MediaMatch,
		); err != nil {
			return nil, fmt.Errorf("scan vod item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vod items: %w", err)
	}
	return items, nil
}

func (store *PostgresStore) Upsert(ctx context.Context, item VodItem) error {
	now := time.Now()
	if item.LastVisitedAt.IsZero() {
		item.LastVisitedAt = now
	}
	metadataHash := StableResourceHash(item)
	_, err := store.database.Exec(ctx, `
INSERT INTO vod_items (
    source_key, vod_id, vod_name, vod_sub, vod_en, vod_tag, vod_class,
    vod_pic, vod_actor, vod_director, vod_blurb, vod_remarks, vod_pubdate,
    vod_total, vod_serial, vod_area, vod_lang, vod_year, vod_duration,
    vod_time, vod_douban_id, vod_content, vod_play_url, type_name,
    last_visited_at, last_seen_at, last_discovered_at, resource_status,
    metadata_hash, metadata_version, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25,
    $26, $26, 'active', $27, 1, $28
)
ON CONFLICT (source_key, vod_id) DO UPDATE SET
    vod_name = EXCLUDED.vod_name,
    vod_sub = EXCLUDED.vod_sub,
    vod_remarks = EXCLUDED.vod_remarks,
    vod_time = EXCLUDED.vod_time,
    vod_play_url = EXCLUDED.vod_play_url,
    last_visited_at = EXCLUDED.last_visited_at,
    last_seen_at = NOW(), last_discovered_at = NOW(), resource_status = 'active',
    stale_at = NULL, cold_at = NULL, lifecycle_batch_id = NULL, updated_at = NOW(),
    metadata_hash = EXCLUDED.metadata_hash,
    metadata_version = CASE
        WHEN COALESCE(vod_items.metadata_hash, '') = '' THEN 1
        WHEN vod_items.metadata_hash = EXCLUDED.metadata_hash THEN GREATEST(COALESCE(vod_items.metadata_version, 1), 1)
        ELSE GREATEST(COALESCE(vod_items.metadata_version, 1), 1) + 1
    END`,
		item.SourceKey, item.VodId, item.VodName, item.VodSub, item.VodEn,
		item.VodTag, item.VodClass, item.VodPic, item.VodActor, item.VodDirector,
		item.VodBlurb, item.VodRemarks, item.VodPubdate, item.VodTotal,
		item.VodSerial, item.VodArea, item.VodLang, item.VodYear, item.VodDuration,
		item.VodTime, item.VodDoubanId, item.VodContent, item.VodPlayUrl,
		item.TypeName, item.LastVisitedAt, now, metadataHash, now,
	)
	if err != nil {
		return fmt.Errorf("upsert vod item: %w", err)
	}
	return nil
}

func (store *PostgresStore) ListEnabled(ctx context.Context) ([]Site, error) {
	rows, err := store.database.Query(ctx, `SELECT key, base_url, enabled FROM sites WHERE enabled = true ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list enabled sites: %w", err)
	}
	defer rows.Close()
	sites := make([]Site, 0)
	for rows.Next() {
		var site Site
		if err := rows.Scan(&site.Key, &site.BaseURL, &site.Enabled); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sites: %w", err)
	}
	return sites, nil
}

func (store *PostgresStore) FindSiteByKey(ctx context.Context, key string) (*Site, error) {
	rows, err := store.database.Query(ctx, `SELECT key, base_url, enabled FROM sites WHERE key = $1 LIMIT 1`, key)
	if err != nil {
		return nil, fmt.Errorf("find site: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var site Site
	if err := rows.Scan(&site.Key, &site.BaseURL, &site.Enabled); err != nil {
		return nil, fmt.Errorf("scan site: %w", err)
	}
	return &site, nil
}

func (store *PostgresStore) CopyrightKeywords(ctx context.Context) ([]string, error) {
	return store.keywords(ctx, `SELECT keyword FROM copyright_filters`)
}

func (store *PostgresStore) CategoryKeywords(ctx context.Context) ([]string, error) {
	return store.keywords(ctx, `SELECT keyword FROM category_filters`)
}

func (store *PostgresStore) keywords(ctx context.Context, query string) ([]string, error) {
	rows, err := store.database.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list filter keywords: %w", err)
	}
	defer rows.Close()
	keywords := make([]string, 0)
	for rows.Next() {
		var keyword string
		if err := rows.Scan(&keyword); err != nil {
			return nil, fmt.Errorf("scan filter keyword: %w", err)
		}
		keywords = append(keywords, keyword)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filter keywords: %w", err)
	}
	return keywords, nil
}

func (store *PostgresStore) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := store.database.Query(ctx, `SELECT id, key, base_url, enabled, created_at, updated_at FROM sites ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()
	sites := make([]Site, 0)
	for rows.Next() {
		var site Site
		if err := rows.Scan(&site.ID, &site.Key, &site.BaseURL, &site.Enabled, &site.CreatedAt, &site.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

func (store *PostgresStore) GetSite(ctx context.Context, id uint) (*Site, error) {
	rows, err := store.database.Query(ctx, `SELECT id, key, base_url, enabled, created_at, updated_at FROM sites WHERE id = $1 LIMIT 1`, id)
	if err != nil {
		return nil, fmt.Errorf("get site: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var site Site
	if err := rows.Scan(&site.ID, &site.Key, &site.BaseURL, &site.Enabled, &site.CreatedAt, &site.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scan site: %w", err)
	}
	return &site, nil
}

func (store *PostgresStore) CreateSite(ctx context.Context, site Site) (*Site, error) {
	now := time.Now().Unix()
	if err := store.database.QueryRow(ctx, `INSERT INTO sites (key, base_url, enabled, created_at, updated_at)
VALUES ($1,$2,$3,$4,$4) RETURNING id`, site.Key, site.BaseURL, site.Enabled, now).Scan(&site.ID); err != nil {
		return nil, fmt.Errorf("create site: %w", err)
	}
	site.CreatedAt, site.UpdatedAt = now, now
	return &site, nil
}

func (store *PostgresStore) UpdateSite(ctx context.Context, site Site) error {
	if _, err := store.database.Exec(ctx, `UPDATE sites SET
key = CASE WHEN $2 = '' THEN key ELSE $2 END,
base_url = CASE WHEN $3 = '' THEN base_url ELSE $3 END,
enabled = $4, updated_at = $5 WHERE id = $1`, site.ID, site.Key, site.BaseURL, site.Enabled, time.Now().Unix()); err != nil {
		return fmt.Errorf("update site: %w", err)
	}
	return nil
}

func (store *PostgresStore) DeleteSite(ctx context.Context, id uint) error {
	_, err := store.database.Exec(ctx, `DELETE FROM sites WHERE id = $1`, id)
	return err
}

func (store *PostgresStore) DeleteInactive(ctx context.Context, days int) (int, error) {
	// 只把资源标记为 stale，不直接删除。这样用户历史和剧集引用仍然有效，
	// 后续抓取也可以重新激活同一个资源键。
	affected, err := store.database.Exec(ctx, `UPDATE vod_items v SET resource_status = 'stale', stale_at = COALESCE(stale_at, NOW()), updated_at = NOW()
WHERE COALESCE(v.resource_status, 'active') = 'active'
  AND COALESCE(v.last_seen_at, v.last_visited_at) < $1
  AND NOT EXISTS (SELECT 1 FROM playback_positions position
                  WHERE position.deleted_at IS NULL
                    AND position.last_source_key = v.source_key AND position.last_vod_id = v.vod_id)`, time.Now().AddDate(0, 0, -days))
	return int(affected), err
}

func (store *PostgresStore) ListCopyrightFilters(ctx context.Context) ([]Filter, error) {
	return store.listFilters(ctx, `SELECT id, keyword, created_at, updated_at FROM copyright_filters ORDER BY id`)
}

func (store *PostgresStore) CreateCopyrightFilter(ctx context.Context, keyword string) (*Filter, error) {
	return store.createFilter(ctx, "copyright_filters", keyword)
}

func (store *PostgresStore) UpdateCopyrightFilter(ctx context.Context, id uint, keyword string) error {
	_, err := store.database.Exec(ctx, `UPDATE copyright_filters SET keyword = $2, updated_at = NOW() WHERE id = $1`, id, keyword)
	return err
}

func (store *PostgresStore) DeleteCopyrightFilter(ctx context.Context, id uint) error {
	_, err := store.database.Exec(ctx, `DELETE FROM copyright_filters WHERE id = $1`, id)
	return err
}

func (store *PostgresStore) ListCategoryFilters(ctx context.Context) ([]Filter, error) {
	return store.listFilters(ctx, `SELECT id, keyword, created_at, updated_at FROM category_filters ORDER BY id`)
}

func (store *PostgresStore) CreateCategoryFilter(ctx context.Context, keyword string) (*Filter, error) {
	return store.createFilter(ctx, "category_filters", keyword)
}

func (store *PostgresStore) DeleteCategoryFilter(ctx context.Context, id uint) error {
	_, err := store.database.Exec(ctx, `DELETE FROM category_filters WHERE id = $1`, id)
	return err
}

func (store *PostgresStore) listFilters(ctx context.Context, query string) ([]Filter, error) {
	rows, err := store.database.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list filters: %w", err)
	}
	defer rows.Close()
	filters := make([]Filter, 0)
	for rows.Next() {
		var filter Filter
		if err := rows.Scan(&filter.ID, &filter.Keyword, &filter.CreatedAt, &filter.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan filter: %w", err)
		}
		filters = append(filters, filter)
	}
	return filters, rows.Err()
}

func (store *PostgresStore) createFilter(ctx context.Context, table, keyword string) (*Filter, error) {
	if table != "copyright_filters" && table != "category_filters" {
		return nil, fmt.Errorf("unsupported filter table")
	}
	filter := &Filter{Keyword: keyword, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	query := `INSERT INTO ` + table + ` (keyword, created_at, updated_at) VALUES ($1,$2,$3) RETURNING id`
	if err := store.database.QueryRow(ctx, query, keyword, filter.CreatedAt, filter.UpdatedAt).Scan(&filter.ID); err != nil {
		return nil, fmt.Errorf("create filter: %w", err)
	}
	return filter, nil
}
