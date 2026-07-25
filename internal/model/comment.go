package model

import "time"

// CommentLike 短评点赞记录：一个用户对一条"用户短评"（即 user_movies 里带评论的那条记录）的点赞
// 用 (UserMovieID, UserID) 联合唯一索引保证同一用户对同一条短评只能点一次赞
type CommentLike struct {
	ID          int       `json:"id" db:"id"`
	UserMovieID int       `json:"user_movie_id" db:"user_movie_id" gorm:"uniqueIndex:idx_comment_like_unique"`
	UserID      int       `json:"user_id" db:"user_id" gorm:"uniqueIndex:idx_comment_like_unique"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// CommentReply 短评回复：挂在某条"用户短评"下面的一条扁平回复（不支持多级嵌套）
type CommentReply struct {
	ID          int       `json:"id" db:"id"`
	UserMovieID int       `json:"user_movie_id" db:"user_movie_id" gorm:"index"`
	UserID      int       `json:"user_id" db:"user_id"`
	Content     string    `json:"content" db:"content"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	User        User      `json:"user,omitempty" gorm:"foreignKey:UserID;references:ID"`
}
