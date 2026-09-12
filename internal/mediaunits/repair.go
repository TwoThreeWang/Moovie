package mediaunits

import (
	"context"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
)

// RepairFeatureReferences 只合并明确的电影版本单元，不猜测剧集和日期单元。
// 先把历史与短期结果迁到正片，再删除无人引用的版本单元；同一用户保留最新进度或删除状态。
func RepairFeatureReferences(ctx context.Context, db database.Executor, mediaID int) error {
	var kind string
	if err := db.QueryRow(ctx, `SELECT media_type FROM media WHERE id=$1 FOR UPDATE`, mediaID).Scan(&kind); err != nil {
		return err
	}
	if kind != "movie" && kind != "cartoon" {
		return nil
	}
	units, err := List(ctx, db, mediaID)
	if err != nil {
		return err
	}
	target := 0
	for _, u := range units {
		if u.UnitType == "feature" {
			target = u.ID
			break
		}
	}
	if target == 0 {
		return nil
	}
	ids := []int{target}
	for _, u := range units {
		if u.ID != target && (playurl.IsVersion(u.EpisodeKey) || u.EpisodeKey == "S01E01" && playurl.IsVersion(u.Title)) {
			ids = append(ids, u.ID)
		}
	}
	if len(ids) == 1 {
		return nil
	}
	if _, err := db.Exec(ctx, `WITH ranked AS (
 SELECT id,ROW_NUMBER() OVER(PARTITION BY user_id ORDER BY activity_at DESC,server_version DESC,id DESC) n
 FROM playback_positions WHERE media_unit_id=ANY($1::bigint[])
) DELETE FROM playback_positions p USING ranked r WHERE p.id=r.id AND r.n>1`, ids); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `UPDATE playback_positions SET media_unit_id=$2,media_id=$3,season_number=0,episode_key='feature',episode='正片'
 WHERE media_unit_id=ANY($1::bigint[])`, ids, target, mediaID); err != nil {
		return err
	}
	if _, err := db.Exec(ctx, `UPDATE playback_results SET media_unit_id=$2 WHERE media_unit_id=ANY($1::bigint[])`, ids, target); err != nil {
		return err
	}
	_, err = db.Exec(ctx, `DELETE FROM media_units WHERE id=ANY($1::bigint[]) AND id<>$2`, ids, target)
	return err
}
