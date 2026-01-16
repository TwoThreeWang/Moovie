package utils

import (
	"strings"
)

// PlaySource 播放源
type PlaySource struct {
	Name     string
	Episodes []PlayEpisode
}

// PlayEpisode 剧集/播放链接
type PlayEpisode struct {
	Title string
	URL   string
}

// ParsePlayUrl 解析资源网播放链接
// 格式通常为: Source1$Ep1$URL1#Ep2$URL2$$$Source2$Ep1$URL1...
func ParsePlayUrl(playUrl string) []PlaySource {
	if playUrl == "" {
		return nil
	}

	var sources []PlaySource
	// 1. 分割不同播放源 (通常用 $$$ 分割)
	sourceSegments := strings.Split(playUrl, "$$$")

	// 如果没有 $$$，尝试检查是否有 # 和 $ 的结构
	if len(sourceSegments) == 1 && !strings.Contains(playUrl, "$$$") {
		// 有些站点可能只有一个源且不带 $$$
	}

	for _, seg := range sourceSegments {
		if seg == "" {
			continue
		}

		source := PlaySource{}
		// 2. 分割剧集 (通常用 # 分割)
		epSegments := strings.Split(seg, "#")

		for _, epSeg := range epSegments {
			if epSeg == "" {
				continue
			}

			// 3. 分割标题和链接 (通常用 $ 分割)
			parts := strings.Split(epSeg, "$")
			if len(parts) >= 2 {
				// 格式: 标题$链接
				source.Episodes = append(source.Episodes, PlayEpisode{
					Title: parts[0],
					URL:   parts[1],
				})
			} else if len(parts) == 1 {
				// 格式: 链接 (没有标题)
				source.Episodes = append(source.Episodes, PlayEpisode{
					Title: "正片",
					URL:   parts[0],
				})
			}
		}

		if len(source.Episodes) > 0 {
			// 尝试给源起个名字，如果有多个源
			if len(sources) == 0 {
				source.Name = "默认源"
			} else {
				source.Name = "备用源 " + string(rune('A'+len(sources)))
			}
			sources = append(sources, source)
		}
	}

	return sources
}
