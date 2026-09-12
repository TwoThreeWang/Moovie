package mediaidentity

import "github.com/TwoThreeWang/Moovie/new/internal/playurl"

const FeatureEpisodeKey = "feature"

// ParseResourceEpisodes 把共享解析结果附上资源身份，只在内存构造候选。
func ParseResourceEpisodes(sourceKey, vodID string, mediaID int, mediaType, raw string, titles ...string) []Episode {
	title := ""
	if len(titles) > 0 {
		title = titles[0]
	}
	return resourceEpisodes(sourceKey, vodID, mediaID, mediaType, title, raw)
}

func resourceEpisodes(sourceKey, vodID string, mediaID int, mediaType, title, raw string) []Episode {
	var episodes []Episode
	for _, e := range playurl.Entries(raw, mediaType, title) {
		episodes = append(episodes, Episode{CandidateKey: sourceKey + ":" + vodID + ":" + e.Key, PlaybackVersion: playurl.Version(raw), Part: e.Part,
			LineKey: e.LineKey, LineLabel: e.LineLabel, LineOrder: e.LineOrder, SourceKey: sourceKey, VodID: vodID, MediaID: mediaID,
			UnitType: e.UnitType, SeasonNumber: e.Season, EpisodeKey: e.EpisodeKey, EpisodeLabel: e.Label, PlayURL: e.URL,
			Format: "m3u8", Quality: e.Quality, SortOrder: e.Order})
	}
	return episodes
}

func isQualityVariantLabel(label string) bool { return playurl.IsVersion(label) }
