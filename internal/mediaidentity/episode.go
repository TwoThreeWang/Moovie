package mediaidentity

import (
	"regexp"
	"strconv"
	"strings"
)

// episodePatterns 按优先级排列的集数识别规则，兼顾中英文写法。
var episodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)S(\d{1,2})\s*E(\d{1,3})`),
	regexp.MustCompile(`第\s*(\d{1,2})\s*季\s*第\s*(\d{1,3})\s*[集话]`),
	regexp.MustCompile(`第\s*(\d{1,3})\s*[集话]`),
	regexp.MustCompile(`(?i)\b(?:EP?|第)\s*[-._ ]?(\d{1,3})\b`),
}

// NormalizeEpisodeLabel 生成稳定的季集键，同时保留原标签用于展示。
// 无法识别的标签会被保留，绝不能让第 3 集在换源时错误退回第 1 集。
func NormalizeEpisodeLabel(label string) (season int, key string) {
	label = strings.TrimSpace(label)
	if label == "" {
		return 1, "S01E01"
	}
	for index, pattern := range episodePatterns {
		matches := pattern.FindStringSubmatch(label)
		if len(matches) == 0 {
			continue
		}
		if index == 1 {
			return episodeKey(parseNumber(matches[1], 1), parseNumber(matches[2], 1))
		}
		if index == 0 {
			return episodeKey(parseNumber(matches[1], 1), parseNumber(matches[2], 1))
		}
		return episodeKey(1, parseNumber(matches[1], 1))
	}
	// Provider API 常用纯数字表示集数，但年份和任意 ID 不能被误判为集数。
	if number, err := strconv.Atoi(label); err == nil && number > 0 && number < 500 {
		return episodeKey(1, number)
	}
	return 1, strings.ToUpper(label)
}

// episodeKey 生成 S01E01 形式的键，季/集不合法时归一到 1。
func episodeKey(season, episode int) (int, string) {
	if season < 1 {
		season = 1
	}
	if episode < 1 {
		episode = 1
	}
	return season, "S" + twoDigits(season) + "E" + twoDigits(episode)
}

// parseNumber 解析数字，失败或小于 1 时返回 fallback。
func parseNumber(value string, fallback int) int {
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return fallback
	}
	return number
}

// twoDigits 补足两位。
func twoDigits(number int) string {
	if number < 10 {
		return "0" + strconv.Itoa(number)
	}
	return strconv.Itoa(number)
}
