package repository

import (
	"errors"
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MovieRepository struct {
	DB *gorm.DB
}

func NewMovieRepository(db *gorm.DB) *MovieRepository {
	return &MovieRepository{DB: db}
}

// FindByDoubanID 根据豆瓣 ID 查找电影
func (r *MovieRepository) FindByDoubanID(doubanID string) (*model.Movie, error) {
	if r.DB == nil {
		return nil, errors.New("database connection is nil")
	}
	var movie model.Movie
	err := r.DB.Where("douban_id = ?", doubanID).Take(&movie).Error
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
	err := r.DB.Select("id", "douban_id", "updated_at").
		Order("updated_at DESC").
		Limit(limit).
		Find(&movies).Error
	return movies, err
}

// Upsert 创建或更新电影
func (r *MovieRepository) Upsert(movie *model.Movie) error {
	movie.UpdatedAt = time.Now()

	// 基础更新字段
	updateCols := []string{
		"title", "original_title", "year", "poster", "rating",
		"genres", "countries", "directors", "actors",
		"summary", "duration", "imdb_id", "updated_at",
		"reviews_json", "reviews_updated_at",
	}

	// 仅当 embedding 不为 nil 时才更新向量字段
	if movie.Embedding != nil {
		updateCols = append(updateCols, "embedding_content", "embedding")
	}

	return r.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "douban_id"}},
		DoUpdates: clause.AssignmentColumns(updateCols),
	}).Create(movie).Error
}

// FindSimilar 根据向量相似度查找相似电影
func (r *MovieRepository) FindSimilar(doubanID string, limit int) ([]model.Movie, error) {
	var movies []model.Movie
	// 使用 pgvector 的 <-> 操作符计算 L2 距离，距离越小越相似
	err := r.DB.Raw(`
		SELECT m2.* FROM movies m1
		JOIN movies m2 ON m1.id != m2.id
		WHERE m1.douban_id = ?
		  AND m1.embedding IS NOT NULL
		  AND m2.embedding IS NOT NULL
		ORDER BY m1.embedding <-> m2.embedding
		LIMIT ?
	`, doubanID, limit).Scan(&movies).Error
	return movies, err
}

// FindByID 根据 ID 查找电影
func (r *MovieRepository) FindByID(id int) (*model.Movie, error) {
	var movie model.Movie
	err := r.DB.Where("id = ?", id).Take(&movie).Error
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
	return r.DB.Where("douban_id = ?", doubanID).Delete(&model.Movie{}).Error
}

// GetUserRecommendations 根据用户兴趣向量获取个性化推荐
// 算法：加权平均 用户观看及收藏电影的向量，然后查找相似电影
func (r *MovieRepository) GetUserRecommendations(userID int, limit int) ([]model.Movie, error) {
	var movies []model.Movie

	// 使用一个复杂的 SQL 查询：
	// 1. 先汇聚用户观看和收藏的电影 ID
	// 2. 计算这些电影向量的平均值（用户兴趣向量）
	// 3. 用这个兴趣向量查找最相似的电影，排除已看/已收藏的
	err := r.DB.Raw(`
		WITH user_interests AS (
			-- 1. 已看 (UserMovie: watched) - 权重 1.0
			SELECT m.id, m.embedding, 1.0 as weight
			FROM movies m
			JOIN user_movies um ON um.movie_id = m.douban_id
			WHERE um.user_id = ? AND um.status = 'watched' AND m.embedding IS NOT NULL

			UNION ALL

			-- 2. 想看 (UserMovie: wish) - 权重 2.0
			SELECT m.id, m.embedding, 2.0 as weight
			FROM movies m
			JOIN user_movies um ON um.movie_id = m.douban_id
			WHERE um.user_id = ? AND um.status = 'wish' AND m.embedding IS NOT NULL

			UNION ALL

			-- 3. 正在观看 (WatchHistory) - 权重 0.8 (作为隐式兴趣，且排除已在 UserMovie 中的记录)
			SELECT m.id, m.embedding, 0.8 as weight
			FROM movies m
			JOIN watch_histories wh ON wh.douban_id = m.douban_id
			WHERE wh.user_id = ?
			  AND m.embedding IS NOT NULL
			  AND wh.progress > 5
			  AND NOT EXISTS (
				  SELECT 1 FROM user_movies um
				  WHERE um.user_id = ? AND um.movie_id = m.douban_id
			  )
		),
		user_vector AS (
			-- 计算加权平均向量
			SELECT AVG(embedding) as avg_embedding
			FROM user_interests
			WHERE embedding IS NOT NULL
		),
		excluded_ids AS (
			-- 排除用户已互动的电影 (已看/想看/正在看)
			SELECT m.id FROM movies m
			JOIN user_movies um ON um.movie_id = m.douban_id
			WHERE um.user_id = ?
			UNION
			SELECT m.id FROM movies m
			JOIN watch_histories wh ON wh.douban_id = m.douban_id
			WHERE wh.user_id = ?
		)
		SELECT m.* FROM movies m, user_vector uv
		WHERE m.embedding IS NOT NULL
		  AND m.id NOT IN (SELECT id FROM excluded_ids)
		  AND uv.avg_embedding IS NOT NULL
		ORDER BY m.embedding <-> uv.avg_embedding
		LIMIT ?
	`, userID, userID, userID, userID, userID, userID, limit).Scan(&movies).Error

	return movies, err
}

