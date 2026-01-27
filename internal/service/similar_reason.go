package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
)

// SimilarMovieWithReason 带推荐理由的相似电影
type SimilarMovieWithReason struct {
	Movie      model.Movie
	Reason     string  `json:"reason"`
	ReasonType string  `json:"reason_type"`
	Similarity float64 `json:"similarity"`
}

// RecommendationService 推荐服务
type RecommendationService struct {
	movieRepo *repository.MovieRepository
}

// NewRecommendationService 创建推荐服务
func NewRecommendationService(movieRepo *repository.MovieRepository) *RecommendationService {
	return &RecommendationService{
		movieRepo: movieRepo,
	}
}

// MovieFeatures 提取出的电影特征，用于高性能匹配
type MovieFeatures struct {
	Genres    map[string]struct{}
	Directors map[string]struct{}
	Actors    map[string]struct{}
	Year      int
	Rating    float64
	Title     string
}

// extractMovieFeatures 将电影模型转换为特征结构，用于快速匹配
func extractMovieFeatures(m model.Movie) MovieFeatures {
	f := MovieFeatures{
		Genres:    make(map[string]struct{}),
		Directors: make(map[string]struct{}),
		Actors:    make(map[string]struct{}),
		Rating:    m.Rating,
		Title:     m.Title,
	}

	for _, g := range parseGenres(m.Genres) {
		f.Genres[g] = struct{}{}
	}
	for _, d := range parsePersonNames(m.Directors) {
		f.Directors[d] = struct{}{}
	}
	for _, a := range parsePersonNames(m.Actors) {
		f.Actors[a] = struct{}{}
	}
	fmt.Sscanf(m.Year, "%d", &f.Year)
	return f
}

// FindSimilarWithReasons 根据向量相似度查找相似电影并生成推荐理由
func (s *RecommendationService) FindSimilarWithReasons(doubanID string, limit int) ([]SimilarMovieWithReason, *model.Movie, error) {
	// 1. 先获取源电影信息
	sourceMovie, err := s.movieRepo.FindByDoubanID(doubanID)
	if err != nil {
		return nil, nil, err
	}

	// 预解析源电影特征（加速循环）
	sourceFeatures := extractMovieFeatures(*sourceMovie)

	// 2. 获取相似电影
	similarMovies, err := s.movieRepo.FindSimilar(doubanID, limit)
	if err != nil {
		return nil, nil, err
	}

	// 3. 为每部电影生成推荐理由
	result := make([]SimilarMovieWithReason, 0, len(similarMovies))
	for _, movie := range similarMovies {
		targetFeatures := extractMovieFeatures(movie)
		reason, reasonType, score := GenerateRecommendationReasonV2(sourceFeatures, targetFeatures, *sourceMovie, movie)

		result = append(result, SimilarMovieWithReason{
			Movie:      movie,
			Reason:     reason,
			ReasonType: reasonType,
			Similarity: score,
		})
	}

	return result, sourceMovie, nil
}

