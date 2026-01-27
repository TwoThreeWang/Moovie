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

// FindSimilarWithReasons 根据向量相似度查找相似电影并生成推荐理由
func (s *RecommendationService) FindSimilarWithReasons(doubanID string, limit int) ([]SimilarMovieWithReason, *model.Movie, error) {
	// 1. 先获取源电影信息
	sourceMovie, err := s.movieRepo.FindByDoubanID(doubanID)
	if err != nil {
		return nil, nil, err
	}

	// 2. 获取相似电影 (获取多一些，方便筛选)
	similarMovies, err := s.movieRepo.FindSimilar(doubanID, limit)
	if err != nil {
		return nil, nil, err
	}

	// 3. 为每部电影生成推荐理由
	result := make([]SimilarMovieWithReason, 0, limit)
	for _, movie := range similarMovies {
		reason, reasonType, score := GenerateRecommendationReason(*sourceMovie, movie)

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

// calculateGenreSimilarity 计算类型重合度
func calculateGenreSimilarity(sourceGenres, targetGenres string) (float64, []string) {
	sourceList := parseGenres(sourceGenres)
	targetList := parseGenres(targetGenres)

	commonGenres := []string{}
	for _, source := range sourceList {
		for _, target := range targetList {
			if source == target && source != "" {
				commonGenres = append(commonGenres, source)
			}
		}
	}

	maxLen := math.Max(float64(len(sourceList)), float64(len(targetList)))
	if maxLen == 0 {
		return 0, commonGenres
	}

	similarity := float64(len(commonGenres)) / maxLen
	return similarity, commonGenres
}

// parsePersonNames 从人员JSON字符串中提取人名列表
func parsePersonNames(personsJSON string) []string {
	// 首先尝试解析为JSON数组
	var persons []model.Person
	if err := json.Unmarshal([]byte(personsJSON), &persons); err == nil {
		// 成功解析为JSON，提取人名
		names := make([]string, 0, len(persons))
		for _, person := range persons {
			if person.Name != "" {
				names = append(names, strings.TrimSpace(person.Name))
			}
		}
		return names
	}

	// 如果不是JSON格式，按原来的逗号分割方式处理
	if personsJSON == "" {
		return []string{}
	}

	names := strings.Split(personsJSON, ",")
	result := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// calculatePersonSimilarity 计算人员重合度（导演、演员）
func calculatePersonSimilarity(sourcePersons, targetPersons string) (float64, []string) {
	sourceList := parsePersonNames(sourcePersons)
	targetList := parsePersonNames(targetPersons)

	commonPersons := []string{}
	for _, source := range sourceList {
		for _, target := range targetList {
			if source == target && source != "" {
				commonPersons = append(commonPersons, source)
			}
		}
	}

	maxLen := math.Max(float64(len(sourceList)), float64(len(targetList)))
	if maxLen == 0 {
		return 0, commonPersons
	}

	similarity := float64(len(commonPersons)) / maxLen
	return similarity, commonPersons
}

// calculateRatingSimilarity 计算评分相似度
func calculateRatingSimilarity(sourceRating, targetRating float64) float64 {
	ratingDiff := math.Abs(sourceRating - targetRating)
	// 将评分差异转换为相似度（差异越小，相似度越高）
	similarity := 1 - (ratingDiff / 10.0)
	return math.Max(0, similarity)
}

// calculateEraSimilarity 计算年代相似度
func calculateEraSimilarity(sourceYear, targetYear string) float64 {
	// 将年份字符串转换为整数
	sourceYearInt := 0
	targetYearInt := 0

	if sourceYear != "" {
		fmt.Sscanf(sourceYear, "%d", &sourceYearInt)
	}
	if targetYear != "" {
		fmt.Sscanf(targetYear, "%d", &targetYearInt)
	}

	if sourceYearInt == 0 || targetYearInt == 0 {
		return 0.5 // 如果年份无效，返回中等相似度
	}

	yearDiff := math.Abs(float64(sourceYearInt - targetYearInt))

	// 根据年份差异计算相似度
	if yearDiff <= 1 {
		return 1.0 // 同一年或相邻年份
	} else if yearDiff <= 3 {
		return 0.8 // 3年内
	} else if yearDiff <= 5 {
		return 0.6 // 5年内
	} else if yearDiff <= 10 {
		return 0.4 // 10年内
	} else {
		return 0.2 // 超过10年
	}
}

// GenerateRecommendationReason 生成推荐理由（基于优先级算法）
func GenerateRecommendationReason(sourceMovie, targetMovie model.Movie) (string, string, float64) {
	// 计算各个维度的相似度和重合信息
	genreSimilarity, commonGenres := calculateGenreSimilarity(sourceMovie.Genres, targetMovie.Genres)
	directorSimilarity, commonDirectors := calculatePersonSimilarity(sourceMovie.Directors, targetMovie.Directors)
	actorSimilarity, commonActors := calculatePersonSimilarity(sourceMovie.Actors, targetMovie.Actors)
	ratingSimilarity := calculateRatingSimilarity(sourceMovie.Rating, targetMovie.Rating)
	eraSimilarity := calculateEraSimilarity(sourceMovie.Year, targetMovie.Year)

	// 定义权重（用于综合相似度计算）
	weights := map[string]float64{
		"genre":    0.4,
		"director": 0.25,
		"actor":    0.2,
		"rating":   0.1,
		"era":      0.05,
	}

	// 计算综合相似度
	totalSimilarity := genreSimilarity*weights["genre"] +
		directorSimilarity*weights["director"] +
		actorSimilarity*weights["actor"] +
		ratingSimilarity*weights["rating"] +
		eraSimilarity*weights["era"]

	// 优先级算法：按优先级顺序检查各个维度
	var reason string
	var reasonType string

	// 1. 最高优先级：同导演
	if directorSimilarity > 0.5 && len(commonDirectors) > 0 {
		directorName := commonDirectors[0]
		if len(commonDirectors) > 1 {
			directorName = strings.Join(commonDirectors, "、")
		}
		reason = fmt.Sprintf("由同位导演 %s 执导，叙事风格一脉相承", directorName)
		reasonType = "director"
		return reason, reasonType, totalSimilarity
	}

	// 2. 第二优先级：同主演
	if actorSimilarity > 0.3 && len(commonActors) > 0 {
		actorName := commonActors[0]
		if len(commonActors) > 1 {
			actorName = strings.Join(commonActors[:1], "、")
		}
		reason = fmt.Sprintf("同样由 %s 领衔主演，演技表现同样出色", actorName)
		reasonType = "actor"
		return reason, reasonType, totalSimilarity
	}

	// 3. 第三优先级：核心类型重合（科幻、悬疑等核心类型）
	coreGenres := []string{"科幻", "悬疑", "惊悚", "动作", "喜剧", "爱情", "剧情", "战争", "历史"}
	coreCommonGenres := []string{}
	for _, genre := range commonGenres {
		for _, coreGenre := range coreGenres {
			if genre == coreGenre {
				coreCommonGenres = append(coreCommonGenres, genre)
			}
		}
	}

	if len(coreCommonGenres) > 0 {
		genreDesc := ""
		if len(coreCommonGenres) == 1 {
			genreDesc = coreCommonGenres[0]
		} else {
			genreDesc = strings.Join(coreCommonGenres, "、")
		}

		// 根据类型选择不同的描述
		if contains(coreCommonGenres, "科幻") || contains(coreCommonGenres, "悬疑") || contains(coreCommonGenres, "惊悚") {
			reason = fmt.Sprintf("同属优质%s片，带给你类似的烧脑/震撼体验", genreDesc)
		} else if contains(coreCommonGenres, "动作") || contains(coreCommonGenres, "战争") {
			reason = fmt.Sprintf("同属优质%s片，带给你类似的刺激体验", genreDesc)
		} else if contains(coreCommonGenres, "喜剧") || contains(coreCommonGenres, "爱情") {
			reason = fmt.Sprintf("同属优质%s片，带给你类似的情感体验", genreDesc)
		} else {
			reason = fmt.Sprintf("同属优质%s片，风格相似", genreDesc)
		}
		reasonType = "genre"
		return reason, reasonType, totalSimilarity
	}

	// 4. 第四优先级：年代/评分接近
	if eraSimilarity > 0.6 && ratingSimilarity > 0.7 {
		yearRange := fmt.Sprintf("%s年左右", sourceMovie.Year)
		if sourceMovie.Year != targetMovie.Year {
			yearRange = fmt.Sprintf("%s-%s年", sourceMovie.Year, targetMovie.Year)
		}
		reason = fmt.Sprintf("同为 %s 的经典高分佳作（评分：%.1f vs %.1f）", yearRange, sourceMovie.Rating, targetMovie.Rating)
		reasonType = "era_rating"
		return reason, reasonType, totalSimilarity
	}

	// 5. 第五优先级：仅年代接近
	if eraSimilarity > 0.6 {
		reason = fmt.Sprintf("同为 %s 年左右的经典佳作", sourceMovie.Year)
		reasonType = "era"
		return reason, reasonType, totalSimilarity
	}

	// 6. 第六优先级：仅评分接近
	if ratingSimilarity > 0.8 {
		reason = fmt.Sprintf("同为高分佳作（评分：%.1f vs %.1f）", sourceMovie.Rating, targetMovie.Rating)
		reasonType = "rating"
		return reason, reasonType, totalSimilarity
	}

	// 7. 兜底：语义相似（基于EmbeddingContent）
	if targetMovie.EmbeddingContent != "" {
		// 提取语义关键词（这里简化处理，实际可以从embedding内容中提取关键词）
		keywords := extractSemanticKeywords(targetMovie.EmbeddingContent)
		if len(keywords) > 0 {
			reason = fmt.Sprintf("剧情内核与本作高度相关，探讨了类似的%s主题", strings.Join(keywords, "、"))
			reasonType = "semantic"
			return reason, reasonType, totalSimilarity
		}
	}

	// 最终兜底
	reason = "基于内容相似度推荐"
	reasonType = "general"
	return reason, reasonType, totalSimilarity
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
