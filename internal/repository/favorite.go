package repository

import (
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FavoriteRepository struct {
	db *gorm.DB
}

func NewFavoriteRepository(db *gorm.DB) *FavoriteRepository {
	return &FavoriteRepository{db: db}
}

// Add 添加收藏
func (r *FavoriteRepository) Add(userID, movieID int) error {
	favorite := &model.Favorite{
		UserID:    userID,
		MovieID:   movieID,
		CreatedAt: time.Now(),
	}
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(favorite).Error
}

// Remove 取消收藏
func (r *FavoriteRepository) Remove(userID, movieID int) error {
	return r.db.Where("user_id = ? AND movie_id = ?", userID, movieID).Delete(&model.Favorite{}).Error
}

// IsFavorited 检查是否已收藏
func (r *FavoriteRepository) IsFavorited(userID, movieID int) (bool, error) {
	var count int64
	err := r.db.Model(&model.Favorite{}).Where("user_id = ? AND movie_id = ?", userID, movieID).Count(&count).Error
	return count > 0, err
}

// ListByUser 获取用户收藏列表
func (r *FavoriteRepository) ListByUser(userID, limit, offset int) ([]*model.Favorite, error) {
	var favorites []*model.Favorite
	err := r.db.Preload("Movie").
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&favorites).Error
	return favorites, err
}

// CountByUser 统计用户收藏数量
func (r *FavoriteRepository) CountByUser(userID int) (int, error) {
	var count int64
	err := r.db.Model(&model.Favorite{}).Where("user_id = ?", userID).Count(&count).Error
	return int(count), err
}
