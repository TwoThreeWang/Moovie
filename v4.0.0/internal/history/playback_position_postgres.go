package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// upsertPlaybackPosition 写入播放进度。
// 进度按三种身份之一去重（优先级从高到低）：季集 ID → 媒体+季集 → 资源站+资源 ID+季集，
// 对应三个不同的唯一索引，所以下面有两段几乎一样的 SQL。
// 末尾的 WHERE EXCLUDED.activity_at >= ... 保证晚到的旧数据不会覆盖新进度。
func upsertPlaybackPosition(ctx context.Context, executor database.Executor, userID int, operation SyncOperation) error {
	if operation.Season < 1 {
		operation.Season = 1
	}
	if operation.EpisodeKey == "" {
		operation.EpisodeKey = "S01E01"
	}
	if operation.EntryPage != "watch" {
		operation.EntryPage = "play"
	}
	if operation.MediaID <= 0 && operation.DoubanID != "" {
		var mediaID int
		if err := executor.QueryRow(ctx, `SELECT id FROM media WHERE douban_id = $1 LIMIT 1`, operation.DoubanID).Scan(&mediaID); err == nil {
			operation.MediaID = mediaID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("resolve playback media: %w", err)
		}
	}
	mediaUnitID, err := resolvePlaybackMediaUnit(ctx, executor, operation)
	if err != nil {
		return err
	}
	operation.MediaUnitID = mediaUnitID
	activityAt := operation.OccurredAt
	if activityAt.IsZero() {
		activityAt = time.Now().UTC()
	}
	completed := operation.Type == "complete" || operation.Progress >= 100
	var deletedAt any
	if operation.Type == "delete" {
		deletedAt = activityAt
	}
	arguments := []any{userID, nullablePositive(operation.MediaID), operation.Position, operation.Duration,
		operation.Progress, completed, operation.Source, operation.VodID, operation.Title, operation.Poster,
		operation.Episode, operation.Season, operation.EpisodeKey, activityAt, deletedAt, operation.EntryPage}
	if mediaUnitID > 0 {
		_, err = executor.Exec(ctx, `INSERT INTO playback_positions
(user_id, media_id, media_unit_id, position_seconds, duration_seconds, progress_percent, completed,
 last_source_key, last_vod_id, title, poster, episode, season_number, episode_key, activity_at, deleted_at, entry_page, updated_at)
VALUES ($1,$2,$17,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$18)
ON CONFLICT (user_id, media_unit_id) WHERE media_unit_id IS NOT NULL DO UPDATE SET
media_id = COALESCE(EXCLUDED.media_id, playback_positions.media_id),
position_seconds = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.position_seconds ELSE playback_positions.position_seconds END,
duration_seconds = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.duration_seconds ELSE playback_positions.duration_seconds END,
progress_percent = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.progress_percent ELSE playback_positions.progress_percent END,
completed = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.completed ELSE playback_positions.completed END,
last_source_key = CASE WHEN EXCLUDED.last_source_key <> '' THEN EXCLUDED.last_source_key ELSE playback_positions.last_source_key END,
last_vod_id = CASE WHEN EXCLUDED.last_vod_id <> '' THEN EXCLUDED.last_vod_id ELSE playback_positions.last_vod_id END,
title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE playback_positions.title END,
poster = CASE WHEN EXCLUDED.poster <> '' THEN EXCLUDED.poster ELSE playback_positions.poster END,
episode = CASE WHEN EXCLUDED.episode <> '' THEN EXCLUDED.episode ELSE playback_positions.episode END,
season_number = EXCLUDED.season_number, episode_key = EXCLUDED.episode_key,
entry_page = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.entry_page ELSE playback_positions.entry_page END,
activity_at = EXCLUDED.activity_at, deleted_at = EXCLUDED.deleted_at,
server_version = nextval('playback_position_version_seq'), updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.activity_at >= playback_positions.activity_at`, append(arguments, mediaUnitID, activityAt)...)
	} else {
		conflictTarget := playbackPositionConflictTarget(operation.MediaID)
		_, err = executor.Exec(ctx, `INSERT INTO playback_positions
(user_id, media_id, position_seconds, duration_seconds, progress_percent, completed,
 last_source_key, last_vod_id, title, poster, episode, season_number, episode_key, activity_at, deleted_at, entry_page, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT `+conflictTarget+` DO UPDATE SET
media_id = COALESCE(EXCLUDED.media_id, playback_positions.media_id),
position_seconds = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.position_seconds ELSE playback_positions.position_seconds END,
duration_seconds = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.duration_seconds ELSE playback_positions.duration_seconds END,
progress_percent = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.progress_percent ELSE playback_positions.progress_percent END,
completed = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.completed ELSE playback_positions.completed END,
last_source_key = CASE WHEN EXCLUDED.last_source_key <> '' THEN EXCLUDED.last_source_key ELSE playback_positions.last_source_key END,
last_vod_id = CASE WHEN EXCLUDED.last_vod_id <> '' THEN EXCLUDED.last_vod_id ELSE playback_positions.last_vod_id END,
title = CASE WHEN EXCLUDED.title <> '' THEN EXCLUDED.title ELSE playback_positions.title END,
poster = CASE WHEN EXCLUDED.poster <> '' THEN EXCLUDED.poster ELSE playback_positions.poster END,
episode = CASE WHEN EXCLUDED.episode <> '' THEN EXCLUDED.episode ELSE playback_positions.episode END,
season_number = EXCLUDED.season_number, episode_key = EXCLUDED.episode_key,
entry_page = CASE WHEN EXCLUDED.deleted_at IS NULL THEN EXCLUDED.entry_page ELSE playback_positions.entry_page END,
activity_at = EXCLUDED.activity_at, deleted_at = EXCLUDED.deleted_at,
server_version = nextval('playback_position_version_seq'), updated_at = EXCLUDED.updated_at
WHERE EXCLUDED.activity_at >= playback_positions.activity_at`, append(arguments, activityAt)...)
	}
	if err != nil {
		return fmt.Errorf("upsert playback position: %w", err)
	}
	return nil
}

