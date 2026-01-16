package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type HistoryRepository struct {
	db *gorm.DB
}

func NewHistoryRepository(db *gorm.DB) *HistoryRepository {
	return &HistoryRepository{db: db}
}

// Upsert 更新或插入观影记录
func (r *HistoryRepository) Upsert(h *model.WatchHistory) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "source"}, {Name: "vod_id"}, {Name: "episode"}},
		DoUpdates: clause.AssignmentColumns([]string{"progress", "last_time", "duration", "watched_at"}),
	}).Create(h).Error
}

// ListByUser 获取用户观影历史
func (r *HistoryRepository) ListByUser(userID, limit, offset int) ([]*model.WatchHistory, error) {
	var histories []*model.WatchHistory
	err := r.db.Where("user_id = ?", userID).
		Order("watched_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&histories).Error
	return histories, err
}

// GetAfter 获取指定时间之后的记录（用于同步）
func (r *HistoryRepository) GetAfter(userID int, after time.Time) ([]*model.WatchHistory, error) {
	var histories []*model.WatchHistory
	err := r.db.Where("user_id = ? AND watched_at > ?", userID, after).
		Order("watched_at DESC").
		Find(&histories).Error
	return histories, err
}

// CountByUser 统计用户观影历史数量
func (r *HistoryRepository) CountByUser(userID int) (int, error) {
	var count int64
	err := r.db.Model(&model.WatchHistory{}).Where("user_id = ?", userID).Count(&count).Error
	return int(count), err
}

// Delete 删除观影记录
func (r *HistoryRepository) Delete(userID int, id int) error {
	return r.db.Where("user_id = ? AND id = ?", userID, id).Delete(&model.WatchHistory{}).Error
}
