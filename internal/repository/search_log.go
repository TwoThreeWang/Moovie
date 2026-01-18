package repository

import (
	"fmt"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/utils"
	"gorm.io/gorm"
)

type SearchLogRepository struct {
	db *gorm.DB
}

func NewSearchLogRepository(db *gorm.DB) *SearchLogRepository {
	return &SearchLogRepository{db: db}
}

// Log 记录搜索日志
func (r *SearchLogRepository) Log(keyword string, userID *int, ipHash string) error {
	// 1. 记录原始日志
	log := &model.SearchLog{
		Keyword:   keyword,
		UserID:    userID,
		IPHash:    ipHash,
		CreatedAt: time.Now(),
	}
	if err := r.db.Create(log).Error; err != nil {
		return err
	}

	// 2. 更新热搜关键词统计表
	return r.db.Exec(`
		INSERT INTO trending_keywords (keyword, count, last_searched_at)
		VALUES ($1, 1, NOW())
		ON CONFLICT (keyword) DO UPDATE SET
			count = trending_keywords.count + 1,
			last_searched_at = EXCLUDED.last_searched_at
	`, keyword).Error
}

// GetTrending 获取热搜关键词
func (r *SearchLogRepository) GetTrending(hours, limit int) ([]*model.TrendingKeyword, error) {
	// 1. 检查缓存
	cacheKey := fmt.Sprintf("trending:%d:%d", hours, limit)
	if cached, found := utils.CacheGet(cacheKey); found {
		if keywords, ok := cached.([]*model.TrendingKeyword); ok {
			return keywords, nil
		}
	}

	var keywords []*model.TrendingKeyword

	// 2. 从数据库获取
	// 如果 hours > 0，仍然从 search_logs 实时计算（为了支持不同时间范围的趋势）
	// 如果 hours <= 0，从 trending_keywords 表获取（全量统计）
	var err error
	if hours > 0 {
		err = r.db.Raw(`
			SELECT keyword, COUNT(*) as count, MAX(created_at) as last_searched_at
			FROM search_logs
			WHERE created_at > NOW() - INTERVAL '1 hour' * $1
			GROUP BY keyword
			ORDER BY count DESC
			LIMIT $2
		`, hours, limit).Scan(&keywords).Error
	} else {
		// 从汇总表获取
		err = r.db.Table("trending_keywords").
			Select("keyword, count, last_searched_at").
			Order("count DESC").
			Limit(limit).
			Scan(&keywords).Error
	}

	if err != nil {
		return nil, err
	}

	// 3. 设置缓存
	duration := 30 * time.Minute
	utils.CacheSet(cacheKey, keywords, duration)

	return keywords, nil
}

// DeleteOldKeywords 清理超过指定天数未搜索的关键词
func (r *SearchLogRepository) DeleteOldKeywords(days int) (int64, error) {
	result := r.db.Exec(`
		DELETE FROM trending_keywords
		WHERE last_searched_at < NOW() - INTERVAL '1 day' * $1
	`, days)
	return result.RowsAffected, result.Error
}

// DeleteOldLogs 清理超过指定天数的原始搜索日志
func (r *SearchLogRepository) DeleteOldLogs(days int) (int64, error) {
	result := r.db.Exec(`
		DELETE FROM search_logs
		WHERE created_at < NOW() - INTERVAL '1 day' * $1
	`, days)
	return result.RowsAffected, result.Error
}