// playbackPositionConflictTarget 根据有没有 media_id 选择对应的部分唯一索引。
func playbackPositionConflictTarget(mediaID int) string {
	if mediaID > 0 {
		return `(user_id, media_id, season_number, episode_key)
WHERE media_unit_id IS NULL AND media_id IS NOT NULL`
	}
	return `(user_id, last_source_key, last_vod_id, season_number, episode_key)
WHERE media_unit_id IS NULL AND media_id IS NULL`
}

// resolvePlaybackMediaUnit 尽量把进度挂到规范季集上：
// 先按 media_id + 集号找，再退回按资源的候选反查（该资源只对应唯一一集时也认）。
// 实在找不到就返回 0，进度按资源身份保存。
func resolvePlaybackMediaUnit(ctx context.Context, executor database.Executor, operation SyncOperation) (int, error) {
	if operation.MediaUnitID > 0 {
		return operation.MediaUnitID, nil
	}
	if operation.MediaID > 0 {
		var mediaUnitID int
		err := executor.QueryRow(ctx, `SELECT id FROM media_units
WHERE media_id = $1 AND (episode_key = $2 OR ($3 = 1 AND unit_type = 'feature'))
ORDER BY CASE WHEN episode_key = $2 THEN 0 ELSE 1 END, id LIMIT 1`, operation.MediaID, operation.EpisodeKey, operation.Season).Scan(&mediaUnitID)
		if err == nil {
			return mediaUnitID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, fmt.Errorf("resolve playback media unit: %w", err)
		}
	}
	if operation.Source == "" || operation.VodID == "" {
		return 0, nil
	}
	var mediaUnitID *int
	err := executor.QueryRow(ctx, `WITH candidates AS (
    SELECT candidate.media_unit_id, candidate.season_number, candidate.episode_key, candidate.last_seen_at
    FROM resource_episode_candidates candidate
    JOIN resource_play_lines line ON line.id = candidate.line_id
    WHERE line.source_key = $1 AND line.vod_id = $2 AND candidate.media_unit_id IS NOT NULL
), exact_match AS (
    SELECT media_unit_id FROM candidates WHERE season_number = $3 AND episode_key = $4
    ORDER BY last_seen_at DESC NULLS LAST LIMIT 1
), only_candidate AS (
    SELECT MIN(media_unit_id) AS media_unit_id FROM candidates HAVING COUNT(DISTINCT media_unit_id) = 1
)
SELECT COALESCE((SELECT media_unit_id FROM exact_match), (SELECT media_unit_id FROM only_candidate))`,
		operation.Source, operation.VodID, operation.Season, operation.EpisodeKey).Scan(&mediaUnitID)
	if errors.Is(err, pgx.ErrNoRows) || mediaUnitID == nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("resolve resource playback unit: %w", err)
	}
	return *mediaUnitID, nil
}