// GetPopularMovies 获取热门电影（用于新用户或无数据时的降级推荐）
func (r *MovieRepository) GetPopularMovies(limit int) ([]model.Movie, error) {
	var movies []model.Movie
	err := r.DB.Where("rating > 0 AND embedding IS NOT NULL").
		Order("rating DESC, updated_at DESC").
		Limit(limit).
		Find(&movies).Error
	return movies, err
}

// Count 获取电影总数
func (r *MovieRepository) Count() (int64, error) {
	var count int64
	err := r.DB.Model(&model.Movie{}).Count(&count).Error
	return count, err
}

// UpdateReviews 更新电影评论数据
func (r *MovieRepository) UpdateReviews(doubanID string, reviewsJSON string) error {
	return r.DB.Model(&model.Movie{}).Where("douban_id = ?", doubanID).Updates(map[string]interface{}{
		"reviews_json":       reviewsJSON,
		"reviews_updated_at": time.Now(),
	}).Error
}

// GetReliveClassics 获取“重温经典”：用户看过的且评分较高的电影
func (r *MovieRepository) GetReliveClassics(userID int, limit int) ([]model.Movie, error) {
	var movies []model.Movie
	err := r.DB.Raw(`
		SELECT m.* FROM movies m
		JOIN user_movies um ON um.movie_id = m.douban_id
		WHERE um.user_id = ?
		  AND um.status = 'watched'
		  AND m.rating >= 5
		  AND um.updated_at < NOW() - INTERVAL '30 day'
		ORDER BY RANDOM()
		LIMIT ?
	`, userID, limit).Scan(&movies).Error
	return movies, err
}

// GetRecentSimilarMovies 获取“关联推荐”：基于用户最近观看的一部电影推荐相似影片
func (r *MovieRepository) GetRecentSimilarMovies(userID int, limit int) ([]model.Movie, string, error) {
	var result struct {
		DoubanID string
		Title    string
	}

	// 查找用户最近观看的一部且有 embedding 的电影
	// 综合考虑：UserMovie (标记为已看) 和 WatchHistory (播放进度>5%)
	err := r.DB.Raw(`
		SELECT douban_id, title FROM (
			-- 1. UserMovie: 显式标记为已看
			SELECT m.douban_id, m.title, um.updated_at as action_time
			FROM movies m
			JOIN user_movies um ON um.movie_id = m.douban_id
			WHERE um.user_id = ? AND um.status = 'watched' AND m.embedding IS NOT NULL

			UNION ALL

			-- 2. WatchHistory: 隐式观看 (播放进度 > 5%)
			SELECT m.douban_id, m.title, wh.watched_at as action_time
			FROM movies m
			JOIN watch_histories wh ON wh.douban_id = m.douban_id
			WHERE wh.user_id = ? AND m.embedding IS NOT NULL AND wh.progress > 5
		) as t
		ORDER BY action_time DESC
		LIMIT 1
	`, userID, userID).Scan(&result).Error

	if err != nil {
		return nil, "", err
	}
	if result.DoubanID == "" {
		return nil, "", nil
	}

	// 2. 查找与其相似的项目
	similarMovies, err := r.FindSimilar(result.DoubanID, limit)
	return similarMovies, result.Title, err
}
