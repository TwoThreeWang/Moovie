package repository

import (
	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

// UpdateLoadSpeedBySource 根据SourceKey和VodId更新视频加载速度（增量平均算法）
func (r *MovieRepository) UpdateLoadSpeedBySource(sourceKey string, vodID string, loadTime int) error {
	// 使用COALESCE处理null值，如果为null则使用0作为默认值
	return r.DB.Model(&model.VodItem{}).
		Where("source_key = ? AND vod_id = ?", sourceKey, vodID).
		UpdateColumn("avg_speed_ms", gorm.Expr("(COALESCE(avg_speed_ms, 0) * COALESCE(sample_count, 0) + ?) / (COALESCE(sample_count, 0) + 1)", loadTime)).
		UpdateColumn("sample_count", gorm.Expr("COALESCE(sample_count, 0) + 1")).Error
}

// IncrementFailedCountBySource 根据SourceKey和VodId增加失败计数
func (r *MovieRepository) IncrementFailedCountBySource(sourceKey string, vodID string) error {
	// 使用COALESCE处理null值，如果为null则使用0作为默认值
	return r.DB.Model(&model.VodItem{}).
		Where("source_key = ? AND vod_id = ?", sourceKey, vodID).
		UpdateColumn("failed_count", gorm.Expr("COALESCE(failed_count, 0) + 1")).Error
}
