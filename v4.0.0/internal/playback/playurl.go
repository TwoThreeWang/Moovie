package playback

import (
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
)

// PlaySource / PlayEpisode 是 playurl 包类型的别名，避免上层到处 import playurl。
type PlaySource = playurl.Source

// PlayEpisode 是 playurl.Episode 的别名。
type PlayEpisode = playurl.Episode

// selectEpisode 先匹配旧展示标签，再匹配规范季集身份。
// 因此 `?ep=S01E03` 可以选择 Provider 的“第3集”，且绝不会错误退回第一集。
func selectEpisode(episodes []PlayEpisode, requested string) (PlayEpisode, bool) {
	requested = strings.TrimSpace(requested)
	for _, episode := range episodes {
		if strings.TrimSpace(episode.Title) == requested {
			return episode, true
		}
	}
	if requested == "" {
		return PlayEpisode{}, false
	}
	season, key := mediaidentity.NormalizeEpisodeLabel(requested)
	for _, episode := range episodes {
		candidateSeason, candidateKey := mediaidentity.NormalizeEpisodeLabel(episode.Title)
		if season == candidateSeason && key == candidateKey {
			return episode, true
		}
	}
	return PlayEpisode{}, false
}

// formatTVBoxPlayURL 把播放地址转成 TVBox 要求的格式：
// 线路名用 $$$ 分隔，每条线路里再用 # 分隔各集，集名和地址用 $ 连接。
func formatTVBoxPlayURL(raw string) (string, string) {
	sources := parsePlayURL(raw)
	if len(sources) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(sources))
	urls := make([]string, 0, len(sources))
	for _, source := range sources {
		name := source.Name
		if name == "" {
			name = "默认源"
		}
		names = append(names, name)
		episodes := make([]string, 0, len(source.Episodes))
		for _, episode := range source.Episodes {
			episodes = append(episodes, episode.Title+"$"+episode.URL)
		}
		urls = append(urls, strings.Join(episodes, "#"))
	}
	return strings.Join(names, "$$$"), strings.Join(urls, "$$$")
}

// parsePlayURL 解析资源站的播放地址串。
func parsePlayURL(raw string) []PlaySource {
	return playurl.Parse(raw)
}
