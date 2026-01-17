package repository

import (
	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

// CopyrightFilterRepository 版权过滤仓库
type CopyrightFilterRepository struct {
	db *gorm.DB
}

// NewCopyrightFilterRepository 创建版权过滤仓库
func NewCopyrightFilterRepository(db *gorm.DB) *CopyrightFilterRepository {
	return &CopyrightFilterRepository{db: db}
}

// Create 创建版权关键词
func (r *CopyrightFilterRepository) Create(filter *model.CopyrightFilter) error {
	return r.db.Create(filter).Error
}

// Update 更新版权关键词
func (r *CopyrightFilterRepository) Update(filter *model.CopyrightFilter) error {
	return r.db.Save(filter).Error
}

// Delete 删除版权关键词
func (r *CopyrightFilterRepository) Delete(id uint) error {
	return r.db.Delete(&model.CopyrightFilter{}, id).Error
}

// FindByID 根据 ID 查找
func (r *CopyrightFilterRepository) FindByID(id uint) (*model.CopyrightFilter, error) {
	var filter model.CopyrightFilter
	err := r.db.First(&filter, id).Error
	if err != nil {
		return nil, err
	}
	return &filter, nil
}

// ListAll 获取所有版权关键词
func (r *CopyrightFilterRepository) ListAll() ([]*model.CopyrightFilter, error) {
	var filters []*model.CopyrightFilter
	err := r.db.Order("created_at DESC").Find(&filters).Error
	return filters, err
}

// GetAllKeywords 获取所有关键词列表（用于搜索过滤）
func (r *CopyrightFilterRepository) GetAllKeywords() ([]string, error) {
	var keywords []string
	err := r.db.Model(&model.CopyrightFilter{}).Pluck("keyword", &keywords).Error
	return keywords, err
}
