package repository

import (
	"database/sql"
	"time"

	"github.com/user/moovie/internal/model"
)

type HistoryRepository struct {
	db *sql.DB
}

func NewHistoryRepository(db *sql.DB) *HistoryRepository {
	return &HistoryRepository{db: db}
}

// Upsert 更新或插入观影记录
func (r *HistoryRepository) Upsert(h *model.WatchHistory) error {
	_, err := r.db.Exec(`
		INSERT INTO watch_history (user_id, douban_id, title, poster, episode, progress, source, watched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, douban_id, episode) DO UPDATE SET
			progress = EXCLUDED.progress,
			watched_at = EXCLUDED.watched_at
	`, h.UserID, h.DoubanID, h.Title, h.Poster, h.Episode, h.Progress, h.Source, h.WatchedAt)
	return err
}

// ListByUser 获取用户观影历史
func (r *HistoryRepository) ListByUser(userID, limit, offset int) ([]*model.WatchHistory, error) {
	rows, err := r.db.Query(`
		SELECT id, user_id, douban_id, title, poster, episode, progress, source, watched_at
		FROM watch_history
		WHERE user_id = $1
		ORDER BY watched_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var histories []*model.WatchHistory
	for rows.Next() {
		h := &model.WatchHistory{}
		err := rows.Scan(&h.ID, &h.UserID, &h.DoubanID, &h.Title, &h.Poster, &h.Episode, &h.Progress, &h.Source, &h.WatchedAt)
		if err != nil {
			return nil, err
		}
		histories = append(histories, h)
	}

	return histories, nil
}

// GetAfter 获取指定时间之后的记录（用于同步）
func (r *HistoryRepository) GetAfter(userID int, after time.Time) ([]*model.WatchHistory, error) {
	rows, err := r.db.Query(`
		SELECT id, user_id, douban_id, title, poster, episode, progress, source, watched_at
		FROM watch_history
		WHERE user_id = $1 AND watched_at > $2
		ORDER BY watched_at DESC
	`, userID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var histories []*model.WatchHistory
	for rows.Next() {
		h := &model.WatchHistory{}
		err := rows.Scan(&h.ID, &h.UserID, &h.DoubanID, &h.Title, &h.Poster, &h.Episode, &h.Progress, &h.Source, &h.WatchedAt)
		if err != nil {
			return nil, err
		}
		histories = append(histories, h)
	}

	return histories, nil
}
