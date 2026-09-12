// Package mediatype 统一作品展示分类，不用题材或未知值猜测电影。
package mediatype

import "strings"

// Normalize 保留电影、剧集、综艺、动漫四类；未知分类留空等待补充。
func Normalize(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "cartoon" || value == "anime" || value == "animation" || strings.Contains(value, "动漫") || strings.Contains(value, "动画"):
		return "cartoon"
	case value == "show" || value == "variety" || strings.Contains(value, "综艺"):
		return "show"
	case value == "tv" || value == "series" || value == "season" || strings.Contains(value, "电视剧") || strings.Contains(value, "连续剧") || strings.HasSuffix(value, "剧"):
		return "tv"
	case value == "movie" || value == "film" || strings.Contains(value, "电影"):
		return "movie"
	}
	// 这些明确的资源站电影子类可以归一；纪录片等同时存在系列节目的分类不猜。
	switch value {
	case "动作片", "喜剧片", "爱情片", "科幻片", "恐怖片", "惊悚片", "剧情片", "战争片", "犯罪片", "悬疑片":
		return "movie"
	}
	return ""
}
