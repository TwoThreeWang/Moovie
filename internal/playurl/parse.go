// Package playurl 解析 AppleCMS 的播放地址字段。
// 格式：源之间用 $$$ 分隔，集之间用 #，集内用 $ 分「集名$播放地址」。
// 只保留 .m3u8 地址，其他格式（mp4 直链、网页播放器）一律丢弃。
package playurl

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
)

// Source 是一个播放源（一部片子可能有多个源）。
type Source struct {
	Name     string
	Episodes []Episode
}

// Episode 是一集及其播放地址。
type Episode struct {
	Title string
	URL   string
}

var tcLabel = regexp.MustCompile(`(?i)(^|[^a-z])TC([^a-z]|$)`)

// Excluded 只检查标签，不扫描 URL，避免误伤地址里的 tc 字符。
func Excluded(label string) bool {
	for _, word := range []string{"预告", "片花", "花絮", "彩蛋"} {
		if strings.Contains(label, word) {
			return true
		}
	}
	return tcLabel.MatchString(label)
}

// Parse 是采集、播放、下载和 TVBox 共用的过滤入口，只保留可直接播放的 HLS。
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
			parts := strings.SplitN(episodeSegment, "$", 2)
			title, target := "", ""
			switch {
			case len(parts) >= 2:
				title, target = parts[0], parts[1]
			case len(parts) == 1:
				title, target = "正片", parts[0]
			}
			title, target = strings.TrimSpace(title), strings.TrimSpace(target)
			if title == "" {
				title = "正片"
			}
			parsed, err := url.Parse(target)
			if !Excluded(title) && err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
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

// Clean 保存过滤后的原始分隔格式；备注只用于没有明确版本标签的正片项。
func Clean(raw string, remarks ...string) string {
	sources := Parse(raw)
	var lines []string
	for _, source := range sources {
		var episodes []string
		for _, episode := range source.Episodes {
			if episode.Title == "正片" && len(remarks) > 0 && Excluded(remarks[0]) {
				continue
			}
			episodes = append(episodes, episode.Title+"$"+episode.URL)
		}
		if len(episodes) > 0 {
			lines = append(lines, strings.Join(episodes, "#"))
		}
	}
	return strings.Join(lines, "$$$")
}

// Version 用播放列表自身校验迟到的上报，海报或标题更新不影响资源统计。
func Version(raw string) string {
	sum := md5.Sum([]byte(Clean(raw)))
	return hex.EncodeToString(sum[:16])
}
