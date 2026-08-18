package social

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type PostgresStore struct{ database database.Executor }

func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

const activityColumns = `um.id, um.user_id, um.movie_id,
COALESCE(NULLIF(media.title, ''), um.title),
COALESCE(NULLIF(media.poster, ''), um.poster),
COALESCE(NULLIF(media.year, ''), um.year), um.status, um.rating, um.comment,
um.created_at, um.updated_at, u.id, u.email, u.username, u.password_hash, u.role, u.douban_user_id, u.is_public, u.avatar, u.created_at`

func (store *PostgresStore) ListCommentsByMovie(ctx context.Context, movieID string, limit int) ([]Activity, error) {
	rows, err := store.database.Query(ctx, `SELECT `+activityColumns+` FROM user_movies um
LEFT JOIN media ON media.id = um.media_id
JOIN users u ON u.id = um.user_id
WHERE um.movie_id = $1 AND um.status = 'watched' AND um.comment IS NOT NULL AND um.comment <> ''
ORDER BY um.updated_at DESC LIMIT $2`, movieID, limit)
	if err != nil {
		return nil, fmt.Errorf("list movie comments: %w", err)
	}
	defer rows.Close()
	return scanActivities(rows)
}

func (store *PostgresStore) CountLikes(ctx context.Context, ids []int) (map[int]int, error) {
	return store.countByUserMovies(ctx, `SELECT user_movie_id, COUNT(*) FROM comment_likes WHERE user_movie_id = ANY($1) GROUP BY user_movie_id`, ids)
}

func (store *PostgresStore) CountReplies(ctx context.Context, ids []int) (map[int]int, error) {
	return store.countByUserMovies(ctx, `SELECT user_movie_id, COUNT(*) FROM comment_replies WHERE user_movie_id = ANY($1) GROUP BY user_movie_id`, ids)
}

func (store *PostgresStore) countByUserMovies(ctx context.Context, query string, ids []int) (map[int]int, error) {
	counts := make(map[int]int, len(ids))
	if len(ids) == 0 {
		return counts, nil
	}
	rows, err := store.database.Query(ctx, query, ids)
	if err != nil {
		return nil, fmt.Errorf("count comment interactions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, fmt.Errorf("scan comment interaction count: %w", err)
		}
		counts[id] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment interaction counts: %w", err)
	}
	return counts, nil
}

func (store *PostgresStore) LikedByUser(ctx context.Context, ids []int, userID int) (map[int]bool, error) {
	liked := make(map[int]bool, len(ids))
	if len(ids) == 0 || userID <= 0 {
		return liked, nil
	}
	rows, err := store.database.Query(ctx, `SELECT user_movie_id FROM comment_likes WHERE user_movie_id = ANY($1) AND user_id = $2`, ids, userID)
	if err != nil {
		return nil, fmt.Errorf("list current user comment likes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan current user comment like: %w", err)
		}
		liked[id] = true
	}
	return liked, rows.Err()
}

func (store *PostgresStore) ToggleLike(ctx context.Context, userMovieID, userID int) (int, bool, error) {
	// 删除、插入和计数合并在一条 PostgreSQL 语句中，两个并发点击不会留下重复点赞。
	row := store.database.QueryRow(ctx, `WITH deleted AS (
  DELETE FROM comment_likes WHERE user_movie_id = $1 AND user_id = $2 RETURNING 1
), inserted AS (
  INSERT INTO comment_likes (user_movie_id, user_id)
  SELECT $1, $2 WHERE NOT EXISTS (SELECT 1 FROM deleted)
  ON CONFLICT (user_movie_id, user_id) DO NOTHING RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM inserted)`, userMovieID, userID)
	var liked bool
	if err := row.Scan(&liked); err != nil {
		return 0, false, fmt.Errorf("toggle comment like: %w", err)
	}
	// 计数必须单独查：上面那条语句里的 COUNT 读的是语句开始时的快照，
	// 看不到同一条语句刚插入的点赞，会让刚点赞的用户看到少 1 的数字。
	var count int
	if err := store.database.QueryRow(ctx,
		`SELECT COUNT(*) FROM comment_likes WHERE user_movie_id = $1`, userMovieID).Scan(&count); err != nil {
		return 0, false, fmt.Errorf("count comment likes: %w", err)
	}
	return count, liked, nil
}

func (store *PostgresStore) ListReplies(ctx context.Context, userMovieID int) ([]Reply, error) {
	rows, err := store.database.Query(ctx, `SELECT r.id, r.user_movie_id, r.user_id, r.content, r.created_at,
u.id, u.email, u.username, u.password_hash, u.role, u.douban_user_id, u.is_public, u.avatar, u.created_at
FROM comment_replies r JOIN users u ON u.id = r.user_id
WHERE r.user_movie_id = $1 ORDER BY r.created_at ASC`, userMovieID)
	if err != nil {
		return nil, fmt.Errorf("list comment replies: %w", err)
	}
	defer rows.Close()
	replies := make([]Reply, 0)
	for rows.Next() {
		var reply Reply
		if err := rows.Scan(&reply.ID, &reply.UserMovieID, &reply.UserID, &reply.Content, &reply.CreatedAt,
			&reply.User.ID, &reply.User.Email, &reply.User.Username, &reply.User.PasswordHash, &reply.User.Role,
			&reply.User.DoubanUserID, &reply.User.IsPublic, &reply.User.Avatar, &reply.User.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan comment reply: %w", err)
		}
		replies = append(replies, reply)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment replies: %w", err)
	}
	return replies, nil
}

