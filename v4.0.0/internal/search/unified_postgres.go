package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/mediatitle"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// SearchUnifiedMedia 只读取规范媒体元数据。资源行会单独加载，
// 避免同一个媒体实体因为多个别名或来源而重复出现。
func (store *PostgresStore) SearchUnifiedMedia(ctx context.Context, query UnifiedQuery) ([]UnifiedItem, error) {
	pattern := "%" + strings.TrimSpace(query.Keyword) + "%"
	normalizedPattern := ""
	if normalizedKeyword := mediatitle.Normalize(query.Keyword); normalizedKeyword != "" {
		normalizedPattern = "%" + normalizedKeyword + "%"
	}
	rows, err := store.database.Query(ctx, `SELECT media.id, media.title, media.original_title,
       COALESCE(ARRAY(SELECT alias.alias FROM media_aliases alias
         WHERE alias.media_id = media.id AND alias.alias_type = 'aka' ORDER BY alias.id), ARRAY[]::text[]), media.year,
       media.media_type, media.poster, media.douban_id,
       COALESCE(media.rating_douban, 0), COALESCE(LEFT(media.summary, 120), '')
FROM media
WHERE (media.title ILIKE $1 OR media.original_title ILIKE $1 OR EXISTS (
    SELECT 1 FROM media_aliases alias
    WHERE alias.media_id = media.id AND $2 <> '' AND alias.normalized_alias LIKE $2
))
  AND ($3 = '' OR media.year = $3)
  AND ($4 = '' OR media.media_type = $4)
ORDER BY CASE
    WHEN LOWER(media.title) = LOWER($5) THEN 0
    WHEN LOWER(media.original_title) = LOWER($5) THEN 1
    ELSE 2
END, media.updated_at DESC, media.id
LIMIT $6`, pattern, normalizedPattern, strings.TrimSpace(query.Year), normalizeUnifiedMediaType(query.MediaType), strings.TrimSpace(query.Keyword), query.Limit)
	if err != nil {
		return nil, fmt.Errorf("search unified media: %w", err)
	}
	defer rows.Close()
	items := make([]UnifiedItem, 0)
	for rows.Next() {
		var item UnifiedItem
		if err := rows.Scan(&item.MediaID, &item.Title, &item.OriginalTitle, &item.SearchAliases, &item.Year, &item.MediaType, &item.Poster, &item.DoubanID, &item.RatingDouban, &item.Summary); err != nil {
			return nil, fmt.Errorf("scan unified media: %w", err)
		}
		item.Resources = make([]UnifiedResource, 0)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unified media: %w", err)
	}
	return items, nil
}

func (store *PostgresStore) ListUnifiedResources(ctx context.Context, mediaIDs []int) ([]VodItem, error) {
	if len(mediaIDs) == 0 {
		return []VodItem{}, nil
	}
	identifiers := make([]int64, len(mediaIDs))
	for index, mediaID := range mediaIDs {
		identifiers[index] = int64(mediaID)
	}
	rows, err := store.database.Query(ctx, `SELECT link.media_id, `+vodItemColumns+`
FROM resource_media_links link
JOIN vod_items resource ON resource.source_key = link.source_key AND resource.vod_id = link.vod_id
`+resourceQualityJoin+`
WHERE link.media_id = ANY($1::bigint[])
  AND COALESCE(resource.resource_status, 'active') <> 'removed'
ORDER BY link.media_id, resource.last_visited_at DESC`, identifiers)
	if err != nil {
		return nil, fmt.Errorf("list unified resources: %w", err)
	}
	return scanUnifiedResources(rows)
}

