package mediaidentity

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MatchInput 是待匹配资源的特征（来自资源站的原始字段）。
type MatchInput struct {
	Title         string
	OriginalTitle string
	Year          string
	MediaType     string
	Actors        string
	Directors     string
}

// MatchResult 是匹配结果。HardConflict 非空表示存在硬冲突（年份差太多、季号对不上等），
// 这种情况无论分数多高都不能自动采纳。
type MatchResult struct {
	MediaID      int
	Confidence   float64
	MatchedBy    string
	Status       string
	ReasonJSON   string
	HardConflict string
}

// matchFeature 是单个特征的打分明细（权重 × 相似度 = 得分），会原样存进复核理由里。
type matchFeature struct {
	Weight     float64 `json:"weight"`
	Similarity float64 `json:"similarity"`
	Score      float64 `json:"score"`
}

// matchReason 是写进 resource_match_candidates.reason 的完整打分说明，供后台复核时展示。
type matchReason struct {
	Confidence    float64                 `json:"confidence"`
	Features      map[string]matchFeature `json:"features"`
	HardConflicts []string                `json:"hard_conflicts"`
}

// 下面这些正则用于从片名里剥离季号、清晰度标签（1080p/BluRay）和中文标签（国语/中字）等噪音。
var (
	seasonEnglishPattern = regexp.MustCompile(`(?i)(?:\bseason\s*|\bs)(\d{1,2})\b`)
	seasonChinesePattern = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十两]+)\s*季`)
	releaseTagPattern    = regexp.MustCompile(`(?i)\b(?:2160p|1080p|720p|4k|uhd|blu-?ray|web-?dl|hdtv|hdr|x26[45])\b`)
	chineseTagPattern    = regexp.MustCompile(`(?:国语|粤语|中字|双语|修复版|加长版|导演剪辑版|高清|超清)`)
	yearPattern          = regexp.MustCompile(`(?:19|20)\d{2}`)
	nameSeparatorPattern = regexp.MustCompile(`[\s,，、/;；|]+`)
)

// MatchResource 是加权打分匹配（五层匹配里的第 4 层）：
// 先用别名表的三元组相似度粗筛出最多 20 个候选，再逐个打分，取分最高的一个。
func (store *PostgresStore) MatchResource(ctx context.Context, input MatchInput) (MatchResult, error) {
	titleKey, _ := matchTitleParts(input.Title)
	if titleKey == "" {
		return MatchResult{}, nil
	}
	rows, err := store.database.Query(ctx, mediaSelect+` WHERE id IN (
    SELECT media_id FROM media_aliases
    WHERE normalized_alias = $1 OR similarity(normalized_alias, $1) >= 0.45
    ORDER BY similarity(normalized_alias, $1) DESC
    LIMIT 20
) ORDER BY updated_at DESC`, titleKey)
	if err != nil {
		return MatchResult{}, fmt.Errorf("find media match candidates: %w", err)
	}
	defer rows.Close()
	results := make([]MatchResult, 0, 20)
	for rows.Next() {
		var media Media
		if err := scanMedia(rows, &media); err != nil {
			return MatchResult{}, fmt.Errorf("scan media match candidate: %w", err)
		}
		result := ScoreResourceMatch(input, media)
		if result.MediaID > 0 {
			results = append(results, result)
		}
	}
	if err := rows.Err(); err != nil {
		return MatchResult{}, fmt.Errorf("iterate media match candidates: %w", err)
	}
	if len(results) == 0 {
		return MatchResult{}, nil
	}
	sort.SliceStable(results, func(left, right int) bool {
		if results[left].Confidence != results[right].Confidence {
			return results[left].Confidence > results[right].Confidence
		}
		leftClean := results[left].HardConflict == ""
		rightClean := results[right].HardConflict == ""
		return leftClean && !rightClean
	})
	return results[0], nil
}

// ScoreResourceMatch 给「一条资源 vs 一部媒体」打分。
// 权重：片名 0.40、年份 0.15、类型 0.15、原名 0.10、演员 0.10、导演 0.10。
// 年份差 2 年以上、季号对不上、类型不符都会记为硬冲突。
func ScoreResourceMatch(input MatchInput, media Media) MatchResult {
	resourceTitle, resourceSeason := matchTitleParts(input.Title)
	candidateTitle, candidateSeason := matchTitleParts(media.Title)
	resourceOriginal, _ := matchTitleParts(input.OriginalTitle)
	candidateOriginal, _ := matchTitleParts(media.OriginalTitle)
	features := map[string]matchFeature{}
	addFeature := func(name string, weight, similarity float64) {
		similarity = clamp01(similarity)
		features[name] = matchFeature{Weight: weight, Similarity: round4(similarity), Score: round4(weight * similarity)}
	}

	addFeature("title", 0.40, titleSimilarity(resourceTitle, candidateTitle))
	resourceYear, candidateYear := parseYear(input.Year), parseYear(media.Year)
	yearSimilarity := 0.0
	hardConflicts := make([]string, 0, 3)
	if resourceYear > 0 && candidateYear > 0 {
		difference := absInt(resourceYear - candidateYear)
		switch difference {
		case 0:
			yearSimilarity = 1
		case 1:
			yearSimilarity = 0.5
		default:
			hardConflicts = append(hardConflicts, "year_mismatch")
		}
	}
	addFeature("year", 0.15, yearSimilarity)

	resourceType, candidateType := normalizeMatchMediaType(input.MediaType), normalizeMatchMediaType(media.MediaType)
	typeSimilarity := 0.0
	if resourceType != "" && candidateType != "" {
		if resourceType == candidateType {
			typeSimilarity = 1
		} else {
			hardConflicts = append(hardConflicts, "media_type_mismatch")
		}
	}
	addFeature("media_type", 0.10, typeSimilarity)

	seasonSimilarity := 0.0
	if resourceSeason > 0 || candidateSeason > 0 {
		if resourceSeason > 0 && candidateSeason > 0 && resourceSeason == candidateSeason {
			seasonSimilarity = 1
		} else if resourceSeason > 0 && candidateSeason > 0 {
			hardConflicts = append(hardConflicts, "season_mismatch")
		}
	} else if resourceType == "movie" && candidateType == "movie" {
		seasonSimilarity = 1
	}
	addFeature("season", 0.15, seasonSimilarity)

	peopleSimilarity := (setOverlap(input.Directors, media.Directors) + setOverlap(input.Actors, media.Actors)) / 2
	addFeature("people", 0.10, peopleSimilarity)
	addFeature("original_title", 0.10, titleSimilarity(resourceOriginal, candidateOriginal))

	confidence := 0.0
	for _, feature := range features {
		confidence += feature.Score
	}
	confidence = round4(confidence)
	reason := matchReason{Confidence: confidence, Features: features, HardConflicts: hardConflicts}
	reasonJSON, _ := json.Marshal(reason)
	status, conflict := "review", ""
	if len(hardConflicts) > 0 {
		status, conflict = "rejected", strings.Join(hardConflicts, ",")
	}
	return MatchResult{MediaID: media.ID, Confidence: confidence, MatchedBy: "weighted_features",
		Status: status, ReasonJSON: string(reasonJSON), HardConflict: conflict}
}

// matchTitleParts 把片名拆成「归一化后的主标题」和「季号」。
func matchTitleParts(value string) (string, int) {
	value, season := titleBaseParts(value)
	return NormalizeTitle(value), season
}

// titleBaseParts 去掉季号和各种发布标签，得到干净的主标题。
func titleBaseParts(value string) (string, int) {
	value = strings.ToLower(norm.NFKC.String(strings.TrimSpace(value)))
	season := 0
	if matches := seasonEnglishPattern.FindStringSubmatch(value); len(matches) == 2 {
		season, _ = strconv.Atoi(matches[1])
		value = seasonEnglishPattern.ReplaceAllString(value, " ")
	} else if matches := seasonChinesePattern.FindStringSubmatch(value); len(matches) == 2 {
		season = parseChineseNumber(matches[1])
		value = seasonChinesePattern.ReplaceAllString(value, " ")
	}
	value = releaseTagPattern.ReplaceAllString(value, " ")
	value = chineseTagPattern.ReplaceAllString(value, " ")
	return strings.Join(strings.Fields(value), " "), season
}

// TitleSeasonNumber 从标题或原始标题中提取明确的季数。
// 返回 0 表示它是整剧标题，或标题没有提供足够信息判断具体季度。
func TitleSeasonNumber(values ...string) int {
	for _, value := range values {
		if _, season := matchTitleParts(value); season > 0 {
			return season
		}
	}
	return 0
}

// TitleBase 返回去掉季度和发布标签后的标题匹配键。
// 它用于以季度条目标题回查整剧资料，例如 "Silo Season 3" -> "silo"。
func TitleBase(values ...string) string {
	for _, value := range values {
		if title, _ := titleBaseParts(value); title != "" {
			return title
		}
	}
	return ""
}

// titleSimilarity 用字符二元组（bigram）计算标题相似度，对中文比编辑距离更稳。
func titleSimilarity(left, right string) float64 {
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}
	leftBigrams, rightBigrams := runeBigrams(left), runeBigrams(right)
	intersection := 0
	remaining := make(map[string]int, len(rightBigrams))
	for _, value := range rightBigrams {
		remaining[value]++
	}
	for _, value := range leftBigrams {
		if remaining[value] > 0 {
			intersection++
			remaining[value]--
		}
	}
	return float64(2*intersection) / float64(len(leftBigrams)+len(rightBigrams))
}

// runeBigrams 把字符串切成相邻两字的组合。
func runeBigrams(value string) []string {
	characters := []rune(value)
	if len(characters) < 2 {
		return []string{value}
	}
	result := make([]string, 0, len(characters)-1)
	for index := 0; index < len(characters)-1; index++ {
		result = append(result, string(characters[index:index+2]))
	}
	return result
}

// normalizeMatchMediaType 归一媒体类型，识别不了返回空串（空串不参与打分，也不算冲突）。
func normalizeMatchMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case value == "movie" || value == "film" || strings.Contains(value, "电影"):
		return "movie"
	case value == "tv" || value == "series" || value == "season" || value == "show" || value == "animation" ||
		strings.Contains(value, "电视") || strings.Contains(value, "连续剧") || strings.Contains(value, "动漫") || strings.Contains(value, "综艺"):
		return "tv"
	default:
		return ""
	}
}

// setOverlap 计算两组人名的重合度。
func setOverlap(left, right string) float64 {
	leftSet, rightSet := nameSet(left), nameSet(right)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}
	intersection := 0
	for name := range leftSet {
		if rightSet[name] {
			intersection++
		}
	}
	return float64(intersection) / float64(maxInt(len(leftSet), len(rightSet)))
}

// nameSet 把人名串按各种分隔符拆成集合。
func nameSet(value string) map[string]bool {
	result := make(map[string]bool)
	for _, name := range nameSeparatorPattern.Split(value, -1) {
		if normalized := NormalizeTitle(name); normalized != "" {
			result[normalized] = true
		}
	}
	return result
}

// parseYear 从字符串里抽出四位年份。
func parseYear(value string) int {
	match := yearPattern.FindString(value)
	result, _ := strconv.Atoi(match)
	return result
}

// parseChineseNumber 解析「第三季」里的中文数字。
func parseChineseNumber(value string) int {
	if utf8.RuneCountInString(value) == 0 {
		return 0
	}
	if number, err := strconv.Atoi(value); err == nil {
		return number
	}
	digits := map[rune]int{'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(value)
	if runes[0] == '十' {
		if len(runes) == 1 {
			return 10
		}
		return 10 + digits[runes[1]]
	}
	if len(runes) == 1 {
		return digits[runes[0]]
	}
	if len(runes) >= 2 && runes[1] == '十' {
		result := digits[runes[0]] * 10
		if len(runes) > 2 {
			result += digits[runes[2]]
		}
		return result
	}
	return 0
}

// round4/clamp01/absInt 是打分用的小工具。
func round4(value float64) float64 { return math.Round(value*10000) / 10000 }

// clamp01 把分数限制在 0 到 1 之间。
func clamp01(value float64) float64 { return math.Max(0, math.Min(1, value)) }

// absInt 取绝对值。
func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
