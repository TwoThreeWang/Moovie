package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SiteStatRepository 采集站点健康度统计仓库
type SiteStatRepository struct {
	db *gorm.DB
}

// NewSiteStatRepository 创建统计仓库
func NewSiteStatRepository(db *gorm.DB) *SiteStatRepository {
	return &SiteStatRepository{db: db}
}

// AddBatch 批量累加统计。
// 冲突时在数据库侧做加法，因此多实例同时写同一个小时桶也不会互相覆盖。
func (r *SiteStatRepository) AddBatch(stats []*model.SiteStat) error {
	if len(stats) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "site_key"}, {Name: "bucket"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"ok_count":      gorm.Expr("site_stats.ok_count + excluded.ok_count"),
			"empty_count":   gorm.Expr("site_stats.empty_count + excluded.empty_count"),
			"timeout_count": gorm.Expr("site_stats.timeout_count + excluded.timeout_count"),
			"error_count":   gorm.Expr("site_stats.error_count + excluded.error_count"),
			"total_ms":      gorm.Expr("site_stats.total_ms + excluded.total_ms"),
		}),
	}).Create(&stats).Error
}

// SummarySince 汇总 since 之后的统计，按站点分组
func (r *SiteStatRepository) SummarySince(since time.Time) (map[string]*model.SiteStatSummary, error) {
	var rows []*model.SiteStatSummary
	err := r.db.Model(&model.SiteStat{}).
		Select(`site_key,
			COALESCE(SUM(ok_count), 0)      AS ok_count,
			COALESCE(SUM(empty_count), 0)   AS empty_count,
			COALESCE(SUM(timeout_count), 0) AS timeout_count,
			COALESCE(SUM(error_count), 0)   AS error_count,
			COALESCE(SUM(total_ms), 0)      AS total_ms`).
		Where("bucket >= ?", since).
		Group("site_key").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]*model.SiteStatSummary, len(rows))
	for _, row := range rows {
		result[row.SiteKey] = row
	}
	return result, nil
}

// ListBuckets 取某站点最近 N 个小时的分时明细（时间正序），用于趋势排查
func (r *SiteStatRepository) ListBuckets(siteKey string, since time.Time) ([]*model.SiteStat, error) {
	var stats []*model.SiteStat
	err := r.db.Where("site_key = ? AND bucket >= ?", siteKey, since).
		Order("bucket ASC").
		Find(&stats).Error
	return stats, err
}

// DeleteBefore 删除 before 之前的统计，返回删除行数
func (r *SiteStatRepository) DeleteBefore(before time.Time) (int64, error) {
	res := r.db.Where("bucket < ?", before).Delete(&model.SiteStat{})
	return res.RowsAffected, res.Error
}
