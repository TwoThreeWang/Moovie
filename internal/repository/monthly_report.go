package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

type MonthlyReportRepository struct {
	db *gorm.DB
}

func NewMonthlyReportRepository(db *gorm.DB) *MonthlyReportRepository {
	return &MonthlyReportRepository{db: db}
}

// Upsert 创建或更新月度报告
func (r *MonthlyReportRepository) Upsert(report *model.MonthlyReport) error {
	report.UpdatedAt = time.Now()
	if report.CreatedAt.IsZero() {
		report.CreatedAt = time.Now()
	}
	return r.db.Where("user_id = ? AND year_month = ?", report.UserID, report.YearMonth).
		Assign(report).
		FirstOrCreate(report).Error
}

// GetByUserAndMonth 根据用户 ID 和月份获取报告
func (r *MonthlyReportRepository) GetByUserAndMonth(userID int, yearMonth string) (*model.MonthlyReport, error) {
	var report model.MonthlyReport
	err := r.db.Where("user_id = ? AND year_month = ?", userID, yearMonth).First(&report).Error
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// GetLatestByUser 获取用户最新月度报告
func (r *MonthlyReportRepository) GetLatestByUser(userID int) (*model.MonthlyReport, error) {
	var report model.MonthlyReport
	err := r.db.Where("user_id = ? AND status = ?", userID, model.MonthlyReportStatusGenerated).
		Order("year_month DESC").
		First(&report).Error
	if err != nil {
		return nil, err
	}
	return &report, nil
}

// GetByUserAndDateRange 根据用户 ID 和日期范围获取报告列表
func (r *MonthlyReportRepository) GetByUserAndDateRange(userID int, startMonth, endMonth string) ([]*model.MonthlyReport, error) {
	var reports []*model.MonthlyReport
	err := r.db.Where("user_id = ? AND year_month BETWEEN ? AND ?", userID, startMonth, endMonth).
		Order("year_month DESC").
		Find(&reports).Error
	return reports, err
}

// ListByUser 获取用户所有月度报告
func (r *MonthlyReportRepository) ListByUser(userID int, limit, offset int) ([]*model.MonthlyReport, error) {
	var reports []*model.MonthlyReport
	err := r.db.Where("user_id = ? AND status = ?", userID, model.MonthlyReportStatusGenerated).
		Order("year_month DESC").
		Limit(limit).
		Offset(offset).
		Find(&reports).Error
	return reports, err
}

// GetPendingReports 获取所有待生成的报告
func (r *MonthlyReportRepository) GetPendingReports(limit int) ([]*model.MonthlyReport, error) {
	var reports []*model.MonthlyReport
	err := r.db.Where("status = ?", model.MonthlyReportStatusPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&reports).Error
	return reports, err
}

// UpdateStatus 更新报告状态
func (r *MonthlyReportRepository) UpdateStatus(id int, status model.MonthlyReportStatus, errMsg string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if status == model.MonthlyReportStatusGenerated {
		now := time.Now()
		updates["generated_at"] = &now
	}
	return r.db.Model(&model.MonthlyReport{}).Where("id = ?", id).Updates(updates).Error
}

// Exists 检查报告是否已存在
func (r *MonthlyReportRepository) Exists(userID int, yearMonth string) (bool, error) {
	var count int64
	err := r.db.Model(&model.MonthlyReport{}).Where("user_id = ? AND year_month = ?", userID, yearMonth).Count(&count).Error
	return count > 0, err
}