// ListResourcesByDoubanID 返回适合嵌入豆瓣卡片的精简资源信息。
// 它按 vod_douban_id 直接查询 vod_items，因此不依赖 resource_media_links 是否已经回填。
func (store *PostgresStore) ListResourcesByDoubanID(ctx context.Context, doubanID string) ([]LinkedResourceRow, error) {
	rows, err := store.database.Query(ctx, `SELECT resource.source_key, resource.vod_id,
       COALESCE(resource.vod_name, ''), COALESCE(resource.vod_pic, ''), COALESCE(resource.vod_year, ''),
       COALESCE(resource.vod_area, ''), COALESCE(resource.type_name, ''), COALESCE(resource.vod_actor, ''),
       COALESCE(resource.vod_remarks, ''), COALESCE(resource.vod_douban_id, ''),
       COALESCE(resource_quality.avg_speed_ms, 0)::INTEGER,
       COALESCE(resource_quality.sample_count, 0)::INTEGER,
       COALESCE(resource_quality.failed_count, 0)::INTEGER
FROM vod_items resource `+resourceQualityJoin+`
WHERE resource.vod_douban_id = $1
  AND COALESCE(resource.resource_status, 'active') <> 'removed'
ORDER BY CASE WHEN resource_quality.avg_speed_ms > 0 AND resource_quality.sample_count > 0 THEN 0 ELSE 1 END,
         resource_quality.avg_speed_ms ASC NULLS LAST
LIMIT 10`, doubanID)
	if err != nil {
		return nil, fmt.Errorf("list resources by douban id: %w", err)
	}
	defer rows.Close()
	items := make([]LinkedResourceRow, 0, 6)
	for rows.Next() {
		var r LinkedResourceRow
		if err := rows.Scan(&r.SourceKey, &r.VodID,
			&r.VodName, &r.VodPic, &r.VodYear, &r.VodArea, &r.TypeName, &r.VodActor,
			&r.VodRemarks, &r.VodDoubanID,
			&r.AvgSpeedMs, &r.SampleCount, &r.FailedCount); err != nil {
			return nil, fmt.Errorf("scan linked resource: %w", err)
		}
		items = append(items, r)
	}
	return items, rows.Err()
}

// HasPlayableResource 与 /watch 的输入保持一致：只有已经建立剧集候选、线路仍有效，
// 且资源详情包含播放地址时，详情页才展示“立即播放”。
func (store *PostgresStore) HasPlayableResource(ctx context.Context, mediaID int) (bool, error) {
	if mediaID <= 0 {
		return false, nil
	}
	var playable bool
	err := store.database.QueryRow(ctx, `SELECT EXISTS (
	    SELECT 1
	    FROM resource_episode_candidates candidate
	    JOIN resource_play_lines line ON line.id = candidate.line_id
	    JOIN vod_items resource ON resource.source_key = line.source_key AND resource.vod_id = line.vod_id
	    WHERE candidate.media_id = $1
	      AND candidate.resource_status NOT IN ('retired', 'deleted')
	      AND line.resource_status NOT IN ('retired', 'deleted')
	      AND COALESCE(resource.resource_status, 'active') <> 'removed'
	      AND COALESCE(resource.vod_play_url, '') <> ''
	)`, mediaID).Scan(&playable)
	if err != nil {
		return false, fmt.Errorf("check playable resource: %w", err)
	}
	return playable, nil
}

// LinkedResourceRow 保存豆瓣卡片中渲染搜索结果样式资源卡所需的完整 vod_item 信息。
type LinkedResourceRow struct {
	SourceKey   string
	VodID       string
	VodName     string
	VodPic      string
	VodYear     string
	VodArea     string
	TypeName    string
	VodActor    string
	VodRemarks  string
	VodDoubanID string
	AvgSpeedMs  int
	SampleCount int
	FailedCount int
}

func scanUnifiedResources(rows database.Rows) ([]VodItem, error) {
	defer rows.Close()
	items := make([]VodItem, 0)
	for rows.Next() {
		var item VodItem
		if err := rows.Scan(
			&item.MediaID, &item.SourceKey, &item.VodId, &item.VodName, &item.VodSub, &item.VodEn,
			&item.VodTag, &item.VodClass, &item.VodPic, &item.VodActor, &item.VodDirector,
			&item.VodBlurb, &item.VodRemarks, &item.VodPubdate, &item.VodTotal,
			&item.VodSerial, &item.VodArea, &item.VodLang, &item.VodYear,
			&item.VodDuration, &item.VodTime, &item.VodDoubanId, &item.VodContent,
			&item.VodPlayUrl, &item.TypeName, &item.LastVisitedAt, &item.AvgSpeedMs,
			&item.SampleCount, &item.FailedCount,
		); err != nil {
			return nil, fmt.Errorf("scan unified resource: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unified resources: %w", err)
	}
	return items, nil
}
