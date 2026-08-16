package social

import (
	"context"
	"time"
)

type Store interface {
	ListCommentsByMovie(ctx context.Context, movieID string, limit int) ([]Activity, error)
	CountLikes(ctx context.Context, userMovieIDs []int) (map[int]int, error)
	CountReplies(ctx context.Context, userMovieIDs []int) (map[int]int, error)
	LikedByUser(ctx context.Context, userMovieIDs []int, userID int) (map[int]bool, error)
	ToggleLike(ctx context.Context, userMovieID, userID int) (count int, liked bool, err error)
	ListReplies(ctx context.Context, userMovieID int) ([]Reply, error)
	CreateReply(ctx context.Context, userMovieID, userID int, content string) (*Reply, error)
	ListWeeklyFilms(ctx context.Context, since time.Time, limit int) ([]WeeklyFilm, error)
	ListFeaturedComments(ctx context.Context, limit int) ([]Activity, error)
	ListFilmFriends(ctx context.Context, currentUserID, limit int) ([]FilmFriend, error)
}
