package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

type DoubanSyncJobRepository struct {
	db *gorm.DB
}

func NewDoubanSyncJobRepository(db *gorm.DB) *DoubanSyncJobRepository {
	return &DoubanSyncJobRepository{db: db}
}

// Create 创建同步任务
func (r *DoubanSyncJobRepository) Create(job *model.DoubanSyncJob) error {
	if job.Status == "" {
		job.Status = model.DoubanSyncStatusPending
	}
	if job.SyncType == "" {
		job.SyncType = model.DoubanSyncTypeFull
	}
	job.CreatedAt = time.Now()
	job.UpdatedAt = time.Now()
	return r.db.Create(job).Error
}

// GetByID 根据 ID 查询任务
func (r *DoubanSyncJobRepository) GetByID(id int) (*model.DoubanSyncJob, error) {
	var job model.DoubanSyncJob
	err := r.db.First(&job, id).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetLatestByUser 获取用户最新的同步任务
func (r *DoubanSyncJobRepository) GetLatestByUser(userID int) (*model.DoubanSyncJob, error) {
	var job model.DoubanSyncJob
	err := r.db.Where("user_id = ?", userID).Order("id DESC").First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// UpdateStatus 更新任务状态
func (r *DoubanSyncJobRepository) UpdateStatus(id int, status model.DoubanSyncStatus, message string) error {
	updates := map[string]interface{}{
		"status":        status,
		"error_message": message,
		"updated_at":    time.Now(),
	}
	if status == model.DoubanSyncStatusRunning {
		now := time.Now()
		updates["started_at"] = &now
	}
	if status == model.DoubanSyncStatusCompleted || status == model.DoubanSyncStatusFailed {
		now := time.Now()
		updates["finished_at"] = &now
	}
	return r.db.Model(&model.DoubanSyncJob{}).Where("id = ?", id).Updates(updates).Error
}

// UpdateProgress 更新任务进度
func (r *DoubanSyncJobRepository) UpdateProgress(id int, processed, failed int, cursor string) error {
	return r.db.Model(&model.DoubanSyncJob{}).Where("id = ?", id).Updates(map[string]interface{}{
		"processed":   processed,
		"failed_count": failed,
		"cursor":      cursor,
		"updated_at":  time.Now(),
	}).Error
}

// UpdateTotal 更新任务总数
func (r *DoubanSyncJobRepository) UpdateTotal(id int, total int) error {
	return r.db.Model(&model.DoubanSyncJob{}).Where("id = ?", id).Updates(map[string]interface{}{
		"total":      total,
		"updated_at": time.Now(),
	}).Error
}

// ListPending 获取待执行的同步任务，按创建时间升序
func (r *DoubanSyncJobRepository) ListPending(limit int) ([]*model.DoubanSyncJob, error) {
	var jobs []*model.DoubanSyncJob
	err := r.db.Where("status = ?", model.DoubanSyncStatusPending).
		Order("id ASC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// ListFailedBefore 获取指定时间之前失败的同步任务
func (r *DoubanSyncJobRepository) ListFailedBefore(before time.Time, limit int) ([]*model.DoubanSyncJob, error) {
	var jobs []*model.DoubanSyncJob
	err := r.db.Where("status = ? AND updated_at < ?", model.DoubanSyncStatusFailed, before).
		Order("id ASC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// HasRunningJob 检查用户是否有正在运行的同步任务
func (r *DoubanSyncJobRepository) HasRunningJob(userID int) (bool, error) {
	var count int64
	err := r.db.Model(&model.DoubanSyncJob{}).
		Where("user_id = ? AND status = ?", userID, model.DoubanSyncStatusRunning).
		Count(&count).Error
	return count > 0, err
}
