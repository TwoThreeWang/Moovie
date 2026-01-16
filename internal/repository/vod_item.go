package repository

import (
	"errors"
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type VodItemRepository struct {
	db *gorm.DB
}

func NewVodItemRepository(db *gorm.DB) *VodItemRepository {
	return &VodItemRepository{db: db}
}

// Upsert 更新或插入视频数据，并更新最后访问时间
func (r *VodItemRepository) Upsert(item *model.VodItem) error {
	if item.LastVisitedAt.IsZero() {
		item.LastVisitedAt = time.Now()
	}

	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_key"}, {Name: "vod_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"vod_name", "vod_sub", "vod_remarks", "vod_time", "vod_play_url", "last_visited_at",
		}),
	}).Create(item).Error
}

// FindBySourceId 根据来源和ID查找视频
func (r *VodItemRepository) FindBySourceId(sourceKey, vodId string) (*model.VodItem, error) {
	var item model.VodItem
	err := r.db.Where("source_key = ? AND vod_id = ?", sourceKey, vodId).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 每次查询时更新最后访问时间
	r.UpdateLastVisited(sourceKey, vodId)

	return &item, nil
}

// Search 根据关键词搜索视频（匹配名称、副标题、英文名）
func (r *VodItemRepository) Search(keyword string) ([]model.VodItem, error) {
	var items []model.VodItem
	err := r.db.Where("vod_name LIKE ? OR vod_sub LIKE ? OR vod_en LIKE ?", 
		"%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%").
		Order("last_visited_at DESC").
		Find(&items).Error
	return items, err
}

// UpdateLastVisited 更新最后访问时间
func (r *VodItemRepository) UpdateLastVisited(sourceKey, vodId string) error {
	return r.db.Model(&model.VodItem{}).
		Where("source_key = ? AND vod_id = ?", sourceKey, vodId).
		Update("last_visited_at", time.Now()).Error
}

// DeleteInactive 删除指定天数内未访问的数据
func (r *VodItemRepository) DeleteInactive(days int) (int64, error) {
	result := r.db.Where("last_visited_at < NOW() - (? || ' days')::interval", days).
		Delete(&model.VodItem{})
	return result.RowsAffected, result.Error
}
