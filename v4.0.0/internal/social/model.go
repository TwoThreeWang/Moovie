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

type FilmFriend struct {
	UserID       int
	Username     string
	Avatar       string
	WatchedCount int
	CommentCount int
	SharedCount  int
	LastActiveAt time.Time
}

type Reply struct {
	ID          int
	UserMovieID int
	UserID      int
	Content     string
	CreatedAt   time.Time
	User        identity.User
}
