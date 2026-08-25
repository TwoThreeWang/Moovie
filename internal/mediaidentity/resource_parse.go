package mediaidentity

import (
	"fmt"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
)

// ParseResourceEpisodes 把资源站的播放地址串（多线路、多集，用分隔符拼在一起）
// 解析成结构化的剧集候选列表。电影且只有一集时标记为 feature。
func ParseResourceEpisodes(sourceKey, vodID string, mediaID int, mediaType, raw string) []Episode {
	sources := playurl.Parse(raw)
	result := make([]Episode, 0)
	for lineOrder, source := range sources {
		lineKey := "default"
		if lineOrder > 0 {
			lineKey = fmt.Sprintf("line-%02d", lineOrder+1)
		}
		for sortOrder, candidate := range source.Episodes {
			season, key := NormalizeEpisodeLabel(candidate.Title)
			unitType := "episode"
			if isMovieMediaType(mediaType) && len(source.Episodes) == 1 {
				unitType = "feature"
			}
			result = append(result, Episode{LineKey: lineKey, LineLabel: source.Name, LineOrder: lineOrder,
				SourceKey: sourceKey, VodID: vodID, MediaID: mediaID, UnitType: unitType,
				SeasonNumber: season, EpisodeKey: key, EpisodeLabel: candidate.Title, PlayURL: candidate.URL,
				Format: "m3u8", SortOrder: sortOrder})
		}
	}
	return result
}

// isMovieMediaType 判断分类名是否表示电影。
func isMovieMediaType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "movie" || value == "film" || strings.Contains(value, "电影")
}
