package social

import (
	"context"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// PostgresStore 是片场的 PostgreSQL 实现。
type PostgresStore struct{ database database.Executor }

// NewPostgresStore 创建存储实现。
func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

// activityColumns 是短评查询共用的字段（短评 + 作者信息）。
const activityColumns = `um.id, um.user_id, um.movie_id,
COALESCE(NULLIF(media.title, ''), um.title),
COALESCE(NULLIF(media.poster, ''), um.poster),
COALESCE(NULLIF(media.year, ''), um.year), um.status, um.rating, um.comment,
um.created_at, um.updated_at, u.id, u.email, u.username, u.password_hash, u.role, u.douban_user_id, u.is_public, u.avatar, u.created_at`

// ListCommentsByMovie 列出某部片子的短评。
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

// CountLikes 批量统计点赞数，一次查完避免每条短评一次查询。
func (store *PostgresStore) CountLikes(ctx context.Context, ids []int) (map[int]int, error) {
	return store.countByUserMovies(ctx, `SELECT user_movie_id, COUNT(*) FROM comment_likes WHERE user_movie_id = ANY($1) GROUP BY user_movie_id`, ids)
}

// CountReplies 批量统计回复数。
func (store *PostgresStore) CountReplies(ctx context.Context, ids []int) (map[int]int, error) {
	return store.countByUserMovies(ctx, `SELECT user_movie_id, COUNT(*) FROM comment_replies WHERE user_movie_id = ANY($1) GROUP BY user_movie_id`, ids)
}

// countByUserMovies 是两个批量统计的公共实现。
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

// LikedByUser 批量查询当前用户点过赞的短评。
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

// ToggleLike 点赞或取消点赞并返回最新数量。
func (store *PostgresStore) ToggleLike(ctx context.Context, userMovieID, userID int) (int, bool, error) {
	// 删除、插入和计数合并在一条 PostgreSQL 语句中，两个并发点击不会留下重复点赞。
	row := store.database.QueryRow(ctx, `WITH target AS (
  SELECT user_id AS recipient_user_id FROM user_movies WHERE id = $1
), deleted AS (
  DELETE FROM comment_likes WHERE user_movie_id = $1 AND user_id = $2 RETURNING 1
), inserted AS (
  INSERT INTO comment_likes (user_movie_id, user_id)
  SELECT $1, $2 FROM target WHERE NOT EXISTS (SELECT 1 FROM deleted)
  ON CONFLICT (user_movie_id, user_id) DO NOTHING RETURNING 1
), removed_notification AS (
  DELETE FROM social_notifications
  WHERE type = 'comment_like' AND user_movie_id = $1 AND actor_user_id = $2
    AND EXISTS (SELECT 1 FROM deleted)
  RETURNING id
), saved_notification AS (
  INSERT INTO social_notifications (recipient_user_id, actor_user_id, type, user_movie_id)
  SELECT recipient_user_id, $2, 'comment_like', $1 FROM target
  WHERE recipient_user_id <> $2 AND EXISTS (SELECT 1 FROM inserted)
  ON CONFLICT (type, actor_user_id, user_movie_id, (COALESCE(reply_id, 0)))
  DO UPDATE SET read_at = NULL, created_at = NOW()
  RETURNING id
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

// ListReplies 列出某条短评的全部回复。
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

// CreateReply 新建回复并带出作者信息。
func (store *PostgresStore) CreateReply(ctx context.Context, userMovieID, userID int, content string) (*Reply, error) {
	reply := &Reply{UserMovieID: userMovieID, UserID: userID, Content: content}
	if err := store.database.QueryRow(ctx, `WITH reply AS (
  INSERT INTO comment_replies (user_movie_id, user_id, content)
  VALUES ($1,$2,$3) RETURNING id, created_at
), saved_notification AS (
  INSERT INTO social_notifications (recipient_user_id, actor_user_id, type, user_movie_id, reply_id)
  SELECT um.user_id, $2, 'comment_reply', $1, reply.id
  FROM reply JOIN user_movies um ON um.id = $1
  WHERE um.user_id <> $2
  RETURNING id
)
SELECT id, created_at FROM reply`, userMovieID, userID, content).Scan(&reply.ID, &reply.CreatedAt); err != nil {
		return nil, fmt.Errorf("create comment reply: %w", err)
	}
	return reply, nil
}

// CountUnreadNotifications 返回消息页中的未读项数；同一短评的多个赞只算一项。
func (store *PostgresStore) CountUnreadNotifications(ctx context.Context, userID int) (int, error) {
	var count int
	err := store.database.QueryRow(ctx, `SELECT COUNT(*) FROM (
  SELECT user_movie_id FROM social_notifications
  WHERE recipient_user_id = $1 AND type = 'comment_like' AND read_at IS NULL
  GROUP BY user_movie_id
  UNION ALL
  SELECT id FROM social_notifications
  WHERE recipient_user_id = $1 AND type = 'comment_reply' AND read_at IS NULL
) unread`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

// ListNotifications 列出互动消息；点赞按短评聚合，回复逐条展示。
func (store *PostgresStore) ListNotifications(ctx context.Context, userID, limit int) ([]Notification, error) {
	rows, err := store.database.Query(ctx, `WITH like_counts AS (
  SELECT user_movie_id, COUNT(*)::int AS actor_count, BOOL_OR(read_at IS NULL) AS unread,
         MAX(created_at) AS created_at
  FROM social_notifications
  WHERE recipient_user_id = $1 AND type = 'comment_like'
  GROUP BY user_movie_id
), latest_likes AS (
  SELECT DISTINCT ON (user_movie_id) id, user_movie_id, actor_user_id
  FROM social_notifications
  WHERE recipient_user_id = $1 AND type = 'comment_like'
  ORDER BY user_movie_id, created_at DESC, id DESC
), items AS (
  SELECT latest.id, 'comment_like'::text AS type, latest.user_movie_id, um.movie_id,
         COALESCE(NULLIF(media.title, ''), um.title) AS movie_title,
         actor.username AS actor_name, actor.avatar AS actor_avatar,
         ''::text AS content, likes.actor_count, likes.unread, likes.created_at
  FROM like_counts likes
  JOIN latest_likes latest ON latest.user_movie_id = likes.user_movie_id
  JOIN user_movies um ON um.id = latest.user_movie_id
  LEFT JOIN media ON media.id = um.media_id
  JOIN users actor ON actor.id = latest.actor_user_id
  UNION ALL
  SELECT notification.id, notification.type, notification.user_movie_id, um.movie_id,
         COALESCE(NULLIF(media.title, ''), um.title), actor.username, actor.avatar,
         reply.content, 1, notification.read_at IS NULL, notification.created_at
  FROM social_notifications notification
  JOIN user_movies um ON um.id = notification.user_movie_id
  LEFT JOIN media ON media.id = um.media_id
  JOIN users actor ON actor.id = notification.actor_user_id
  JOIN comment_replies reply ON reply.id = notification.reply_id
  WHERE notification.recipient_user_id = $1 AND notification.type = 'comment_reply'
)
SELECT id, type, user_movie_id, movie_id, movie_title, actor_name, actor_avatar, content,
       actor_count, unread, created_at
FROM items ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	notifications := make([]Notification, 0)
	for rows.Next() {
		var notification Notification
		if err := rows.Scan(&notification.ID, &notification.Type, &notification.UserMovieID,
			&notification.MovieID, &notification.MovieTitle, &notification.ActorName,
			&notification.ActorAvatar, &notification.Content, &notification.ActorCount,
			&notification.Unread, &notification.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}
	return notifications, nil
}

// ReadNotification 标记一项已读并返回服务端计算的短评落点。
func (store *PostgresStore) ReadNotification(ctx context.Context, notificationID, userID int) (string, int, error) {
	var movieID string
	var userMovieID int
	err := store.database.QueryRow(ctx, `WITH selected AS (
  SELECT id, type, user_movie_id FROM social_notifications
  WHERE id = $1 AND recipient_user_id = $2
), marked AS (
  UPDATE social_notifications notification SET read_at = COALESCE(notification.read_at, NOW())
  FROM selected
  WHERE notification.recipient_user_id = $2
    AND (notification.id = selected.id OR
         (selected.type = 'comment_like' AND notification.type = 'comment_like'
          AND notification.user_movie_id = selected.user_movie_id))
)
SELECT um.movie_id, selected.user_movie_id
FROM selected JOIN user_movies um ON um.id = selected.user_movie_id`, notificationID, userID).Scan(&movieID, &userMovieID)
	if err != nil {
		return "", 0, fmt.Errorf("read notification: %w", err)
	}
	return movieID, userMovieID, nil
}

// ReadAllNotifications 标记当前用户的所有互动消息已读。
func (store *PostgresStore) ReadAllNotifications(ctx context.Context, userID int) error {
	if _, err := store.database.Exec(ctx, `UPDATE social_notifications SET read_at = NOW()
WHERE recipient_user_id = $1 AND read_at IS NULL`, userID); err != nil {
		return fmt.Errorf("read all notifications: %w", err)
	}
	return nil
}

// DeleteNotification 物理删除一条消息；点赞消息按页面展示的聚合组整体删除。
func (store *PostgresStore) DeleteNotification(ctx context.Context, notificationID, userID int) error {
	var deleted bool
	err := store.database.QueryRow(ctx, `WITH selected AS (
  SELECT id, type, user_movie_id FROM social_notifications
  WHERE id = $1 AND recipient_user_id = $2
), deleted AS (
  DELETE FROM social_notifications notification USING selected
  WHERE notification.recipient_user_id = $2
    AND (notification.id = selected.id OR
         (selected.type = 'comment_like' AND notification.type = 'comment_like'
          AND notification.user_movie_id = selected.user_movie_id))
  RETURNING notification.id
)
SELECT EXISTS (SELECT 1 FROM deleted)`, notificationID, userID).Scan(&deleted)
	if err != nil {
		return fmt.Errorf("delete notification: %w", err)
	}
	if !deleted {
		return fmt.Errorf("delete notification: not found")
	}
	return nil
}

// ListWeeklyFilms 统计本周被标记最多的影片。
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
WHERE um.status IN ('watched', 'watching') AND u.is_public = TRUE AND um.created_at >= $1
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

// ListFeaturedComments 挑选精选短评。
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

// ListFilmFriends 按活跃度和口味重合度推荐片友。
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

// scanActivities 把查询结果扫成短评列表。
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
