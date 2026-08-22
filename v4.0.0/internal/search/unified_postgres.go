package search

import (
	"context"
	"fmt"
	"sort"
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
       COALESCE(media.rating_douban, 0), COALESCE(LEFT(media.summary, 120), ''),
       media.genres, media.countries, media.directors, media.actors, media.duration
FROM media
WHERE media.douban_id <> '' AND (media.title ILIKE $1 OR media.original_title ILIKE $1 OR EXISTS (
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
LIMIT $6`, pattern, normalizedPattern, strings.TrimSpace(query.Year), normalizeMediaType(query.MediaType), strings.TrimSpace(query.Keyword), query.Limit)
	if err != nil {
		return nil, fmt.Errorf("search unified media: %w", err)
	}
	defer rows.Close()
	items := make([]UnifiedItem, 0)
	for rows.Next() {
		var item UnifiedItem
		if err := rows.Scan(&item.MediaID, &item.Title, &item.OriginalTitle, &item.SearchAliases, &item.Year, &item.MediaType, &item.Poster, &item.DoubanID, &item.RatingDouban, &item.Summary,
			&item.Genres, &item.Countries, &item.Directors, &item.Actors, &item.Duration); err != nil {
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

// ListUnifiedResources 批量取出这些媒体下挂着的有效播放资源。
func (store *PostgresStore) ListUnifiedResources(ctx context.Context, mediaIDs []int) ([]VodItem, error) {
	if len(mediaIDs) == 0 {
		return []VodItem{}, nil
	}
	identifiers := make([]int64, len(mediaIDs))
	for index, mediaID := range mediaIDs {
		identifiers[index] = int64(mediaID)
	}
	// 别名必须叫 media_link：vodItemColumns 末尾三列读的就是 media_link.*。
	// 这里已经 JOIN 了 resource_media_links，所以不再叠 resourceMediaLinkJoin
	// 那个 LATERAL——同一张表查两遍纯属浪费。
	rows, err := store.database.Query(ctx, `SELECT media_link.media_id, `+vodItemColumns+`,
	CASE WHEN EXISTS (
	    SELECT 1 FROM resource_episode_candidates candidate
	    JOIN resource_play_lines line ON line.id = candidate.line_id
	    WHERE candidate.media_id = media_link.media_id
	      AND line.source_key = media_link.source_key AND line.vod_id = media_link.vod_id
	      AND candidate.resource_status NOT IN ('retired', 'deleted')
	      AND line.resource_status NOT IN ('retired', 'deleted')
	      AND COALESCE(candidate.play_url, '') <> ''
	) THEN 'ready' ELSE 'direct' END
FROM resource_media_links media_link
JOIN vod_items resource ON resource.source_key = media_link.source_key AND resource.vod_id = media_link.vod_id
WHERE media_link.media_id = ANY($1::bigint[])
  AND COALESCE(resource.resource_status, 'active') <> 'removed'
  AND COALESCE(resource.vod_play_url, '') <> ''
ORDER BY media_link.media_id, resource.last_visited_at DESC`, identifiers)
	if err != nil {
		return nil, fmt.Errorf("list unified resources: %w", err)
	}
	return scanUnifiedResources(rows)
}

// ListPlaybackSummaries 批量聚合媒体播放状态，供搜索缓存、详情页和首页入口共用。
func (store *PostgresStore) ListPlaybackSummaries(ctx context.Context, mediaIDs []int) (map[int]PlaybackSummary, error) {
	summaries := make(map[int]PlaybackSummary, len(mediaIDs))
	for _, mediaID := range mediaIDs {
		summaries[mediaID] = PlaybackSummary{MediaID: mediaID, State: PlaybackNone, Resources: []VodItem{}}
	}
	resources, err := store.ListUnifiedResources(ctx, mediaIDs)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		summary := summaries[resource.MediaID]
		summary.MediaID = resource.MediaID
		summary.Resources = append(summary.Resources, resource)
		summaries[resource.MediaID] = summary
	}
	for mediaID, summary := range summaries {
		sort.SliceStable(summary.Resources, func(left, right int) bool {
			return resourceIsBetter(newUnifiedResource(summary.Resources[left]), newUnifiedResource(summary.Resources[right]))
		})
		summary.ResourceCount = len(summary.Resources)
		if summary.ResourceCount > 0 {
			best := summary.Resources[0]
			summary.BestResource, summary.State = &best, playbackState(best)
		}
		summaries[mediaID] = summary
	}
	return summaries, nil
}

// PlaybackSummary 返回单部媒体的统一播放摘要。
func (store *PostgresStore) PlaybackSummary(ctx context.Context, mediaID int) (PlaybackSummary, error) {
	summaries, err := store.ListPlaybackSummaries(ctx, []int{mediaID})
	if err != nil {
		return PlaybackSummary{}, err
	}
	return summaries[mediaID], nil
}

// scanUnifiedResources 比 scanVodItems 多扫一列 media_id（查询里在最前面）。
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
			&item.SampleCount, &item.FailedCount, &item.ResourceStatus,
			&item.MediaID, &item.MediaConfidence, &item.MediaMatch, &item.PlaybackState,
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
