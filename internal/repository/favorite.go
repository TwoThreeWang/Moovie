package repository

import (
	"database/sql"

	"github.com/user/moovie/internal/model"
)

type FavoriteRepository struct {
	db *sql.DB
}

func NewFavoriteRepository(db *sql.DB) *FavoriteRepository {
	return &FavoriteRepository{db: db}
}

// Add 添加收藏
func (r *FavoriteRepository) Add(userID, movieID int) error {
	_, err := r.db.Exec(`
		INSERT INTO favorites (user_id, movie_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_id, movie_id) DO NOTHING
	`, userID, movieID)
	return err
}

// Remove 取消收藏
func (r *FavoriteRepository) Remove(userID, movieID int) error {
	_, err := r.db.Exec(`
		DELETE FROM favorites WHERE user_id = $1 AND movie_id = $2
	`, userID, movieID)
	return err
}

// IsFavorited 检查是否已收藏
func (r *FavoriteRepository) IsFavorited(userID, movieID int) (bool, error) {
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM favorites WHERE user_id = $1 AND movie_id = $2
	`, userID, movieID).Scan(&count)
	return count > 0, err
}

// ListByUser 获取用户收藏列表
func (r *FavoriteRepository) ListByUser(userID, limit, offset int) ([]*model.Favorite, error) {
	rows, err := r.db.Query(`
		SELECT f.id, f.user_id, f.movie_id, f.created_at,
		       m.douban_id, m.title, m.poster, m.rating, m.year
		FROM favorites f
		JOIN movies m ON f.movie_id = m.id
		WHERE f.user_id = $1
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var favorites []*model.Favorite
	for rows.Next() {
		f := &model.Favorite{Movie: &model.Movie{}}
		err := rows.Scan(
			&f.ID, &f.UserID, &f.MovieID, &f.CreatedAt,
			&f.Movie.DoubanID, &f.Movie.Title, &f.Movie.Poster, &f.Movie.Rating, &f.Movie.Year,
		)
		if err != nil {
			return nil, err
		}
		favorites = append(favorites, f)
	}

	return favorites, nil
}

// CountByUser 统计用户收藏数量
func (r *FavoriteRepository) CountByUser(userID int) (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM favorites WHERE user_id = $1`, userID).Scan(&count)
	return count, err
}
