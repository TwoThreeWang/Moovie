package playurl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/mediatype"
)

// Entry 只存在于后端内存，内容身份与线路、版本、电影分段分别保存。
type Entry struct {
	Key, LineKey, LineLabel               string
	LineOrder, Order                      int
	UnitType                              string
	Season                                int
	EpisodeKey, Label, Quality, Part, URL string
}

var versionLabel = regexp.MustCompile(`(?i)^(正片|完整版|抢先版?|超高清|蓝光|超清|高清|标清|流畅|原画|原盘|画质|2160|1080|720|480|360|4K|P|WEBDL|WEBRIP|HDTV|BDRIP|HDRIP|WEB|DVD|CAM|BD|HD|SD|TC|HC|TS|国语|粤语|英语|日语|韩语|闽南语|原声|配音|译制|中英|中字|双语|无字|字幕|内嵌|版|[-_. ·、])+$`)
var explicitSeason = regexp.MustCompile(`(?i)(?:S(\d{1,2})\s*E|第(\d{1,2})季)`)
var namedSeason = regexp.MustCompile(`第([一二三四五六七八九十\d]+)季`)

func IsVersion(label string) bool { return versionLabel.MatchString(strings.TrimSpace(label)) }

// SeasonFromTitle 仅使用明确的季标记；普通数字和年份不当季号。
func SeasonFromTitle(title string) int {
	m := namedSeason.FindStringSubmatch(title)
	if len(m) == 0 {
		return 0
	}
	if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
		return n
	}
	for i, word := range []string{"一", "二", "三", "四", "五", "六", "七", "八", "九", "十"} {
		if m[1] == word {
			return i + 1
		}
	}
	return 1
}

// Entries 将同一份过滤结果映射到规范内容。未知标签保留，不让错误类型吞掉整季。
func Entries(raw, mediaType, title string) []Entry {
	sources := Parse(raw)
	kind, defaultSeason := mediatype.Normalize(mediaType), SeasonFromTitle(title)
	if defaultSeason == 0 {
		defaultSeason = 1
	}
	feature := kind == "movie"
	if kind == "cartoon" {
		feature = len(sources) > 0
		for _, source := range sources {
			for _, ep := range source.Episodes {
				if !IsVersion(ep.Title) && MoviePart(ep.Title) == "" {
					feature = false
				}
			}
		}
	}
	var result []Entry
	for lineIndex, source := range sources {
		lineKey := "default"
		if lineIndex > 0 {
			lineKey = fmt.Sprintf("line-%02d", lineIndex+1)
		}
		for index, ep := range source.Episodes {
			season, key := NormalizeEpisodeLabel(ep.Title)
			unitType, quality, part := "episode", "", ""
			if feature && (IsVersion(ep.Title) || MoviePart(ep.Title) != "" || !strings.HasPrefix(key, "S") && !dateLabel.MatchString(ep.Title) && !strings.ContainsAny(ep.Title, "集期话")) {
				season, key, unitType = 0, "feature", "feature"
				part = MoviePart(ep.Title)
				if part == "" {
					quality = ep.Title
				}
			} else if season == 0 {
				// 连续内容的“正片”不能凭名称并进电影单元。
				season, key = defaultSeason, "正片"
			} else if defaultSeason > 1 && !explicitSeason.MatchString(ep.Title) && strings.HasPrefix(key, "S01E") {
				season, key = defaultSeason, fmt.Sprintf("S%02d%s", defaultSeason, key[3:])
			}
			identity := lineKey + "\x00" + key + "\x00" + ep.Title + "\x00" + ep.URL
			sum := sha256.Sum256([]byte(identity))
			result = append(result, Entry{Key: hex.EncodeToString(sum[:16]), LineKey: lineKey, LineLabel: source.Name,
				LineOrder: lineIndex, Order: index, UnitType: unitType, Season: season, EpisodeKey: key,
				Label: ep.Title, Quality: quality, Part: part, URL: ep.URL})
		}
	}
	return result
}

// MoviePart 识别明确的电影分段标签；同时用于旧进度的继续播放。
func MoviePart(label string) string {
	switch strings.TrimSpace(label) {
	case "上", "上部", "上集", "正片上", "正片（上）":
		return "上"
	case "中", "中部", "中集", "正片中", "正片（中）":
		return "中"
	case "下", "下部", "下集", "正片下", "正片（下）":
		return "下"
	}
	return ""
}
