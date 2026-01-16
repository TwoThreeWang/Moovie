package repository

import (
	"errors"
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MovieRepository struct {
	db *gorm.DB
}

func NewMovieRepository(db *gorm.DB) *MovieRepository {
	return &MovieRepository{db: db}
}

// FindByDoubanID 根据豆瓣 ID 查找电影
func (r *MovieRepository) FindByDoubanID(doubanID string) (*model.Movie, error) {
	if r.db == nil {
		return nil, errors.New("database connection is nil")
	}
	var movie model.Movie
	err := r.db.Where("douban_id = ?", doubanID).Take(&movie).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &movie, nil
}

// GetSitemapMovies 获取用于站点地图的电影列表
func (r *MovieRepository) GetSitemapMovies(limit int) ([]model.Movie, error) {
	var movies []model.Movie
	err := r.db.Select("id", "douban_id", "updated_at").
		Order("updated_at DESC").
		Limit(limit).
		Find(&movies).Error
	return movies, err
}

// Upsert 创建或更新电影
func (r *MovieRepository) Upsert(movie *model.Movie) error {
	movie.UpdatedAt = time.Now()
	// 使用 GORM 的 Clauses 处理冲突并更新
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "douban_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"title", "original_title", "year", "poster", "rating",
			"genres", "countries", "directors", "actors",
			"summary", "duration", "imdb_id", "updated_at",
		}),
	}).Create(movie).Error
}

// FindByID 根据 ID 查找电影
func (r *MovieRepository) FindByID(id int) (*model.Movie, error) {
	var movie model.Movie
	err := r.db.Where("id = ?", id).Take(&movie).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &movie, nil
}

// DeleteByDoubanID 根据豆瓣 ID 删除记录
func (r *MovieRepository) DeleteByDoubanID(doubanID string) error {
	return r.db.Where("douban_id = ?", doubanID).Delete(&model.Movie{}).Error
}
