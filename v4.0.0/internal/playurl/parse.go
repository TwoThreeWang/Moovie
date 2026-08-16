package playurl

import "strings"

type Source struct {
	Name     string
	Episodes []Episode
}

type Episode struct {
	Title string
	URL   string
}

// Parse 保留旧版 AppleCMS HLS 解析规则，并为播放渲染和影子剧集索引提供统一解析器。
func Parse(raw string) []Source {
	if raw == "" {
		return nil
	}
	sources := make([]Source, 0)
	for _, segment := range strings.Split(raw, "$$$") {
		if segment == "" {
			continue
		}
		source := Source{}
		for _, episodeSegment := range strings.Split(segment, "#") {
			if episodeSegment == "" {
				continue
			}
			parts := strings.Split(episodeSegment, "$")
			title, target := "", ""
			switch {
			case len(parts) >= 2:
				title, target = parts[0], parts[1]
			case len(parts) == 1:
				title, target = "正片", parts[0]
			}
			if strings.Contains(strings.ToLower(target), ".m3u8") {
				source.Episodes = append(source.Episodes, Episode{Title: title, URL: target})
			}
		}
		if len(source.Episodes) > 0 {
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