// playbackPositionSelect 是进度查询的公共部分，标题和海报优先取 media 表的（更准更新）。
const playbackPositionSelect = `SELECT position.id, position.user_id, position.media_id, position.media_unit_id,
COALESCE(media.douban_id, ''), position.last_vod_id,
COALESCE(NULLIF(media.title, ''), position.title),
COALESCE(NULLIF(media.poster, ''), position.poster), position.episode,
position.season_number, position.episode_key, position.progress_percent, position.position_seconds,
position.duration_seconds, position.last_source_key, position.entry_page, position.activity_at, position.updated_at
FROM playback_positions position LEFT JOIN media ON media.id = position.media_id`

// queryPlaybackPositions 按给定条件查询进度记录。
func queryPlaybackPositions(ctx context.Context, executor database.Executor, predicate string, arguments ...any) ([]Record, error) {
	rows, err := executor.Query(ctx, playbackPositionSelect+` WHERE `+predicate, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list playback positions: %w", err)
	}
	defer rows.Close()
	records := make([]Record, 0)
	for rows.Next() {
		var record Record
		var mediaID, mediaUnitID *int
		if err := rows.Scan(&record.ID, &record.UserID, &mediaID, &mediaUnitID, &record.DoubanID,
			&record.VodID, &record.Title, &record.Poster, &record.Episode, &record.SeasonNumber,
			&record.EpisodeKey, &record.Progress, &record.LastTime, &record.Duration, &record.Source,
			&record.EntryPage, &record.WatchedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan playback position: %w", err)
		}
		if mediaID != nil {
			record.MediaID = *mediaID
		}
		if mediaUnitID != nil {
			record.MediaUnitID = *mediaUnitID
		}
		record.PreferredSource, record.PreferredVodID = record.Source, record.VodID
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate playback positions: %w", err)
	}
	return records, nil
}

// ListContinue 返回"继续观看"列表。
//
// 标记为"已看"的影片会被排除，但对应的 playback_positions 行不会被删除：
// playback_positions 是唯一的服务端播放进度表，物理删除会让误点变成不可恢复的数据丢失，
// 也会让月报和统计失去这段观看事实。取消"已看"标记后，进度会自动重新出现在列表中。
//
// 排除条件要求"已看"标记不早于最后一次播放（updated_at >= activity_at）。
// 这一条不是多余的：user_movies 里的 watched 有两个来源，含义完全不同。
//
//   - 用户手动点"已看"：library.Handler 不传时间，updated_at 落为当前时刻，
//     必然晚于最后一次播放，因此会被排除——这正是用户想要的"我看完了"。
//   - 豆瓣同步：douban.Service 把豆瓣的标记时间原样写进 updated_at，
//     通常是几个月甚至几年前。豆瓣只能整部标记，没有分集概念，
//     "看过第一季"和"正在追第二季"在库里长得一模一样；若不比较时间，
//     一次全量同步就会把用户正在追的剧集从继续观看里抹掉。
//
// 副作用同样是想要的：标记已看之后又重新播放，activity_at 会超过 updated_at，
// 影片重新回到继续观看——重看本来就该出现在这里。
//
// user_movies 以 media_id 关联；只有资源站身份、尚未关联规范媒体的进度记录
// （position.media_id IS NULL）无法被标记为已看，因此始终保留在列表里。
func (store *PostgresStore) ListContinue(ctx context.Context, userID, limit, offset int) ([]Record, error) {
	return queryPlaybackPositions(ctx, store.database,
		`position.user_id = $1 AND position.deleted_at IS NULL AND position.completed = FALSE
AND NOT EXISTS (
    SELECT 1 FROM user_movies
    WHERE user_movies.user_id = position.user_id
      AND user_movies.media_id IS NOT NULL
      AND user_movies.media_id = position.media_id
      AND user_movies.status = 'watched'
      AND user_movies.updated_at >= position.activity_at
)
ORDER BY position.activity_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
}
