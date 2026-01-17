package utils

import (
	"regexp"
	"strings"
)

// CleanMovieTitle 清理资源网标题中的杂质信息
func CleanMovieTitle(title string) string {
	if title == "" {
		return ""
	}

	// 1. 移除方括号及其内容 (通常是压制组、分辨率等标签)
	// 【...】 [ ... ]
	reBrackets := regexp.MustCompile(`[\[【].*?[\]】]`)
	title = reBrackets.ReplaceAllString(title, " ")

	// 2. 移除圆括号 (通常是英文名、年份或备注)
	reParens := regexp.MustCompile(`\(.*?\)`)
	title = reParens.ReplaceAllString(title, " ")

	// 3. 移除常见的视频质量和压制提示词 (不区分大小写)
	reQuality := regexp.MustCompile(`(?i)(1080p|720p|4k|2k|hd|bd|web-dl|web-rip|hr-hd|dvd)`)
	title = reQuality.ReplaceAllString(title, " ")

	// 4. 移除常见的版本和语言说明
	reMeta := regexp.MustCompile(`(?i)(中字|双语|国语|粤语|简中|繁中|特效|修正|加长版|未删减|导演剪辑版|蓝光|加更版)`)
	title = reMeta.ReplaceAllString(title, " ")

	// 5. 移除剧集信息 (保留第N季，但移除第N集)
	// 关键：保留核心季信息，因不同季通常被视为不同资源，但移除具体的集数
	reEpisodes := regexp.MustCompile(`(?i)(第\d+集|全\d+集|Episode\s*\d+|Ep\s*\d+)`)
	title = reEpisodes.ReplaceAllString(title, " ")

	// 6. 替换点、下划线为控制，并移除多余空格
	title = strings.ReplaceAll(title, ".", " ")
	title = strings.ReplaceAll(title, "_", " ")

	// 7. 处理多余空格
	fields := strings.Fields(title)
	return strings.Join(fields, " ")
}

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
			var title, url string
			if len(parts) >= 2 {
				// 格式: 标题$链接
				title = parts[0]
				url = parts[1]
			} else if len(parts) == 1 {
				// 格式: 链接 (没有标题)
				title = "正片"
				url = parts[0]
			}

			// 只解析 m3u8 链接
			if strings.Contains(url, ".m3u8") {
				source.Episodes = append(source.Episodes, PlayEpisode{
					Title: title,
					URL:   url,
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