// parseGenres 解析类型字符串
func parseGenres(genresStr string) []string {
	if genresStr == "" {
		return []string{}
	}

	genres := strings.Split(genresStr, ",")
	result := make([]string, 0, len(genres))
	for _, genre := range genres {
		if trimmed := strings.TrimSpace(genre); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// parsePersonNames 解析人名字符串（导演、主演等）
func parsePersonNames(namesStr string) []string {
	if namesStr == "" {
		return []string{}
	}

	// 尝试解析是否为 JSON 格式: [{"id":"","name":"邓超"}, ...]
	if strings.HasPrefix(namesStr, "[") {
		var persons []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(namesStr), &persons); err == nil {
			result := make([]string, 0, len(persons))
			for _, p := range persons {
				if trimmed := strings.TrimSpace(p.Name); trimmed != "" {
					result = append(result, trimmed)
				}
			}
			return result
		}
	}

	// 回退到原有的普通字符串处理逻辑
	// 统一处理中文逗号和英文逗号，或者斜线 (常见的电影信息分隔符)
	namesStr = strings.ReplaceAll(namesStr, "，", ",")
	namesStr = strings.ReplaceAll(namesStr, "/", ",")

	names := strings.Split(namesStr, ",")
	result := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// calculateRatingSimilarity 计算评分相似度得分
func calculateRatingSimilarity(sourceRating, targetRating float64) float64 {
	if sourceRating <= 0 || targetRating <= 0 {
		return 0.5
	}
	diff := math.Abs(sourceRating - targetRating)
	// 评分差距在 1 分以内视为高度相似，给予 1.0 的加权得分
	if diff <= 1.0 {
		return 1.0
	}
	// 差距在 2.5 分以上相似度较低
	if diff > 2.5 {
		return 0.3
	}
	// 线性衰减
	return 1.0 - (diff-1.0)/1.5*0.7
}

// calculateGenreSimilarityFast 高性能计算类型重合
func calculateGenreSimilarityFast(source, target MovieFeatures) (float64, []string) {
	common := []string{}
	for g := range target.Genres {
		if _, ok := source.Genres[g]; ok {
			common = append(common, g)
		}
	}
	maxLen := math.Max(float64(len(source.Genres)), float64(len(target.Genres)))
	if maxLen == 0 {
		return 0, common
	}
	return float64(len(common)) / maxLen, common
}

// calculatePersonSimilarityFast 高性能计算人员重合
func calculatePersonSimilarityFast(source, target map[string]struct{}) (float64, []string) {
	common := []string{}
	for p := range target {
		if _, ok := source[p]; ok {
			common = append(common, p)
		}
	}
	maxLen := math.Max(float64(len(source)), float64(len(target)))
	if maxLen == 0 {
		return 0, common
	}
	return float64(len(common)) / maxLen, common
}

// calculateEraSimilarityFast 高性能年代相似度
func calculateEraSimilarityFast(sourceYear, targetYear int) float64 {
	if sourceYear == 0 || targetYear == 0 {
		return 0.5
	}
	diff := math.Abs(float64(sourceYear - targetYear))
	switch {
	case diff <= 1:
		return 1.0
	case diff <= 3:
		return 0.8
	case diff <= 5:
		return 0.6
	case diff <= 10:
		return 0.4
	default:
		return 0.2
	}
}

type reasonCandidate struct {
	reason     string
	reasonType string
	score      float64
}

// GenerateRecommendationReasonV2 优化后的推荐理由生成算法（基于评分和高性能匹配）
func GenerateRecommendationReasonV2(src, tgt MovieFeatures, sourceMovie, targetMovie model.Movie) (string, string, float64) {
	genreSim, commonGenres := calculateGenreSimilarityFast(src, tgt)
	dirSim, commonDirs := calculatePersonSimilarityFast(src.Directors, tgt.Directors)
	actSim, commonActors := calculatePersonSimilarityFast(src.Actors, tgt.Actors)
	rateSim := calculateRatingSimilarity(src.Rating, tgt.Rating)
	eraSim := calculateEraSimilarityFast(src.Year, tgt.Year)

	totalSimilarity := genreSim*0.4 + dirSim*0.25 + actSim*0.2 + rateSim*0.1 + eraSim*0.05

	candidates := make([]reasonCandidate, 0, 8)

	// 1. 系列/续集探测 (极高分)
	if strings.HasPrefix(tgt.Title, src.Title) || strings.HasPrefix(src.Title, tgt.Title) {
		candidates = append(candidates, reasonCandidate{
			reason:     "该系列作品的延续，带你深入了解其光影宇宙",
			reasonType: "series",
			score:      1.5,
		})
	}

	// 2. 导演匹配 (高分)
	if dirSim > 0.4 && len(commonDirs) > 0 {
		score := 0.9 + dirSim
		candidates = append(candidates, reasonCandidate{
			reason:     fmt.Sprintf("由同位导演 %s 执导，叙事风格与艺术造诣一脉相承", commonDirs[0]),
			reasonType: "director",
			score:      score,
		})
	}

	// 3. 核心主演匹配
	if actSim > 0.2 && len(commonActors) > 0 {
		candidates = append(candidates, reasonCandidate{
			reason:     fmt.Sprintf("同样由 %s 主演，演技表现与角色气质依然出众", commonActors[0]),
			reasonType: "actor",
			score:      0.8 + actSim,
		})
	}

	// 4. 类型匹配及复合逻辑
	coreGenres := []string{"科幻", "悬疑", "惊悚", "动作", "喜剧", "爱情", "剧情", "战争", "历史"}
	coreMatch := []string{}
	for _, g := range commonGenres {
		if contains(coreGenres, g) {
			coreMatch = append(coreMatch, g)
		}
	}

	if len(coreMatch) > 0 {
		genreDesc := strings.Join(coreMatch, "、")
		baseScore := 0.7 + genreSim
		reason := fmt.Sprintf("同属优质%s片，风格与本作高度契合", genreDesc)

		if contains(coreMatch, "科幻") || contains(coreMatch, "悬疑") {
			reason = fmt.Sprintf("同属高口碑%s片，带给你类似的脑力激荡与震撼感", genreDesc)
			baseScore += 0.1
		}

		candidates = append(candidates, reasonCandidate{
			reason:     reason,
			reasonType: "genre",
			score:      baseScore,
		})
	}

	// 5. 高分神作维度
	if src.Rating > 8.5 && tgt.Rating > 8.5 && genreSim > 0.3 {
		candidates = append(candidates, reasonCandidate{
			reason:     fmt.Sprintf("两部作品均为 %.1f+ 的顶级神作，艺术水准极高", 8.5),
			reasonType: "masterpiece",
			score:      1.2,
		})
	}

	// 6. 语义内核 (深度挖掘)
	if targetMovie.EmbeddingContent != "" {
		keywords := extractSemanticKeywords(targetMovie.EmbeddingContent)
		if len(keywords) > 0 {
			candidates = append(candidates, reasonCandidate{
				reason:     fmt.Sprintf("剧情内核高度相关，共同探讨了关于 %s 的深刻主题", strings.Join(keywords, "、")),
				reasonType: "semantic",
				score:      0.6 + (totalSimilarity * 0.5),
			})
		}
	}

	// 最终选择最高分理由
	best := reasonCandidate{reason: "基于内容相似度深度推荐", reasonType: "general", score: 0}
	for _, c := range candidates {
		if c.score > best.score {
			best = c
		}
	}

	return best.reason, best.reasonType, totalSimilarity
}

// GenerateRecommendationReason 保持兼容性的旧接口
func GenerateRecommendationReason(sourceMovie, targetMovie model.Movie) (string, string, float64) {
	src := extractMovieFeatures(sourceMovie)
	tgt := extractMovieFeatures(targetMovie)
	return GenerateRecommendationReasonV2(src, tgt, sourceMovie, targetMovie)
}

// contains 检查字符串是否在切片中
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// extractSemanticKeywords 从embedding内容中提取语义关键词
func extractSemanticKeywords(embeddingContent string) []string {
	// 这里简化处理，实际可以从embedding内容中提取关键词
	// 例如通过TF-IDF、关键词提取算法等方式

	// 简单的关键词提取：查找一些常见的高频词
	keywords := []string{}
	content := strings.ToLower(embeddingContent)

	// 预定义一些可能的关键词模式
	keywordPatterns := []string{
		"爱情", "友情", "亲情", "成长", "奋斗", "梦想", "现实",
		"社会", "人性", "道德", "正义", "邪恶", "战争", "和平",
		"科技", "未来", "过去", "历史", "文化", "传统", "现代",
		"悬疑", "推理", "犯罪", "心理", "惊悚", "恐怖", "神秘",
		"喜剧", "幽默", "讽刺", "搞笑", "欢乐", "感动", "温馨",
		"动作", "冒险", "刺激", "紧张", "激烈", "危险", "救援",
		"科幻", "奇幻", "魔法", "超能力", "时空", "宇宙", "外星",
		"家庭", "婚姻", "教育", "青春", "校园", "职场", "生活",
	}

	for _, pattern := range keywordPatterns {
		if strings.Contains(content, pattern) {
			keywords = append(keywords, pattern)
		}
	}

	// 如果找到太多关键词，只取前3个
	if len(keywords) > 3 {
		keywords = keywords[:3]
	}

	// 如果没有找到关键词，返回通用描述
	if len(keywords) == 0 {
		keywords = []string{"人性、情感"}
	}

	return keywords
}
