package repository

import (
	"errors"
	"time"

	"github.com/user/moovie/internal/model"
	"gorm.io/gorm"
)

// ==================== 短评点赞 ====================

type CommentLikeRepository struct {
	db *gorm.DB
}

func NewCommentLikeRepository(db *gorm.DB) *CommentLikeRepository {
	return &CommentLikeRepository{db: db}
}

// Toggle 点赞/取消点赞（同一用户对同一条短评重复点击即取消），返回操作后的最新点赞总数和当前用户的点赞状态
func (r *CommentLikeRepository) Toggle(userMovieID, userID int) (count int, liked bool, err error) {
	var existing model.CommentLike
	tx := r.db.Where("user_movie_id = ? AND user_id = ?", userMovieID, userID).First(&existing)
	switch {
	case tx.Error == nil:
		if err = r.db.Delete(&existing).Error; err != nil {
			return 0, false, err
		}
		liked = false
	case errors.Is(tx.Error, gorm.ErrRecordNotFound):
		like := &model.CommentLike{UserMovieID: userMovieID, UserID: userID, CreatedAt: time.Now()}
		if err = r.db.Create(like).Error; err != nil {
			return 0, false, err
		}
		liked = true
	default:
		return 0, false, tx.Error
	}
	count, err = r.CountByUserMovie(userMovieID)
	return count, liked, err
}

// CountByUserMovie 统计某条短评的点赞数
func (r *CommentLikeRepository) CountByUserMovie(userMovieID int) (int, error) {
	var count int64
	err := r.db.Model(&model.CommentLike{}).Where("user_movie_id = ?", userMovieID).Count(&count).Error
	return int(count), err
}

// CountByUserMovies 批量统计多条短评各自的点赞数，避免渲染评论列表时 N+1 查询
func (r *CommentLikeRepository) CountByUserMovies(userMovieIDs []int) (map[int]int, error) {
	result := make(map[int]int, len(userMovieIDs))
	if len(userMovieIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		UserMovieID int
		Cnt         int
	}
	err := r.db.Model(&model.CommentLike{}).
		Select("user_movie_id, COUNT(*) as cnt").
		Where("user_movie_id IN ?", userMovieIDs).
		Group("user_movie_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.UserMovieID] = row.Cnt
	}
	return result, nil
}

// LikedByUser 批量查询当前登录用户对这些短评的点赞状态
func (r *CommentLikeRepository) LikedByUser(userMovieIDs []int, userID int) (map[int]bool, error) {
	result := make(map[int]bool, len(userMovieIDs))
	if len(userMovieIDs) == 0 || userID == 0 {
		return result, nil
	}
	var likes []model.CommentLike
	err := r.db.Where("user_movie_id IN ? AND user_id = ?", userMovieIDs, userID).Find(&likes).Error
	if err != nil {
		return nil, err
	}
	for _, l := range likes {
		result[l.UserMovieID] = true
	}
	return result, nil
}

// ==================== 短评回复 ====================

type CommentReplyRepository struct {
	db *gorm.DB
}

func NewCommentReplyRepository(db *gorm.DB) *CommentReplyRepository {
	return &CommentReplyRepository{db: db}
}

// Create 发表一条回复，创建后立即带上 User 预加载，方便调用方直接渲染
func (r *CommentReplyRepository) Create(userMovieID, userID int, content string) (*model.CommentReply, error) {
	reply := &model.CommentReply{
		UserMovieID: userMovieID,
		UserID:      userID,
		Content:     content,
		CreatedAt:   time.Now(),
	}
	if err := r.db.Create(reply).Error; err != nil {
		return nil, err
	}
	if err := r.db.Preload("User").First(reply, reply.ID).Error; err != nil {
		return nil, err
	}
	return reply, nil
}

// ListByUserMovie 获取某条短评下的全部回复，按时间正序（老的在上面，符合对话阅读顺序）
func (r *CommentReplyRepository) ListByUserMovie(userMovieID int) ([]*model.CommentReply, error) {
	var replies []*model.CommentReply
	err := r.db.Preload("User").
		Where("user_movie_id = ?", userMovieID).
		Order("created_at ASC").
		Find(&replies).Error
	return replies, err
}

// CountByUserMovies 批量统计多条短评各自的回复数
func (r *CommentReplyRepository) CountByUserMovies(userMovieIDs []int) (map[int]int, error) {
	result := make(map[int]int, len(userMovieIDs))
	if len(userMovieIDs) == 0 {
		return result, nil
	}
	var rows []struct {
		UserMovieID int
		Cnt         int
	}
	err := r.db.Model(&model.CommentReply{}).
		Select("user_movie_id, COUNT(*) as cnt").
		Where("user_movie_id IN ?", userMovieIDs).
		Group("user_movie_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.UserMovieID] = row.Cnt
	}
	return result, nil
}
