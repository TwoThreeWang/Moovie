// Package social 是社区功能「片场」：本周热门放映、精选短评、片友推荐，
// 以及短评的点赞和回复。
//
// 涉及的表：user_movies（短评本体，本包不写）、comment_likes、comment_replies、users。
// 注意短评没有独立的表，它就是 user_movies 上的 comment 字段，
// 所以点赞和回复都挂在 user_movie_id 上。
package social

import (
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

// Activity 以 user_movie 记录作为短评事实来源，并补充作者信息。
type Activity struct {
	library.Record
	User identity.User
}

// WeeklyFilm 是本周被标记最多的影片及其统计。
type WeeklyFilm struct {
	MovieID       string
	Title         string
	Poster        string
	Year          string
	ViewerCount   int
	CommentCount  int
	AverageRating float64
	LastSeenAt    time.Time
}

// FilmFriend 是推荐的片友及其活跃度和口味重合度。
type FilmFriend struct {
	UserID       int
	Username     string
	Avatar       string
	WatchedCount int
	CommentCount int
	SharedCount  int
	LastActiveAt time.Time
}

// Reply 是一条短评回复。
type Reply struct {
	ID          int
	UserMovieID int
	UserID      int
	Content     string
	CreatedAt   time.Time
	User        identity.User
}

// Notification 是消息页的一条互动；同一短评的点赞在查询时聚合。
type Notification struct {
	ID          int
	Type        string
	UserMovieID int
	MovieID     string
	MovieTitle  string
	ActorName   string
	ActorAvatar string
	Content     string
	ActorCount  int
	Unread      bool
	CreatedAt   time.Time
}
