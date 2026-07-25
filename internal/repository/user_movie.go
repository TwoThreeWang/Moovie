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

// ListByUserAndDateRange 获取指定状态且创建时间落在 [start, end) 区间内的记录
// 用于月度报告等按月统计场景，避免像之前那样把用户全部记录（可能上万条）取回内存再逐条比对日期
func (r *UserMovieRepository) ListByUserAndDateRange(userID int, status string, start, end time.Time) ([]*model.UserMovie, error) {
	var records []*model.UserMovie
	err := r.db.Where("user_id = ? AND status = ? AND created_at >= ? AND created_at < ?", userID, status, start, end).
		Order("updated_at DESC").
		Find(&records).Error
	return records, err
}

// CountWatchedByAllUsersInRange 统计 [start, end) 区间内，所有有观影记录的用户各自的观影数
// 一次查询返回全站分布，供月度报告批量生成时计算"超越XX%用户"的百分位，
// 避免给每个用户都单独跑一次全站聚合查询
func (r *UserMovieRepository) CountWatchedByAllUsersInRange(start, end time.Time) (map[int]int, error) {
	var rows []struct {
		UserID int
		Cnt    int
	}
	err := r.db.Model(&model.UserMovie{}).
		Select("user_id, COUNT(*) as cnt").
		Where("status = ? AND created_at >= ? AND created_at < ?", "watched", start, end).
		Group("user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make(map[int]int, len(rows))
	for _, row := range rows {
		result[row.UserID] = row.Cnt
	}
	return result, nil
}

func (r *UserMovieRepository) CountByUser(userID int, status string) (int, error) {
	var count int64
	err := r.db.Model(&model.UserMovie{}).Where("user_id = ? AND status = ?", userID, status).Count(&count).Error
	return int(count), err
}

// CountByMovie 统计某部电影被多少用户标记为"看过"/"想看"，用于电影详情页展示的社交信号
// （"XX人看过 / XX人想看"），只是一个计数查询，不区分标记者是否公开主页
func (r *UserMovieRepository) CountByMovie(movieID string, status string) (int, error) {
	var count int64
	err := r.db.Model(&model.UserMovie{}).Where("movie_id = ? AND status = ?", movieID, status).Count(&count).Error
	return int(count), err
}

// AvgRatingByUser 统计某用户"已看"记录里已评分部分的平均分与评分数量
// 用 SQL 聚合直接算，不需要像分页前那样把全部已看记录拉到内存里再逐条累加
func (r *UserMovieRepository) AvgRatingByUser(userID int) (float64, int, error) {
	var result struct {
		Avg   float64
		Count int
	}
	err := r.db.Model(&model.UserMovie{}).
		Select("COALESCE(AVG(rating), 0) as avg, COUNT(*) as count").
		Where("user_id = ? AND status = ? AND rating > 0", userID, "watched").
		Scan(&result).Error
	return result.Avg, result.Count, err
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

// ==================== 广场（活动流 / 排行榜） ====================
// 只展示开启了"公开分享"的用户的数据，逻辑上和公开主页 PublicProfile 的可见性规则保持一致

// ListRecentPublicActivity 广场活动流：所有公开用户最近的"已看"记录，按更新时间倒序分页
func (r *UserMovieRepository) ListRecentPublicActivity(limit, offset int) ([]*model.UserMovie, error) {
	var records []*model.UserMovie
	err := r.db.Preload("User").
		Joins("JOIN users ON users.id = user_movies.user_id").
		Where("user_movies.status = ? AND users.is_public = ?", "watched", true).
		Order("user_movies.updated_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&records).Error
	return records, err
}

// CountRecentPublicActivity 广场活动流总数，配合分页判断是否还有更多
func (r *UserMovieRepository) CountRecentPublicActivity() (int, error) {
	var count int64
	err := r.db.Table("user_movies").
		Joins("JOIN users ON users.id = user_movies.user_id").
		Where("user_movies.status = ? AND users.is_public = ?", "watched", true).
		Count(&count).Error
	return int(count), err
}

// LeaderboardEntry 排行榜一行：某个公开用户在统计窗口内的观影数
type LeaderboardEntry struct {
	UserID   int
	Username string
	Avatar   string
	Count    int
}

// TopActiveUsersSince 统计公开用户里，从 since 到现在观影数最多的前 limit 名，用于广场"本周活跃"榜
func (r *UserMovieRepository) TopActiveUsersSince(since time.Time, limit int) ([]LeaderboardEntry, error) {
	var rows []LeaderboardEntry
	err := r.db.Table("user_movies").
		Select("user_movies.user_id as user_id, users.username as username, users.avatar as avatar, COUNT(*) as count").
		Joins("JOIN users ON users.id = user_movies.user_id").
		Where("user_movies.status = ? AND users.is_public = ? AND user_movies.created_at >= ?", "watched", true, since).
		Group("user_movies.user_id, users.username, users.avatar").
		Order("count DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

// TopActiveUsersAllTime 统计公开用户的历史观影总榜前 limit 名
func (r *UserMovieRepository) TopActiveUsersAllTime(limit int) ([]LeaderboardEntry, error) {
	var rows []LeaderboardEntry
	err := r.db.Table("user_movies").
		Select("user_movies.user_id as user_id, users.username as username, users.avatar as avatar, COUNT(*) as count").
		Joins("JOIN users ON users.id = user_movies.user_id").
		Where("user_movies.status = ? AND users.is_public = ?", "watched", true).
		Group("user_movies.user_id, users.username, users.avatar").
		Order("count DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

// CountOverlapWatched 统计两个用户"都看过"的电影数量，用于月度小记页给登录访客看的
// "你和TA共同看过 X 部电影"——只是个巧妙的连接点，不是严格意义上的推荐算法
func (r *UserMovieRepository) CountOverlapWatched(userA, userB int) (int, error) {
	if userA == userB {
		return 0, nil
	}
	var count int64
	err := r.db.Table("user_movies AS a").
		Joins("JOIN user_movies AS b ON a.movie_id = b.movie_id AND b.user_id = ? AND b.status = ?", userB, "watched").
		Where("a.user_id = ? AND a.status = ?", userA, "watched").
		Count(&count).Error
	return int(count), err
}
