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
func (r *SearchCacheRepository) Find(keyword, source string) (*model.SearchCache, error) {
	cache := &model.SearchCache{}
	err := r.db.QueryRow(`
		SELECT id, keyword, source, result_json, created_at, expires_at
		FROM search_cache
		WHERE keyword = $1 AND source = $2 AND expires_at > NOW()
	`, keyword, source).Scan(
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

// Upsert 创建或更新缓存
func (r *SearchCacheRepository) Upsert(keyword, source, resultJSON string, ttl time.Duration) error {
	expiresAt := time.Now().Add(ttl)

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
