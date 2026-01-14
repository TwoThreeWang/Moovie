package repository

import (
	"database/sql"

	"github.com/user/moovie/internal/model"
)

type FeedbackRepository struct {
	db *sql.DB
}

func NewFeedbackRepository(db *sql.DB) *FeedbackRepository {
	return &FeedbackRepository{db: db}
}

// Create 创建反馈
func (r *FeedbackRepository) Create(f *model.Feedback) error {
	return r.db.QueryRow(`
		INSERT INTO feedbacks (user_id, type, content, movie_url, status, created_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		RETURNING id
	`, f.UserID, f.Type, f.Content, f.MovieURL, "pending").Scan(&f.ID)
}

// List 获取反馈列表（管理后台用）
func (r *FeedbackRepository) List(status string, limit, offset int) ([]*model.Feedback, error) {
	query := `
		SELECT id, user_id, type, content, movie_url, status, created_at
		FROM feedbacks
	`
	args := []interface{}{}

	if status != "" {
		query += " WHERE status = $1"
		args = append(args, status)
	}

	query += " ORDER BY created_at DESC LIMIT $" + string(rune(len(args)+1+'0')) + " OFFSET $" + string(rune(len(args)+2+'0'))
	args = append(args, limit, offset)

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feedbacks []*model.Feedback
	for rows.Next() {
		f := &model.Feedback{}
		err := rows.Scan(&f.ID, &f.UserID, &f.Type, &f.Content, &f.MovieURL, &f.Status, &f.CreatedAt)
		if err != nil {
			return nil, err
		}
		feedbacks = append(feedbacks, f)
	}

	return feedbacks, nil
}

// UpdateStatus 更新反馈状态
func (r *FeedbackRepository) UpdateStatus(id int, status string) error {
	_, err := r.db.Exec(`UPDATE feedbacks SET status = $1 WHERE id = $2`, status, id)
	return err
}
