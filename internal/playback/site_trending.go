package playback

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// SiteTrendingProvider 从 playback_attempt_events 统计最近 7 天播放次数最多的影片。
type SiteTrendingProvider struct {
	db database.Executor
}

func NewSiteTrendingProvider(db database.Executor) *SiteTrendingProvider {
	return &SiteTrendingProvider{db: db}
}

func (p *SiteTrendingProvider) Popular(ctx context.Context, _ string) ([]PopularSubject, error) {
	rows, err := p.db.Query(ctx, `
		SELECT m.douban_id, m.title, m.poster, m.rating_douban, m.year
		FROM playback_attempt_events e
		JOIN media m ON m.id = e.media_id
		WHERE e.event_type = 'played_10s'
		  AND e.created_at > NOW() - INTERVAL '7 days'
		  AND m.douban_id <> ''
		GROUP BY m.id, m.douban_id, m.title, m.poster, m.rating_douban, m.year
		ORDER BY COUNT(*) DESC
		LIMIT 50`)
	if err != nil {
		return nil, fmt.Errorf("site trending query: %w", err)
	}
	defer rows.Close()

	var subjects []PopularSubject
	for rows.Next() {
		var doubanID, title, poster, year string
		var rating float64
		if err := rows.Scan(&doubanID, &title, &poster, &rating, &year); err != nil {
			return nil, fmt.Errorf("site trending scan: %w", err)
		}
		rate := ""
		if rating > 0 {
			rate = fmt.Sprintf("%.1f", rating)
		}
		subjects = append(subjects, PopularSubject{
			ID:    doubanID,
			Title: title,
			Cover: proxyImagePath(poster),
			Rate:  rate,
			Year:  year,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("site trending rows: %w", err)
	}

	return subjects, nil
}
