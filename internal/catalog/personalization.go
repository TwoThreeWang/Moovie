package catalog

import (
	"context"
	"fmt"
)

// UserRecommendations 是「猜你喜欢」：把用户看过(1.0)、想看(2.0)、看了一半(0.8) 的影片向量
// 按权重平均成一个「口味向量」，再用 pgvector 找最接近的、且用户还没接触过的影片。
func (store *PostgresStore) UserRecommendations(ctx context.Context, userID, limit int) ([]Movie, error) {
	rows, err := store.database.Query(ctx, `WITH user_interests AS (
SELECT m.id, m.embedding, 1.0 AS weight FROM media m JOIN user_movies um ON um.media_id=m.id WHERE um.user_id=$1 AND um.status='watched' AND m.embedding IS NOT NULL
UNION ALL SELECT m.id, m.embedding, 2.0 AS weight FROM media m JOIN user_movies um ON um.media_id=m.id WHERE um.user_id=$1 AND um.status='wish' AND m.embedding IS NOT NULL
UNION ALL SELECT m.id, m.embedding, 0.8 AS weight FROM media m JOIN playback_positions position ON position.media_id=m.id
WHERE position.user_id=$1 AND position.deleted_at IS NULL AND m.embedding IS NOT NULL AND position.progress_percent>5
AND NOT EXISTS (SELECT 1 FROM user_movies um WHERE um.user_id=$1 AND um.media_id=m.id)
), user_vector AS (SELECT AVG(embedding) AS avg_embedding FROM user_interests WHERE embedding IS NOT NULL), excluded_ids AS (
SELECT media_id FROM user_movies WHERE user_id=$1 AND media_id IS NOT NULL
UNION SELECT media_id FROM playback_positions WHERE user_id=$1 AND media_id IS NOT NULL AND deleted_at IS NULL)
SELECT `+movieColumns+`
FROM media m, user_vector uv WHERE m.embedding IS NOT NULL AND m.id NOT IN (SELECT media_id FROM excluded_ids)
AND uv.avg_embedding IS NOT NULL ORDER BY m.embedding <-> uv.avg_embedding LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("user recommendations: %w", err)
	}
	return scanMovies(rows, "user recommendations")
}

// ReliveClassics 是「重温经典」：从用户 30 天前标记看过、评分不低的片里随机挑几部。
func (store *PostgresStore) ReliveClassics(ctx context.Context, userID, limit int) ([]Movie, error) {
	rows, err := store.database.Query(ctx, `SELECT `+movieColumns+`
FROM media m JOIN user_movies um ON um.media_id=m.id WHERE um.user_id=$1 AND um.status='watched'
AND m.rating_douban>=5 AND um.updated_at < NOW()-INTERVAL '30 day' ORDER BY RANDOM() LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("relive classics: %w", err)
	}
	return scanMovies(rows, "relive classics")
}

// RecentSimilar 是「因为你看了 X」：取最近一次观看/标记的影片，再找与它相似的。
// 第二个返回值就是那部影片的片名，用于文案。
func (store *PostgresStore) RecentSimilar(ctx context.Context, userID, limit int) ([]Movie, string, error) {
	rows, err := store.database.Query(ctx, `SELECT douban_id, title FROM (
SELECT m.douban_id,m.title,um.updated_at AS action_time FROM media m JOIN user_movies um ON um.media_id=m.id
WHERE um.user_id=$1 AND um.status='watched' AND m.embedding IS NOT NULL
UNION ALL SELECT m.douban_id,m.title,position.activity_at AS action_time FROM media m
JOIN playback_positions position ON position.media_id=m.id
WHERE position.user_id=$1 AND position.deleted_at IS NULL AND m.embedding IS NOT NULL AND position.progress_percent>5
) recent ORDER BY action_time DESC LIMIT 1`, userID)
	if err != nil {
		return nil, "", fmt.Errorf("recent movie: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return []Movie{}, "", rows.Err()
	}
	var doubanID, title string
	if err := rows.Scan(&doubanID, &title); err != nil {
		return nil, "", fmt.Errorf("scan recent movie: %w", err)
	}
	movies, err := store.FindSimilar(ctx, doubanID, limit)
	return movies, title, err
}

// scanMovies 把查询结果批量扫描成 Movie 切片，label 只用于错误信息。
func scanMovies(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}, label string) ([]Movie, error) {
	defer rows.Close()
	movies := make([]Movie, 0)
	for rows.Next() {
		movie, err := scanMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", label, err)
		}
		movies = append(movies, movie)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", label, err)
	}
	return movies, nil
}
