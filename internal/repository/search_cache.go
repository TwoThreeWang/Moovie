package repository

import (
	"database/sql"
	"time"

	"github.com/user/moovie/internal/model"
)

type SearchCacheRepository struct {
	db *sql.DB
}

func NewSearchCacheRepository(db *sql.DB) *SearchCacheRepository {
	return &SearchCacheRepository{db: db}
}

// Find 查找缓存（未过期）
func (r *SearchCacheRepository) Find(keyword string) (*model.SearchCache, error) {
	cache := &model.SearchCache{}
	err := r.db.QueryRow(`
		SELECT id, keyword, source, result_json, created_at, expires_at
		FROM search_cache
		WHERE keyword = $1 AND expires_at > NOW()
		LIMIT 1
	`, keyword).Scan(
		&cache.ID, &cache.Keyword, &cache.Source,
		&cache.ResultJSON, &cache.CreatedAt, &cache.ExpiresAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return cache, nil
}

// FindWithExpiry 查找缓存，返回缓存及是否过期
// 即使过期也返回缓存，用于"先返回旧数据"策略
func (r *SearchCacheRepository) FindWithExpiry(keyword string) (*model.SearchCache, bool, error) {
	cache := &model.SearchCache{}
	err := r.db.QueryRow(`
		SELECT id, keyword, source, result_json, created_at, expires_at
		FROM search_cache
		WHERE keyword = $1
		ORDER BY created_at DESC
		LIMIT 1
	`, keyword).Scan(
		&cache.ID, &cache.Keyword, &cache.Source,
		&cache.ResultJSON, &cache.CreatedAt, &cache.ExpiresAt,
	)

	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	isExpired := cache.ExpiresAt.Before(time.Now())
	return cache, isExpired, nil
}

// Upsert 创建或更新缓存（按keyword唯一）
func (r *SearchCacheRepository) Upsert(keyword, resultJSON string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)
	source := "vod" // 资源网搜索固定使用 "vod" 作为 source

	_, err := r.db.Exec(`
		INSERT INTO search_cache (keyword, source, result_json, created_at, expires_at)
		VALUES ($1, $2, $3, NOW(), $4)
		ON CONFLICT (keyword, source) DO UPDATE SET
			result_json = EXCLUDED.result_json,
			expires_at = EXCLUDED.expires_at
	`, keyword, source, resultJSON, expiresAt)

	return err
}

// CleanExpired 清理过期缓存
func (r *SearchCacheRepository) CleanExpired() (int64, error) {
	result, err := r.db.Exec(`DELETE FROM search_cache WHERE expires_at < NOW()`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
