package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserMovieRepository struct {
	db *gorm.DB
}

func NewUserMovieRepository(db *gorm.DB) *UserMovieRepository {
	return &UserMovieRepository{db: db}
}

func (r *UserMovieRepository) Upsert(m *model.UserMovie) error {
	// 记录是否提供了外部时间（来自豆瓣）
	hasExternalTime := !m.CreatedAt.IsZero() || !m.UpdatedAt.IsZero()
	
	if m.UpdatedAt.IsZero() {
		m.UpdatedAt = time.Now()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	// 需要更新的字段列表
	updateCols := []string{"title", "poster", "year", "status", "rating", "comment", "updated_at"}
	// 如果提供了有效的创建时间（来自豆瓣），也更新它
	if hasExternalTime {
		updateCols = append(updateCols, "created_at")
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "movie_id"}},
		DoUpdates: clause.AssignmentColumns(updateCols),
	}).Create(m).Error
}

func (r *UserMovieRepository) Remove(userID int, movieID string) error {
	return r.db.Where("user_id = ? AND movie_id = ?", userID, movieID).Delete(&model.UserMovie{}).Error
}

func (r *UserMovieRepository) ListByUser(userID int, status string, limit, offset int) ([]*model.UserMovie, error) {
	var records []*model.UserMovie
	err := r.db.Where("user_id = ? AND status = ?", userID, status).
		Order("updated_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&records).Error
	return records, err
}

func (r *UserMovieRepository) CountByUser(userID int, status string) (int, error) {
	var count int64
	err := r.db.Model(&model.UserMovie{}).Where("user_id = ? AND status = ?", userID, status).Count(&count).Error
	return int(count), err
}

func (r *UserMovieRepository) IsMarked(userID int, movieID string, status string) (bool, error) {
	var count int64
	err := r.db.Model(&model.UserMovie{}).
		Where("user_id = ? AND movie_id = ? AND status = ?", userID, movieID, status).
		Count(&count).Error
	return count > 0, err
}

func (r *UserMovieRepository) UpdateRatingComment(userID int, id int, rating int, comment string) error {
	return r.db.Model(&model.UserMovie{}).
		Where("user_id = ? AND id = ?", userID, id).
		Updates(map[string]interface{}{
			"rating":  rating,
			"comment": comment,
		}).Error
}

func (r *UserMovieRepository) GetByID(userID int, id int) (*model.UserMovie, error) {
	var rec model.UserMovie
	err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&rec).Error
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *UserMovieRepository) SetStatus(userID int, movieID string, status string) error {
	return r.db.Model(&model.UserMovie{}).
		Where("user_id = ? AND movie_id = ?", userID, movieID).
		UpdateColumn("status", status).Error
}

func (r *UserMovieRepository) GetByUserAndMovie(userID int, movieID string) (*model.UserMovie, error) {
	var rec model.UserMovie
	err := r.db.Where("user_id = ? AND movie_id = ?", userID, movieID).First(&rec).Error
	if err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *UserMovieRepository) ListCommentsByMovie(movieID string, limit int) ([]*model.UserMovie, error) {
	var records []*model.UserMovie
	err := r.db.Preload("User").
		Where("movie_id = ? AND status = ? AND comment IS NOT NULL AND comment <> ''", movieID, "watched").
		Order("updated_at DESC").
		Limit(limit).
		Find(&records).Error

	return records, err
}
