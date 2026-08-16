package playback

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// ActivityPopularProvider 把站内行为转换为一个可选热门来源。
// 它只读取规范媒体和历史表；如果媒体 migration 尚不可用，组合 Provider 仍可继续使用外部来源。
type ActivityPopularProvider struct {
	database database.Executor
	mu       sync.Mutex
	cache    map[string]popularCacheEntry
}

func NewActivityPopularProvider(executor database.Executor) *ActivityPopularProvider {
	return &ActivityPopularProvider{database: executor, cache: make(map[string]popularCacheEntry)}
}

func (provider *ActivityPopularProvider) Popular(ctx context.Context, mediaType string) ([]PopularSubject, error) {
	if provider == nil || provider.database == nil {
		return nil, fmt.Errorf("activity popular store is not configured")
	}
	kind, err := activityMediaType(mediaType)
	if err != nil {
		return nil, err
	}
	provider.mu.Lock()
	cached, found := provider.cache[mediaType]
	if found && time.Now().Before(cached.expiresAt) {
		items := append([]PopularSubject(nil), cached.subjects...)
		provider.mu.Unlock()
		return items, nil
	}
	provider.mu.Unlock()

	rows, err := provider.database.Query(ctx, `WITH activity AS (
    SELECT event.media_id,
           SUM(CASE WHEN event.created_at >= NOW() - INTERVAL '6 hours' THEN 3.0
                    WHEN event.created_at >= NOW() - INTERVAL '24 hours' THEN 2.0
                    WHEN event.created_at >= NOW() - INTERVAL '3 days' THEN 1.2
                    ELSE 0.6 END) AS score
    FROM (
        SELECT media_id, attempt_id, MAX(created_at) AS created_at
        FROM playback_attempt_events
        WHERE event_type = 'played_10s'
          AND created_at >= NOW() - INTERVAL '7 days'
        GROUP BY media_id, attempt_id
    ) event
    GROUP BY event.media_id
    UNION ALL
    SELECT event.media_id,
           SUM(CASE WHEN event.created_at >= NOW() - INTERVAL '24 hours' THEN 2.0 ELSE 1.0 END) AS score
    FROM (
        SELECT media_id, attempt_id, MAX(created_at) AS created_at
        FROM playback_attempt_events
        WHERE event_type = 'ended'
          AND created_at >= NOW() - INTERVAL '7 days'
        GROUP BY media_id, attempt_id
    ) event
    GROUP BY event.media_id
    UNION ALL
    SELECT position.media_id, COUNT(DISTINCT position.user_id)::double precision *
           CASE WHEN position.completed THEN 1.5 ELSE 0.5 END AS score
    FROM playback_positions position
    WHERE position.deleted_at IS NULL
      AND position.updated_at >= NOW() - INTERVAL '7 days'
    GROUP BY position.media_id, position.completed
    UNION ALL
    SELECT m.id, COUNT(DISTINCT um.user_id)::double precision * 0.75 AS score
    FROM user_movies um
    JOIN media m ON m.douban_id = um.movie_id
    WHERE um.status IN ('wish', 'watched') AND um.updated_at >= NOW() - INTERVAL '90 days'
    GROUP BY m.id
)
SELECT m.douban_id, m.title, m.year, m.poster, m.rating_douban, m.rating_tmdb, SUM(activity.score) AS activity_score
FROM activity
JOIN media m ON m.id = activity.media_id
WHERE m.media_type = $1 AND m.douban_id <> ''
GROUP BY m.id, m.douban_id, m.title, m.year, m.poster, m.rating_douban, m.rating_tmdb, m.updated_at
ORDER BY activity_score DESC, m.updated_at DESC
LIMIT 50`, kind)
	if err != nil {
		return nil, fmt.Errorf("query activity popular: %w", err)
	}
	defer rows.Close()
	items := make([]PopularSubject, 0)
	for rows.Next() {
		var id, title, year, poster string
		var doubanRating, tmdbRating, activityScore float64
		if err := rows.Scan(&id, &title, &year, &poster, &doubanRating, &tmdbRating, &activityScore); err != nil {
			return nil, fmt.Errorf("scan activity popular: %w", err)
		}
		rate := doubanRating
		if rate <= 0 {
			rate = tmdbRating
		}
		cover := poster
		if strings.HasPrefix(cover, "http://") || strings.HasPrefix(cover, "https://") {
			cover = proxyImagePath(cover)
		}
		items = append(items, PopularSubject{
			ID: id, Title: title, Year: year, Cover: cover,
			Rate: fmt.Sprintf("%.1f", rate), URL: "/movie/" + url.PathEscape(id), Score: activityScore,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate activity popular: %w", err)
	}
	provider.mu.Lock()
	provider.cache[mediaType] = popularCacheEntry{subjects: append([]PopularSubject(nil), items...), expiresAt: time.Now().Add(5 * time.Minute)}
	provider.mu.Unlock()
	return items, nil
}

func activityMediaType(mediaType string) (string, error) {
	switch strings.TrimSpace(mediaType) {
	case "movie":
		return "movie", nil
	case "tv", "show", "cartoon":
		return "tv", nil
	default:
		return "", fmt.Errorf("unsupported activity media type %q", mediaType)
	}
}
