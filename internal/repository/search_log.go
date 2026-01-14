package repository

import (
	"database/sql"

	"github.com/user/moovie/internal/model"
)

type SearchLogRepository struct {
	db *sql.DB
}

func NewSearchLogRepository(db *sql.DB) *SearchLogRepository {
	return &SearchLogRepository{db: db}
}

// Log 记录搜索日志
func (r *SearchLogRepository) Log(keyword string, userID *int, ipHash string) error {
	_, err := r.db.Exec(`
		INSERT INTO search_logs (keyword, user_id, ip_hash, created_at)
		VALUES ($1, $2, $3, NOW())
	`, keyword, userID, ipHash)
	return err
}

// GetTrending 获取热搜关键词
func (r *SearchLogRepository) GetTrending(hours, limit int) ([]*model.TrendingKeyword, error) {
	rows, err := r.db.Query(`
		SELECT keyword, COUNT(*) as count
		FROM search_logs
		WHERE created_at > NOW() - INTERVAL '1 hour' * $1
		GROUP BY keyword
		ORDER BY count DESC
		LIMIT $2
	`, hours, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keywords []*model.TrendingKeyword
	for rows.Next() {
		k := &model.TrendingKeyword{}
		err := rows.Scan(&k.Keyword, &k.Count)
		if err != nil {
			return nil, err
		}
		keywords = append(keywords, k)
	}

	return keywords, nil
}
