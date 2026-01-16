package repository

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

type MovieRepository struct {
	db *gorm.DB
}

func NewMovieRepository(db *gorm.DB) *MovieRepository {
	return &MovieRepository{db: db}
}

// FindByDoubanID 根据豆瓣 ID 查找电影
func (r *MovieRepository) FindByDoubanID(doubanID string) (*model.Movie, error) {
	var movie model.Movie
	var directorsJSON, actorsJSON []byte

	err := r.db.Model(&model.Movie{}).
		Select("id", "douban_id", "title", "original_title", "year", "poster", "rating",
			"genres", "countries", "directors", "actors", "summary", "duration", "imdb_id", "updated_at").
		Where("douban_id = ?", doubanID).
		Row().Scan(
		&movie.ID, &movie.DoubanID, &movie.Title, &movie.OriginalTitle,
		&movie.Year, &movie.Poster, &movie.Rating,
		pq.Array(&movie.Genres), pq.Array(&movie.Countries),
		&directorsJSON, &actorsJSON,
		&movie.Summary, &movie.Duration, &movie.IMDbID, &movie.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}

	// 解析 JSON
	json.Unmarshal(directorsJSON, &movie.Directors)
	json.Unmarshal(actorsJSON, &movie.Actors)

	return &movie, nil
}

// Upsert 创建或更新电影
func (r *MovieRepository) Upsert(movie *model.Movie) error {
	directorsJSON, _ := json.Marshal(movie.Directors)
	actorsJSON, _ := json.Marshal(movie.Actors)

	return r.db.Exec(`
		INSERT INTO movies (douban_id, title, original_title, year, poster, rating,
		                    genres, countries, directors, actors, summary, duration, imdb_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (douban_id) DO UPDATE SET
			title = EXCLUDED.title,
			original_title = EXCLUDED.original_title,
			year = EXCLUDED.year,
			poster = EXCLUDED.poster,
			rating = EXCLUDED.rating,
			genres = EXCLUDED.genres,
			countries = EXCLUDED.countries,
			directors = EXCLUDED.directors,
			actors = EXCLUDED.actors,
			summary = EXCLUDED.summary,
			duration = EXCLUDED.duration,
			imdb_id = EXCLUDED.imdb_id,
			updated_at = EXCLUDED.updated_at
	`, movie.DoubanID, movie.Title, movie.OriginalTitle, movie.Year, movie.Poster, movie.Rating,
		pq.Array(movie.Genres), pq.Array(movie.Countries),
		directorsJSON, actorsJSON,
		movie.Summary, movie.Duration, movie.IMDbID, time.Now()).Error
}

// FindByID 根据 ID 查找电影
func (r *MovieRepository) FindByID(id int) (*model.Movie, error) {
	var movie model.Movie
	var directorsJSON, actorsJSON []byte

	err := r.db.Model(&model.Movie{}).
		Select("id", "douban_id", "title", "original_title", "year", "poster", "rating",
			"genres", "countries", "directors", "actors", "summary", "duration", "imdb_id", "updated_at").
		Where("id = ?", id).
		Row().Scan(
		&movie.ID, &movie.DoubanID, &movie.Title, &movie.OriginalTitle,
		&movie.Year, &movie.Poster, &movie.Rating,
		pq.Array(&movie.Genres), pq.Array(&movie.Countries),
		&directorsJSON, &actorsJSON,
		&movie.Summary, &movie.Duration, &movie.IMDbID, &movie.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, err
	}

	// 解析 JSON
	json.Unmarshal(directorsJSON, &movie.Directors)
	json.Unmarshal(actorsJSON, &movie.Actors)

	return &movie, nil
}
