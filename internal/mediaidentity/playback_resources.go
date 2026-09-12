package mediaidentity

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaunits"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
)

// ListAllEpisodes 选集只读规范单元及采集时计算的可用状态，不需要扫描播放列表。
func (store *PostgresStore) ListAllEpisodes(ctx context.Context, mediaID int) ([]EpisodeInfo, error) {
	units, err := mediaunits.List(ctx, store.database, mediaID)
	if err != nil {
		return nil, err
	}
	var result []EpisodeInfo
	hasEpisodes := false
	for _, u := range units {
		if u.UnitType == "episode" && u.HasResource {
			hasEpisodes = true
		}
	}
	for _, u := range units {
		// 老版本曾给误分类的综艺创建空正片；保留历史引用，但不把废弃单元显示为选集。
		if u.UnitType == "feature" && !u.HasResource && hasEpisodes {
			continue
		}
		if u.UnitType != "feature" && (playurl.IsVersion(u.EpisodeKey) || playurl.Excluded(u.EpisodeKey)) {
			continue
		}
		label := u.EpisodeKey
		if u.UnitType == "feature" {
			label = "正片"
		}
		count := 0
		if u.HasResource {
			count = 1
		}
		result = append(result, EpisodeInfo{UnitID: u.ID, UnitType: u.UnitType, HasResource: u.HasResource,
			SeasonNumber: u.SeasonNumber, EpisodeKey: u.EpisodeKey, EpisodeLabel: label, SourceCount: count})
	}
	return result, nil
}

// ListResourceCandidates 仅从当前有效资源实时解析指定集，资源与地址不再保存第二份索引。
func (store *PostgresStore) ListResourceCandidates(ctx context.Context, mediaID, season int, key string) ([]ResourceCandidate, error) {
	if mediaID <= 0 {
		return nil, nil
	}
	media, err := store.FindByID(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	units, err := mediaunits.List(ctx, store.database, mediaID)
	if err != nil {
		return nil, err
	}
	resources, err := search.NewPostgresStore(store.database).ListUnifiedResources(ctx, []int{mediaID})
	if err != nil {
		return nil, err
	}
	unitIDs := map[string]int{}
	for _, u := range units {
		unitIDs[fmt.Sprintf("%d:%s", u.SeasonNumber, u.EpisodeKey)] = u.ID
	}
	var result []ResourceCandidate
	for _, r := range resources {
		title := r.VodName
		if playurl.SeasonFromTitle(title) == 0 {
			title = media.Title
		}
		for _, e := range resourceEpisodes(r.SourceKey, r.VodId, mediaID, media.MediaType, title, r.VodPlayUrl) {
			if e.SeasonNumber != season || e.EpisodeKey != key {
				continue
			}
			e.MediaUnitID = unitIDs[fmt.Sprintf("%d:%s", e.SeasonNumber, e.EpisodeKey)]
			result = append(result, ResourceCandidate{Episode: e, SuccessCount: r.SampleCount - r.FailedCount,
				FailureCount: r.FailedCount, AvgLoadMs: r.AvgSpeedMs, MappingConfidence: r.MediaConfidence})
		}
	}
	return result, nil
}

func (store *PostgresStore) ListUnitResourceCandidates(ctx context.Context, unitID int) ([]ResourceCandidate, error) {
	var mediaID, season int
	var key string
	if err := store.database.QueryRow(ctx, `SELECT media_id,season_number,episode_key FROM media_units WHERE id=$1`, unitID).Scan(&mediaID, &season, &key); err != nil {
		return nil, err
	}
	return store.ListResourceCandidates(ctx, mediaID, season, key)
}
