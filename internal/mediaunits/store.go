// Package mediaunits 保存作品的内容单元及可用性，不保存播放地址副本。
package mediaunits

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
)

type Unit struct {
	ID, MediaID                                 int
	UnitType                                    string
	SeasonNumber, EpisodeNumber, AbsoluteNumber int
	EpisodeKey, Title                           string
	AirDate                                     time.Time
	RuntimeMinutes                              int
	HasResource                                 bool
}

// List 同时返回未更新的单元，让选集与播出日历使用同一份内容身份。
func List(ctx context.Context, db database.Executor, mediaID int) ([]Unit, error) {
	var kind, title string
	var ready bool
	if err := db.QueryRow(ctx, `SELECT media_type,title,playback_reconciled_at IS NOT NULL FROM media WHERE id=$1`, mediaID).Scan(&kind, &title, &ready); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT id, media_id, unit_type, season_number, COALESCE(episode_number,0),
COALESCE(absolute_number,0), episode_key, title, air_date, COALESCE(runtime_minutes,0), has_resource
FROM media_units WHERE media_id = $1 AND unit_type IN ('feature','episode','special')
ORDER BY season_number, episode_number NULLS LAST, episode_key, id`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var units []Unit
	for rows.Next() {
		var unit Unit
		var airDate *time.Time
		if err := rows.Scan(&unit.ID, &unit.MediaID, &unit.UnitType, &unit.SeasonNumber, &unit.EpisodeNumber,
			&unit.AbsoluteNumber, &unit.EpisodeKey, &unit.Title, &airDate, &unit.RuntimeMinutes, &unit.HasResource); err != nil {
			return nil, err
		}
		if airDate != nil {
			unit.AirDate = *airDate
		}
		// 旧单元可能只有规范集键，缺少数字列；不能把中间缺集排到列表末尾。
		if unit.EpisodeNumber == 0 {
			if number := EpisodeNumber(unit.EpisodeKey); number != nil {
				unit.EpisodeNumber = *number
			}
		}
		units = append(units, unit)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if !ready {
		entries, err := resourceEntries(ctx, db, mediaID, kind, title)
		if err != nil {
			return nil, err
		}
		for i := range units {
			key := fmt.Sprintf("%s:%d:%s", units[i].UnitType, units[i].SeasonNumber, units[i].EpisodeKey)
			_, units[i].HasResource = entries[key]
			delete(entries, key)
		}
		// 存量作品还没生成单元时，也能用季集键播放；页面读取不补写索引。
		for _, entry := range entries {
			unit := Unit{MediaID: mediaID, UnitType: entry.UnitType, SeasonNumber: entry.Season, EpisodeKey: entry.EpisodeKey, Title: entry.Label, HasResource: true}
			if number := EpisodeNumber(entry.EpisodeKey); number != nil {
				unit.EpisodeNumber = *number
			}
			units = append(units, unit)
		}
	}
	sort.SliceStable(units, func(i, j int) bool {
		a, b := units[i], units[j]
		if a.SeasonNumber != b.SeasonNumber {
			return a.SeasonNumber < b.SeasonNumber
		}
		if a.EpisodeNumber != b.EpisodeNumber {
			if a.EpisodeNumber == 0 {
				return false
			}
			if b.EpisodeNumber == 0 {
				return true
			}
			return a.EpisodeNumber < b.EpisodeNumber
		}
		return a.EpisodeKey < b.EpisodeKey
	})
	return units, rows.Err()
}

// Reconcile 串行重算一部作品全部有效来源，不能因一个来源缺集就把其他来源一并置灰。
// 调用者在资源变更的事务内调用；作品行锁使较旧的重算不能晚到覆盖最新状态。
func Reconcile(ctx context.Context, db database.Executor, mediaID int) error {
	if mediaID <= 0 {
		return nil
	}
	var kind, title string
	if err := db.QueryRow(ctx, `SELECT media_type, title FROM media WHERE id=$1 FOR UPDATE`, mediaID).Scan(&kind, &title); err != nil {
		return err
	}
	entries, err := resourceEntries(ctx, db, mediaID, kind, title)
	if err != nil {
		return err
	}
	activeIDs := make([]int64, 0, len(entries))
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := entries[key]
		label := entry.Label
		if entry.UnitType == "feature" {
			label = "正片"
		}
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO media_units (media_id,unit_type,season_number,episode_key,title,episode_number,has_resource)
VALUES ($1,$2,$3,$4,$5,$6,TRUE)
ON CONFLICT (media_id,unit_type,season_number,episode_key) DO UPDATE SET
has_resource=TRUE, title=CASE WHEN media_units.title='' THEN EXCLUDED.title ELSE media_units.title END
RETURNING id`, mediaID, entry.UnitType, entry.Season, entry.EpisodeKey, label, EpisodeNumber(entry.EpisodeKey)).Scan(&id); err != nil {
			return err
		}
		activeIDs = append(activeIDs, id)
	}
	if _, err = db.Exec(ctx, `UPDATE media_units SET has_resource=FALSE
WHERE media_id=$1 AND has_resource AND NOT(id=ANY($2::bigint[]))`, mediaID, activeIDs); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `UPDATE media SET playback_reconciled_at=NOW() WHERE id=$1`, mediaID)
	return err
}

// resourceEntries 是回填与未回填读取共用的解析入口，不写数据库。
func resourceEntries(ctx context.Context, db database.Executor, mediaID int, kind, title string) (map[string]playurl.Entry, error) {
	rows, err := db.Query(ctx, `SELECT resource.vod_play_url, resource.vod_name
FROM resource_media_links link JOIN vod_items resource USING (source_key,vod_id)
JOIN sites ON sites.key = resource.source_key AND sites.enabled
WHERE link.media_id=$1 AND resource.resource_status NOT IN ('removed','retired','deleted')
AND resource.vod_play_url <> '' ORDER BY resource.source_key,resource.vod_id`, mediaID)
	if err != nil {
		return nil, err
	}
	entries := map[string]playurl.Entry{}
	for rows.Next() {
		var raw, resourceTitle string
		if err := rows.Scan(&raw, &resourceTitle); err != nil {
			rows.Close()
			return nil, err
		}
		if playurl.SeasonFromTitle(resourceTitle) == 0 {
			resourceTitle = title
		}
		for _, entry := range playurl.Entries(raw, kind, resourceTitle) {
			key := fmt.Sprintf("%s:%d:%s", entry.UnitType, entry.Season, entry.EpisodeKey)
			if _, exists := entries[key]; !exists {
				entries[key] = entry
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func EpisodeNumber(key string) *int {
	_, value, ok := strings.Cut(key, "E")
	if !ok {
		return nil
	}
	value, _, _ = strings.Cut(value, ":")
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return nil
	}
	return &n
}

// ReconcileResource 在采集保存后查真实关联，不信任调用方可能过期的 media_id。
func ReconcileResource(ctx context.Context, db database.Executor, source, vodID string) error {
	return ReconcileQuery(ctx, db, `SELECT media_id FROM resource_media_links WHERE source_key=$1 AND vod_id=$2`, source, vodID)
}

// ReconcileQuery 先释放结果集再写单元，事务内不会占着游标嵌套查询。
func ReconcileQuery(ctx context.Context, db database.Executor, query string, args ...any) error {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	sort.Ints(ids)
	for i, id := range ids {
		if i > 0 && id == ids[i-1] {
			continue
		}
		if err := Reconcile(ctx, db, id); err != nil {
			return err
		}
	}
	return nil
}
