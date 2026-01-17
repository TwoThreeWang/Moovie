package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

type FeedbackRepository struct {
	db *gorm.DB
}

func NewFeedbackRepository(db *gorm.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

// Create 创建反馈
func (r *FeedbackRepository) Create(f *model.Feedback) error {
	f.Status = "pending"
	f.CreatedAt = time.Now()
	return r.db.Create(f).Error
}

// List 获取反馈列表（管理后台用）
func (r *FeedbackRepository) List(status string, limit, offset int) ([]*model.Feedback, error) {
	var feedbacks []*model.Feedback
	db := r.db.Model(&model.Feedback{})

	if status != "" {
		db = db.Where("status = ?", status)
	}

	err := db.Order("created_at DESC").Limit(limit).Offset(offset).Find(&feedbacks).Error
	return feedbacks, err
}

// UpdateStatus 更新反馈状态
func (r *FeedbackRepository) UpdateStatus(id int, status string) error {
	return r.db.Model(&model.Feedback{}).Where("id = ?", id).Update("status", status).Error
}

// Reply 回复反馈
func (r *FeedbackRepository) Reply(id int, reply string) error {
	now := time.Now()
	return r.db.Model(&model.Feedback{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     "resolved",
		"reply":      reply,
		"replied_at": &now,
	}).Error
}

// CountPending 获取待处理反馈数量
func (r *FeedbackRepository) CountPending() (int64, error) {
	var count int64
	err := r.db.Model(&model.Feedback{}).Where("status = ?", "pending").Count(&count).Error
	return count, err
}

// FindByID 根据ID查找反馈
func (r *FeedbackRepository) FindByID(id int) (*model.Feedback, error) {
	var feedback model.Feedback
	err := r.db.First(&feedback, id).Error
	if err != nil {
		return nil, err
	}
	return &feedback, nil
}

// ListPublic 获取公开反馈列表（用于前台页面展示）
// 只返回已处理(resolved)的反馈，按创建时间倒序
func (r *FeedbackRepository) ListPublic(limit, offset int) ([]*model.Feedback, error) {
	var feedbacks []*model.Feedback
	err := r.db.Model(&model.Feedback{}).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&feedbacks).Error
	return feedbacks, err
}

// CountPublic 统计公开反馈总数
func (r *FeedbackRepository) CountPublic() (int64, error) {
	var count int64
	err := r.db.Model(&model.Feedback{}).Count(&count).Error
	return count, err
}
