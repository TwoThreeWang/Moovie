package danmaku

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9A-F]{6}$`)
	controlPattern  = regexp.MustCompile(`[\x00-\x1F\x7F]`)
	formatPattern   = regexp.MustCompile(`\p{Cf}`)
	seasonCJK       = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十]+)\s*[季部]`)
	seasonEnglish   = regexp.MustCompile(`(?i)\bs(?:eason)?\s*(\d{1,2})\b`)
	titleNoise      = regexp.MustCompile(`(?i)[（(\[【][^）)\]】]*[）)\]】]|4k|2160p|1080p|720p|web-?dl|蓝光|国语|粤语|中字|高清|抢先版|未删减`)
	episodePure     = regexp.MustCompile(`^\s*(\d{1,4})\s*$`)
	episodeCJK      = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百零两]+)\s*[集话話期]`)
	episodeEnglish  = regexp.MustCompile(`(?i)^\s*e(?:p|pisode)?\.?\s*(\d{1,4})\s*$`)
)

func buildVodKey(title string, season, episode int) string {
	return fmt.Sprintf("%s|S%02d|E%03d", strings.ToLower(title), season, episode)
}

func sanitizeText(value string) string {
	value = controlPattern.ReplaceAllString(value, " ")
	value = formatPattern.ReplaceAllString(value, "")
	return strings.Join(strings.Fields(value), " ")
}

func splitSeason(title string) (int, string) {
	season, clean := 1, title
	if matches := seasonCJK.FindStringSubmatch(title); len(matches) == 2 {
		if number := chineseNumber(matches[1]); number > 0 {
			season = number
		}
		clean = strings.Replace(clean, matches[0], " ", 1)
	} else if matches := seasonEnglish.FindStringSubmatch(title); len(matches) == 2 {
		if number, err := strconv.Atoi(matches[1]); err == nil && number > 0 {
			season = number
		}
		clean = strings.Replace(clean, matches[0], " ", 1)
	}
	clean = titleNoise.ReplaceAllString(clean, " ")
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		clean = strings.TrimSpace(title)
	}
	return season, clean
}

func parseEpisode(raw string) int {
	value := strings.TrimSpace(raw)
	if matches := episodePure.FindStringSubmatch(value); len(matches) == 2 {
		number, _ := strconv.Atoi(matches[1])
		return number
	}
	if matches := episodeCJK.FindStringSubmatch(value); len(matches) == 2 {
		return chineseNumber(matches[1])
	}
	if matches := episodeEnglish.FindStringSubmatch(value); len(matches) == 2 {
		number, _ := strconv.Atoi(matches[1])
		return number
	}
	return 0
}

func chineseNumber(value string) int {
	if number, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return number
	}
	digits := map[rune]int{'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	section := 0
	for _, character := range value {
		switch character {
		case '十':
			if section == 0 {
				section = 1
			}
			section *= 10
		case '百':
			if section == 0 {
				section = 1
			}
			section *= 100
		default:
			digit, exists := digits[character]
			if !exists {
				return 0
			}
			if section > 0 && section%10 == 0 {
				section += digit
			} else {
				section = section*10 + digit
			}
		}
	}
	return section
}
