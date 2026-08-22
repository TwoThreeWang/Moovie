package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// PostgresStore 是 search 域所有存储接口的唯一实现，
// 同时被 admin、operations 等模块复用（它们各自只依赖其中一小部分方法）。
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
       resource.avg_speed_ms,
       (resource.success_count + resource.failure_count)::INTEGER,
       resource.failure_count,
       COALESCE(resource.resource_status, 'active'),
       COALESCE(media_link.media_id, 0),
       COALESCE(media_link.confidence, 0)::double precision,
       COALESCE(media_link.matched_by, '')`

const searchRowBudget = 300

// resourceMediaLinkJoin 把已确认/锁定的规范媒体身份读到资源行上，
// 这样直接读 vod_items 也能看到人工复核后的关联，不必再走一次服务层匹配。
const resourceMediaLinkJoin = `LEFT JOIN LATERAL (
    SELECT media_id, confidence, matched_by
    FROM resource_media_links link
    WHERE link.source_key = resource.source_key AND link.vod_id = resource.vod_id
    LIMIT 1
) media_link ON TRUE`

// Log 写一条搜索日志。
func (store *PostgresStore) Log(ctx context.Context, keyword string, userID *int, ipHash string) error {
	if _, err := store.database.Exec(ctx, `INSERT INTO search_logs (keyword, user_id, ip_hash, created_at) VALUES ($1, $2, $3, NOW())`, keyword, userID, ipHash); err != nil {
		return fmt.Errorf("insert search log: %w", err)
	}
	return nil
}

// Trending 按时间窗口从搜索日志聚合热搜榜。
func (store *PostgresStore) Trending(ctx context.Context, hours, limit int) ([]TrendingKeyword, error) {
	rows, err := store.database.Query(ctx, `
SELECT keyword, COUNT(*) AS count, MAX(created_at) AS last_searched_at
FROM search_logs
WHERE created_at > NOW() - INTERVAL '1 hour' * $1
GROUP BY keyword
ORDER BY count DESC
LIMIT $2`, hours, limit)
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

// AddHealthStats 批量写资源站健康统计，同一小时桶累加。
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

// SummaryHealthSince 汇总某时间点之后的资源站健康数据。
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

// DeleteOldSearchLogs 清理过期搜索日志。
func (store *PostgresStore) DeleteOldSearchLogs(ctx context.Context, days int) (int, error) {
	affected, err := store.database.Exec(ctx, `DELETE FROM search_logs WHERE created_at < NOW() - ($1 * INTERVAL '1 day')`, days)
	if err != nil {
		return 0, fmt.Errorf("delete old search logs: %w", err)
	}
	return int(affected), nil
}

// DeleteHealthBefore 清理过期的资源站健康统计。
func (store *PostgresStore) DeleteHealthBefore(ctx context.Context, before time.Time) (int, error) {
	affected, err := store.database.Exec(ctx, `DELETE FROM site_stats WHERE bucket < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("delete old site health stats: %w", err)
	}
	return int(affected), nil
}

// NewPostgresStore 创建存储实现；如果传入的执行器支持事务，会一并保存以支持需要事务的操作。
func NewPostgresStore(executor database.Executor) *PostgresStore {
	store := &PostgresStore{database: executor}
	if beginner, ok := executor.(database.Beginner); ok {
		store.beginner = beginner
	}
	return store
}

// Search 在本地 vod_items 里做模糊匹配（片名/副标题/英文名）。
// ponytail: LIKE '%kw%' 无法走索引，数据量继续增长时需要换成全文检索或三元组索引。
func (store *PostgresStore) Search(ctx context.Context, keyword string) ([]VodItem, error) {
	pattern := "%" + keyword + "%"
	rows, err := store.database.Query(ctx, `SELECT `+vodItemColumns+`
FROM (
    SELECT * FROM vod_items
    WHERE vod_name LIKE $1 OR vod_sub LIKE $1 OR vod_en LIKE $1
    ORDER BY last_visited_at DESC
    LIMIT $2
) resource `+resourceMediaLinkJoin+`
ORDER BY resource.last_visited_at DESC`, pattern, searchRowBudget)
	if err != nil {
		return nil, fmt.Errorf("search vod items: %w", err)
	}
	return scanVodItems(rows)
}

