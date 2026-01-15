package repository

import (
	"database/sql"
	"time"

	"github.com/user/moovie/internal/model"
)

// SiteRepository 站点配置仓库
type SiteRepository struct {
	db *sql.DB
}

// NewSiteRepository 创建站点仓库
func NewSiteRepository(db *sql.DB) *SiteRepository {
	return &SiteRepository{db: db}
}

// Create 创建站点
func (r *SiteRepository) Create(site *model.Site) error {
	now := time.Now().Unix()
	err := r.db.QueryRow(`
		INSERT INTO sites (key, base_url, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, site.Key, site.BaseUrl, site.Enabled, now, now).Scan(&site.ID)
	return err
}

// Update 更新站点
func (r *SiteRepository) Update(site *model.Site) error {
	now := time.Now().Unix()
	_, err := r.db.Exec(`
		UPDATE sites
		SET key = $1, base_url = $2, enabled = $3, updated_at = $4
		WHERE id = $5
	`, site.Key, site.BaseUrl, site.Enabled, now, site.ID)
	return err
}

// Delete 物理删除站点
func (r *SiteRepository) Delete(id uint) error {
	_, err := r.db.Exec(`DELETE FROM sites WHERE id = $1`, id)
	return err
}

// FindByKey 根据Key查找站点
func (r *SiteRepository) FindByKey(key string) (*model.Site, error) {
	site := &model.Site{}
	err := r.db.QueryRow(`
		SELECT id, key, base_url, enabled, created_at, updated_at
		FROM sites
		WHERE key = $1
	`, key).Scan(&site.ID, &site.Key, &site.BaseUrl, &site.Enabled, &site.CreatedAt, &site.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return site, nil
}

// ListEnabled 获取所有启用的站点
func (r *SiteRepository) ListEnabled() ([]*model.Site, error) {
	rows, err := r.db.Query(`
		SELECT id, key, base_url, enabled, created_at, updated_at
		FROM sites
		WHERE enabled = true
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []*model.Site
	for rows.Next() {
		site := &model.Site{}
		if err := rows.Scan(&site.ID, &site.Key, &site.BaseUrl, &site.Enabled, &site.CreatedAt, &site.UpdatedAt); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, nil
}

// ListAll 获取所有站点
func (r *SiteRepository) ListAll() ([]*model.Site, error) {
	rows, err := r.db.Query(`
		SELECT id, key, base_url, enabled, created_at, updated_at
		FROM sites
		ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sites []*model.Site
	for rows.Next() {
		site := &model.Site{}
		if err := rows.Scan(&site.ID, &site.Key, &site.BaseUrl, &site.Enabled, &site.CreatedAt, &site.UpdatedAt); err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	return sites, nil
}
