package mediaidentity

import (
	"fmt"
	"sort"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
)

// FeatureEpisodeKey 是电影正片的统一集键。资源站习惯把 720P、HD中字、TC国语
// 这些清晰度/版本当成"分集"塞进同一条线路，各家叫法还不一样；如果照单全收，
// 选集网格就会变成一排杂乱的清晰度按钮。折叠到同一个键之后，版本差异改由
// 候选自己的 Quality 承载，在线路列表里作为版本标签展示。
const FeatureEpisodeKey = "S01E01"

// qualityVariantTokens 是资源站给正片起名时用的画质、片源、语言词根，按长度降序排列，
// 供 isQualityVariantLabel 从标签头部逐段剥离。补词直接往这里加。
var qualityVariantTokens = func() []string {
	tokens := []string{
		"正片", "完整版", "抢先版", "抢先", "预告片", "预告", "片花", "花絮", "彩蛋",
		"超高清", "蓝光", "超清", "高清", "标清", "流畅", "原画", "原盘", "画质",
		"2160", "1080", "720", "480", "360", "4K", "P",
		"WEBDL", "WEBRIP", "HDTV", "BDRIP", "HDRIP", "HDRip",
		"WEB", "DVD", "CAM", "BD", "HD", "SD", "TC", "HC", "TS",
		"国语", "粤语", "英语", "日语", "韩语", "闽南语", "原声", "配音", "译制",
		"中英", "中字", "双语", "无字", "字幕", "内嵌", "版", "-", "_", ".", " ", "·", "、",
	}
	sort.SliceStable(tokens, func(i, j int) bool { return len(tokens[i]) > len(tokens[j]) })
	return tokens
}()

// isQualityVariantLabel 判断标签是清晰度/语言版本还是集次。
// "HD中字""TC国语""720P"能被词根剥干净，综艺的"第5期""20240315"、剧集的"第01集"剥不干净——
// 豆瓣按 movie/tv/show 顺序试端点，综艺常被错标成 movie，只认 media_type 会把整季并成一集。
func isQualityVariantLabel(label string) bool {
	rest := strings.ToUpper(strings.TrimSpace(label))
	if rest == "" {
		return false
	}
	for trimmed := true; trimmed && rest != ""; {
		trimmed = false
		for _, token := range qualityVariantTokens {
			if strings.HasPrefix(rest, strings.ToUpper(token)) {
				rest, trimmed = rest[len(token):], true
				break
			}
		}
	}
	return rest == ""
}

// ParseResourceEpisodes 把资源站的播放地址串（多线路、多集，用分隔符拼在一起）
// 解析成结构化的剧集候选列表。电影的每条地址都是同一部正片的不同版本。
func ParseResourceEpisodes(sourceKey, vodID string, mediaID int, mediaType, raw string) []Episode {
	sources := playurl.Parse(raw)
	isMovie := isMovieMediaType(mediaType)
	result := make([]Episode, 0)
	for lineOrder, source := range sources {
		lineKey := "default"
		if lineOrder > 0 {
			lineKey = fmt.Sprintf("line-%02d", lineOrder+1)
		}
		for sortOrder, candidate := range source.Episodes {
			season, key := NormalizeEpisodeLabel(candidate.Title)
			unitType, quality := "episode", ""
			// 元数据说是电影、标签本身也确实是版本名，两个条件都成立才折叠成正片。
			if isMovie && isQualityVariantLabel(candidate.Title) {
				season, key = 1, FeatureEpisodeKey
				unitType, quality = "feature", strings.TrimSpace(candidate.Title)
			}
			result = append(result, Episode{LineKey: lineKey, LineLabel: source.Name, LineOrder: lineOrder,
				SourceKey: sourceKey, VodID: vodID, MediaID: mediaID, UnitType: unitType,
				SeasonNumber: season, EpisodeKey: key, EpisodeLabel: candidate.Title, PlayURL: candidate.URL,
				Format: "m3u8", Quality: quality, SortOrder: sortOrder})
		}
	}
	return result
}

// isMovieMediaType 判断分类名是否表示电影。这里只认规范媒体库的类型和明确写着
// "电影"的分类：资源站的"动作片""国产剧"太杂，宁可漏判退回按集处理，
// 也不能把一部电视剧的所有集错折叠成一部正片。
func isMovieMediaType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "movie" || value == "film" || strings.Contains(value, "电影")
}