// FindBySourceID 按 (来源, 资源ID) 精确取一条资源。
func (store *PostgresStore) FindBySourceID(ctx context.Context, sourceKey, vodID string) (*VodItem, error) {
	rows, err := store.database.Query(ctx, `SELECT `+vodItemColumns+` FROM vod_items resource `+resourceMediaLinkJoin+`
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

// SearchByDoubanID 按豆瓣 ID 找出所有对应资源。
func (store *PostgresStore) SearchByDoubanID(ctx context.Context, doubanID string) ([]VodItem, error) {
	rows, err := store.database.Query(ctx, `SELECT `+vodItemColumns+` FROM vod_items resource `+resourceMediaLinkJoin+`
WHERE resource.vod_douban_id = $1 ORDER BY resource.last_visited_at DESC`, doubanID)
	if err != nil {
		return nil, fmt.Errorf("search vod items by douban id: %w", err)
	}
	return scanVodItems(rows)
}

func (store *PostgresStore) LoadStats(ctx context.Context, sourceKey, vodID string) (*LoadStats, error) {
	rows, err := store.database.Query(ctx, `SELECT avg_speed_ms, success_count + failure_count, failure_count
FROM vod_items WHERE source_key = $1 AND vod_id = $2`, sourceKey, vodID)
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

// nullableMediaID 把 0 转成 NULL 写库。
func nullableMediaID(id int) any {
	if id <= 0 {
		return nil
	}
	return id
}

// scanVodItems 按 vodItemColumns 的顺序扫描资源行，字段顺序必须与常量保持一致。
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

// Upsert 写入或更新一条资源。metadata_hash 用来判断内容是否真的变了：
// 内容没变时 metadata_version 保持不变，避免每次抓取都触发下游刷新。
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
    stale_at = NULL, updated_at = NOW(),
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

// ListEnabled 取启用中的资源站。
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

// FindSiteByKey 按 key 取资源站。
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

// CopyrightKeywords 取版权屏蔽词。
func (store *PostgresStore) CopyrightKeywords(ctx context.Context) ([]string, error) {
	return store.keywords(ctx, `SELECT keyword FROM copyright_filters`)
}

// CategoryKeywords 取分类屏蔽词（抓取阶段就丢弃这些分类）。
func (store *PostgresStore) CategoryKeywords(ctx context.Context) ([]string, error) {
	return store.keywords(ctx, `SELECT keyword FROM category_filters`)
}

// keywords 是上面两个方法的公共查询。
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

// 下面几个方法是后台管理资源站用的增删改查。
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

// GetSite 按 ID 查资源网。
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

// CreateSite 新增资源网。
func (store *PostgresStore) CreateSite(ctx context.Context, site Site) (*Site, error) {
	now := time.Now().Unix()
	if err := store.database.QueryRow(ctx, `INSERT INTO sites (key, base_url, enabled, created_at, updated_at)
VALUES ($1,$2,$3,$4,$4) RETURNING id`, site.Key, site.BaseURL, site.Enabled, now).Scan(&site.ID); err != nil {
		return nil, fmt.Errorf("create site: %w", err)
	}
	site.CreatedAt, site.UpdatedAt = now, now
	return &site, nil
}

// UpdateSite 空字符串表示不修改该字段。
func (store *PostgresStore) UpdateSite(ctx context.Context, site Site) error {
	if _, err := store.database.Exec(ctx, `UPDATE sites SET
key = CASE WHEN $2 = '' THEN key ELSE $2 END,
base_url = CASE WHEN $3 = '' THEN base_url ELSE $3 END,
enabled = $4, updated_at = $5 WHERE id = $1`, site.ID, site.Key, site.BaseURL, site.Enabled, time.Now().Unix()); err != nil {
		return fmt.Errorf("update site: %w", err)
	}
	return nil
}

// DeleteSite 删除资源网。
func (store *PostgresStore) DeleteSite(ctx context.Context, id uint) error {
	_, err := store.database.Exec(ctx, `DELETE FROM sites WHERE id = $1`, id)
	return err
}

// DeleteInactive 名字叫 Delete，实际只把长期未出现的资源标记为 stale（见内部注释）。
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

// PurgeStaleResources 删除已标记 stale 且 90 天以上无人播放、无站点收录的资源，
// 但保留每个媒体的最后一条资源（确保媒体至少有一个可用来源）。
func (store *PostgresStore) PurgeStaleResources(ctx context.Context, staleDays int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -staleDays)
	affected, err := store.database.Exec(ctx, `DELETE FROM vod_items v
WHERE v.resource_status = 'stale'
  AND COALESCE(v.last_seen_at, v.created_at) < $1
  AND COALESCE(v.last_played_at, '1970-01-01') < $1
  AND EXISTS (
      SELECT 1 FROM resource_media_links link
      JOIN resource_media_links sibling
        ON sibling.media_id = link.media_id
       AND (sibling.source_key, sibling.vod_id) <> (link.source_key, link.vod_id)
      WHERE link.source_key = v.source_key AND link.vod_id = v.vod_id
  )`, cutoff)
	return int(affected), err
}

// 下面几个方法是后台管理屏蔽词用的增删改查。
func (store *PostgresStore) ListCopyrightFilters(ctx context.Context) ([]Filter, error) {
	return store.listFilters(ctx, `SELECT id, keyword, created_at, updated_at FROM copyright_filters ORDER BY id`)
}

// CreateCopyrightFilter 新增版权屏蔽词。
func (store *PostgresStore) CreateCopyrightFilter(ctx context.Context, keyword string) (*Filter, error) {
	return store.createFilter(ctx, "copyright_filters", keyword)
}

// UpdateCopyrightFilter 修改版权屏蔽词。
func (store *PostgresStore) UpdateCopyrightFilter(ctx context.Context, id uint, keyword string) error {
	_, err := store.database.Exec(ctx, `UPDATE copyright_filters SET keyword = $2, updated_at = NOW() WHERE id = $1`, id, keyword)
	return err
}

// DeleteCopyrightFilter 删除版权屏蔽词。
func (store *PostgresStore) DeleteCopyrightFilter(ctx context.Context, id uint) error {
	_, err := store.database.Exec(ctx, `DELETE FROM copyright_filters WHERE id = $1`, id)
	return err
}

// ListCategoryFilters 列出分类屏蔽词。
func (store *PostgresStore) ListCategoryFilters(ctx context.Context) ([]Filter, error) {
	return store.listFilters(ctx, `SELECT id, keyword, created_at, updated_at FROM category_filters ORDER BY id`)
}

// CreateCategoryFilter 新增分类屏蔽词。
func (store *PostgresStore) CreateCategoryFilter(ctx context.Context, keyword string) (*Filter, error) {
	return store.createFilter(ctx, "category_filters", keyword)
}

// DeleteCategoryFilter 删除分类屏蔽词。
func (store *PostgresStore) DeleteCategoryFilter(ctx context.Context, id uint) error {
	_, err := store.database.Exec(ctx, `DELETE FROM category_filters WHERE id = $1`, id)
	return err
}

// listFilters 是两类屏蔽词的公共查询。
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

// createFilter 表名是拼进 SQL 的，因此这里用白名单校验，杜绝注入。
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