func (store *PostgresStore) CreateReply(ctx context.Context, userMovieID, userID int, content string) (*Reply, error) {
	reply := &Reply{UserMovieID: userMovieID, UserID: userID, Content: content}
	if err := store.database.QueryRow(ctx, `INSERT INTO comment_replies (user_movie_id, user_id, content)
VALUES ($1,$2,$3) RETURNING id, created_at`, userMovieID, userID, content).Scan(&reply.ID, &reply.CreatedAt); err != nil {
		return nil, fmt.Errorf("create comment reply: %w", err)
	}
	return reply, nil
}

func (store *PostgresStore) ListWeeklyFilms(ctx context.Context, since time.Time, limit int) ([]WeeklyFilm, error) {
	rows, err := store.database.Query(ctx, `SELECT um.movie_id,
COALESCE(NULLIF(media.title, ''), MAX(um.title)),
COALESCE(NULLIF(media.poster, ''), MAX(um.poster)),
COALESCE(NULLIF(media.year, ''), MAX(um.year)),
COUNT(DISTINCT um.user_id),
COUNT(*) FILTER (WHERE BTRIM(COALESCE(um.comment, '')) <> ''),
COALESCE(AVG(NULLIF(um.rating, 0))::double precision, 0),
MAX(um.created_at)
FROM user_movies um
LEFT JOIN media ON media.id = um.media_id
JOIN users u ON u.id = um.user_id
WHERE um.status = 'watched' AND u.is_public = TRUE AND um.created_at >= $1
GROUP BY um.movie_id, media.title, media.poster, media.year
ORDER BY MAX(um.created_at) DESC, COUNT(DISTINCT um.user_id) DESC
LIMIT $2`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("list weekly films: %w", err)
	}
	defer rows.Close()
	films := make([]WeeklyFilm, 0)
	for rows.Next() {
		var film WeeklyFilm
		if err := rows.Scan(&film.MovieID, &film.Title, &film.Poster, &film.Year, &film.ViewerCount,
			&film.CommentCount, &film.AverageRating, &film.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan weekly film: %w", err)
		}
		films = append(films, film)
	}
	return films, rows.Err()
}

func (store *PostgresStore) ListFeaturedComments(ctx context.Context, limit int) ([]Activity, error) {
	rows, err := store.database.Query(ctx, `WITH ranked AS (
  SELECT um.id, ROW_NUMBER() OVER (PARTITION BY um.user_id ORDER BY um.updated_at DESC, um.id DESC) AS user_rank
  FROM user_movies um JOIN users u ON u.id = um.user_id
  WHERE um.status = 'watched' AND u.is_public = TRUE AND BTRIM(COALESCE(um.comment, '')) <> ''
)
SELECT `+activityColumns+` FROM ranked
JOIN user_movies um ON um.id = ranked.id
LEFT JOIN media ON media.id = um.media_id
JOIN users u ON u.id = um.user_id
WHERE ranked.user_rank <= 2
ORDER BY um.updated_at DESC, um.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list featured comments: %w", err)
	}
	defer rows.Close()
	return scanActivities(rows)
}

func (store *PostgresStore) ListFilmFriends(ctx context.Context, currentUserID, limit int) ([]FilmFriend, error) {
	rows, err := store.database.Query(ctx, `SELECT u.id, u.username, u.avatar,
COUNT(um.id) AS watched_count,
COUNT(um.id) FILTER (WHERE BTRIM(COALESCE(um.comment, '')) <> '') AS comment_count,
COUNT(um.id) FILTER (WHERE $1 > 0 AND EXISTS (
  SELECT 1 FROM user_movies mine
  WHERE mine.user_id = $1 AND mine.status = 'watched' AND mine.movie_id = um.movie_id
)) AS shared_count,
MAX(um.created_at) AS last_active_at
FROM users u JOIN user_movies um ON um.user_id = u.id AND um.status = 'watched'
WHERE u.is_public = TRUE AND u.id <> $1
GROUP BY u.id, u.username, u.avatar
ORDER BY shared_count DESC, comment_count DESC, last_active_at DESC
LIMIT $2`, currentUserID, limit)
	if err != nil {
		return nil, fmt.Errorf("list film friends: %w", err)
	}
	defer rows.Close()
	friends := make([]FilmFriend, 0)
	for rows.Next() {
		var friend FilmFriend
		if err := rows.Scan(&friend.UserID, &friend.Username, &friend.Avatar, &friend.WatchedCount,
			&friend.CommentCount, &friend.SharedCount, &friend.LastActiveAt); err != nil {
			return nil, fmt.Errorf("scan film friend: %w", err)
		}
		friends = append(friends, friend)
	}
	return friends, rows.Err()
}

func scanActivities(rows database.Rows) ([]Activity, error) {
	activities := make([]Activity, 0)
	for rows.Next() {
		var activity Activity
		if err := rows.Scan(&activity.ID, &activity.UserID, &activity.MovieID, &activity.Title, &activity.Poster,
			&activity.Year, &activity.Status, &activity.Rating, &activity.Comment, &activity.CreatedAt, &activity.UpdatedAt,
			&activity.User.ID, &activity.User.Email, &activity.User.Username, &activity.User.PasswordHash, &activity.User.Role,
			&activity.User.DoubanUserID, &activity.User.IsPublic, &activity.User.Avatar, &activity.User.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan social activity: %w", err)
		}
		activities = append(activities, activity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate social activities: %w", err)
	}
	return activities, nil
}
