package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

type DanmakuRepository struct {
	db *gorm.DB
}

func NewDanmakuRepository(db *gorm.DB) *DanmakuRepository {
	return &DanmakuRepository{db: db}
}

// ListByVodKey 取某一集的站内弹幕。
// limit 兜底，防止某一集被刷爆之后拖垮整个播放页的响应。
func (r *DanmakuRepository) ListByVodKey(vodKey string, limit int) ([]model.Danmaku, error) {
	var list []model.Danmaku
	err := r.db.
		Where("vod_key = ? AND deleted = ?", vodKey, false).
		Order("time ASC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

// Create 保存一条弹幕
func (r *DanmakuRepository) Create(d *model.Danmaku) error {
	d.CreatedAt = time.Now()
	return r.db.Create(d).Error
}

// CountByUserSince 统计用户在指定时间之后发了多少条，用于发送频率限制
func (r *DanmakuRepository) CountByUserSince(userID int, since time.Time) (int, error) {
	var count int64
	err := r.db.Model(&model.Danmaku{}).
		Where("user_id = ? AND created_at >= ?", userID, since).
		Count(&count).Error
	return int(count), err
}

// ExistsDuplicate 判断用户是否刚在同一集发过一模一样的内容（防手抖连发和刷屏）
func (r *DanmakuRepository) ExistsDuplicate(userID int, vodKey, text string, since time.Time) (bool, error) {
	var count int64
	err := r.db.Model(&model.Danmaku{}).
		Where("user_id = ? AND vod_key = ? AND text = ? AND created_at >= ?", userID, vodKey, text, since).
		Count(&count).Error
	return count > 0, err
}

// SoftDelete 软删除。保留原记录，方便追溯发送者和后续封号
func (r *DanmakuRepository) SoftDelete(id int) error {
	return r.db.Model(&model.Danmaku{}).Where("id = ?", id).Update("deleted", true).Error
}

// ListRecent 后台审核用：按时间倒序列出最近的弹幕
func (r *DanmakuRepository) ListRecent(limit, offset int) ([]model.Danmaku, error) {
	var list []model.Danmaku
	err := r.db.
		Where("deleted = ?", false).
		Order("created_at DESC").
		Limit(limit).Offset(offset).
		Find(&list).Error
	return list, err
}
