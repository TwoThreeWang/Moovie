package repository

import (
	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

// CategoryFilterRepository 分类过滤仓库
type CategoryFilterRepository struct {
	db *gorm.DB
}

// NewCategoryFilterRepository 创建分类过滤仓库
func NewCategoryFilterRepository(db *gorm.DB) *CategoryFilterRepository {
	return &CategoryFilterRepository{db: db}
}

// Create 创建分类过滤关键词
func (r *CategoryFilterRepository) Create(filter *model.CategoryFilter) error {
	return r.db.Create(filter).Error
}

// Delete 删除分类过滤关键词
func (r *CategoryFilterRepository) Delete(id uint) error {
	return r.db.Delete(&model.CategoryFilter{}, id).Error
}

// ListAll 获取所有分类过滤关键词
func (r *CategoryFilterRepository) ListAll() ([]*model.CategoryFilter, error) {
	var filters []*model.CategoryFilter
	err := r.db.Order("created_at DESC").Find(&filters).Error
	return filters, err
}

// GetAllKeywords 获取所有关键词列表
func (r *CategoryFilterRepository) GetAllKeywords() ([]string, error) {
	var keywords []string
	err := r.db.Model(&model.CategoryFilter{}).Pluck("keyword", &keywords).Error
	return keywords, err
}
