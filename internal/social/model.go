// Package social 是社区功能「片场」：本周热门放映、精选短评、片友推荐，
// 以及短评的点赞和回复。
//
// 涉及的表：user_movies（短评本体，本包不写）、comment_likes、comment_replies、users。
// 注意短评没有独立的表，它就是 user_movies 上的 comment 字段，
// 所以点赞和回复都挂在 user_movie_id 上。
package social

import (
	"errors"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

var ErrFollowUnavailable = errors.New("用户不存在或未公开主页")
var ErrNotificationUnavailable = errors.New("消息已失效或不存在")

// FollowedUser 仅包含关注管理需要的账号信息，不携带观影记录。
type FollowedUser struct {
	ID       int
	Username string
	Avatar   string
	IsPublic bool
}

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

// FollowState 是关注按钮需要的状态：是否已关注、以及目标用户的粉丝数。
type FollowState struct {
	Following bool
	Followers int
}

// NotificationTarget 是点开一条通知后应该去的地方。
// UserMovieID 为 0 表示这条通知没有短评主体（例如关注），仅在作者主页公开时跳转。
type NotificationTarget struct {
	UserMovieID      int
	CommentAvailable bool
	ActorUserID      int
	ActorIsPublic    bool
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
	ID            int
	Type          string
	UserMovieID   int
	MovieID       string
	MovieTitle    string
	ActorUserID   int
	ActorName     string
	ActorAvatar   string
	ActorIsPublic bool
	Content       string
	ActorCount    int
	Unread        bool
	CreatedAt     time.Time
}
