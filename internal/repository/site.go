package repository

import (
	"errors"
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

// SiteRepository 站点配置仓库
type SiteRepository struct {
	db *gorm.DB
}

// NewSiteRepository 创建站点仓库
func NewSiteRepository(db *gorm.DB) *SiteRepository {
	return &SiteRepository{db: db}
}

// Create 创建站点
func (r *SiteRepository) Create(site *model.Site) error {
	now := time.Now().Unix()
	site.CreatedAt = now
	site.UpdatedAt = now
	return r.db.Create(site).Error
}

// Update 更新站点
func (r *SiteRepository) Update(site *model.Site) error {
	site.UpdatedAt = time.Now().Unix()
	return r.db.Save(site).Error
}

// Delete 物理删除站点
func (r *SiteRepository) Delete(id uint) error {
	return r.db.Delete(&model.Site{}, id).Error
}

// FindByKey 根据Key查找站点
func (r *SiteRepository) FindByKey(key string) (*model.Site, error) {
	var site model.Site
	err := r.db.Where("key = ?", key).First(&site).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &site, err
}

// ListEnabled 获取所有启用的站点
func (r *SiteRepository) ListEnabled() ([]*model.Site, error) {
	var sites []*model.Site
	err := r.db.Where("enabled = ?", true).Order("id").Find(&sites).Error
	return sites, err
}

// ListAll 获取所有站点
func (r *SiteRepository) ListAll() ([]*model.Site, error) {
	var sites []*model.Site
	err := r.db.Order("id").Find(&sites).Error
	return sites, err
}
