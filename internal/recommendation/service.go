// Package recommendation 是推荐：相似影片、为你推荐、经典重温。
//
// 它自己不建表，数据全部来自 catalog（media 表和 pgvector 向量列）、
// library（片单）和 history（观看记录）。
//
// 相似推荐分两层：
//
//	数据库向量检索给出候选，再在内存里用类型/导演/演员/评分/年代算一个可解释的理由。
package recommendation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
)

// SimilarMovie 是带推荐理由的相似影片。
type SimilarMovie struct {
	Movie      catalog.Movie
	Reason     string  `json:"reason"`
	ReasonType string  `json:"reason_type"`
	Similarity float64 `json:"similarity"`
}

// Store 是推荐需要的影片读接口，由 catalog 实现。
type Store interface {
	FindByDoubanID(ctx context.Context, doubanID string) (*catalog.Movie, error)
	FindByID(ctx context.Context, id int) (*catalog.Movie, error)
	FindSimilar(ctx context.Context, doubanID string, limit int) ([]catalog.Movie, error)
	Popular(ctx context.Context, limit int) ([]catalog.Movie, error)
}

// Personalizer 是个性化推荐接口，没有注入时相关板块返回空列表。
type Personalizer interface {
	UserRecommendations(ctx context.Context, userID, limit int) ([]catalog.Movie, error)
	ReliveClassics(ctx context.Context, userID, limit int) ([]catalog.Movie, error)
	RecentSimilar(ctx context.Context, userID, limit int) ([]catalog.Movie, string, error)
}

// Service 是推荐服务。
type Service struct {
	store        Store
	personalizer Personalizer
}

// ServiceOption 用于注入个性化推荐。
type ServiceOption func(*Service)

// WithPersonalizer 注入个性化推荐实现。
func WithPersonalizer(personalizer Personalizer) ServiceOption {
	return func(service *Service) { service.personalizer = personalizer }
}

// NewService 创建推荐服务。
func NewService(store Store, options ...ServiceOption) *Service {
	service := &Service{store: store}
	for _, option := range options {
		option(service)
	}
	return service
}

// FindSimilar 返回相似影片（走数据库向量检索）。
func (service *Service) FindSimilar(ctx context.Context, doubanID string, limit int) ([]catalog.Movie, error) {
	return service.store.FindSimilar(ctx, doubanID, limit)
}

// FindByID 按主键查影片。
func (service *Service) FindByID(ctx context.Context, id int) (*catalog.Movie, error) {
	return service.store.FindByID(ctx, id)
}

// UserRecommendations 返回个性化推荐。
func (service *Service) UserRecommendations(ctx context.Context, userID, limit int) ([]catalog.Movie, error) {
	if service.personalizer == nil {
		return []catalog.Movie{}, nil
	}
	return service.personalizer.UserRecommendations(ctx, userID, limit)
}

// ReliveClassics 返回值得重温的老片。
func (service *Service) ReliveClassics(ctx context.Context, userID, limit int) ([]catalog.Movie, error) {
	if service.personalizer == nil {
		return []catalog.Movie{}, nil
	}
	return service.personalizer.ReliveClassics(ctx, userID, limit)
}

// RecentSimilar 返回与最近看过那部相似的影片，第二个返回值是那部片子的名字。
func (service *Service) RecentSimilar(ctx context.Context, userID, limit int) ([]catalog.Movie, string, error) {
	if service.personalizer == nil {
		return []catalog.Movie{}, "", nil
	}
	return service.personalizer.RecentSimilar(ctx, userID, limit)
}

// Popular 返回热门影片，用于没有个人数据时兜底。
func (service *Service) Popular(ctx context.Context, limit int) ([]catalog.Movie, error) {
	return service.store.Popular(ctx, limit)
}

// FindSimilarWithReasons 在相似影片基础上补上推荐理由并按相似度重排。
func (service *Service) FindSimilarWithReasons(ctx context.Context, doubanID string, limit int) ([]SimilarMovie, *catalog.Movie, error) {
	source, err := service.store.FindByDoubanID(ctx, doubanID)
	if err != nil || source == nil {
		return nil, source, err
	}
	movies, err := service.store.FindSimilar(ctx, doubanID, limit)
	if err != nil {
		return nil, source, err
	}
	result := make([]SimilarMovie, 0, len(movies))
	for _, movie := range movies {
		reason, reasonType, similarity := GenerateReason(*source, movie)
		result = append(result, SimilarMovie{Movie: movie, Reason: reason, ReasonType: reasonType, Similarity: similarity})
	}
	return result, source, nil
}

// features 是算理由时用到的影片特征。
type features struct {
	genres, directors, actors map[string]bool
	year                      int
	rating                    float64
	title                     string
}

// extract 从影片里抽取特征。
func extract(movie catalog.Movie) features {
	return features{
		genres: peopleOrList(movie.Genres), directors: peopleOrList(movie.Directors), actors: peopleOrList(movie.Actors),
		year: parseYear(movie.Year), rating: movie.Rating, title: movie.Title,
	}
}

// GenerateReason 生成推荐理由和相似度。
// 相似度权重：类型 0.40、导演 0.25、演员 0.20、评分 0.10、年代 0.05。
// 理由从多个候选里挑分数最高的一条，系列续作优先级最高，都不满足时用通用话术兜底。
func GenerateReason(source, target catalog.Movie) (string, string, float64) {
	src, dst := extract(source), extract(target)
	genreScore, commonGenres := overlap(src.genres, dst.genres)
	directorScore, commonDirectors := overlap(src.directors, dst.directors)
	actorScore, commonActors := overlap(src.actors, dst.actors)
	ratingScore := ratingSimilarity(src.rating, dst.rating)
	eraScore := eraSimilarity(src.year, dst.year)
	similarity := genreScore*0.4 + directorScore*0.25 + actorScore*0.2 + ratingScore*0.1 + eraScore*0.05

	type candidate struct {
		text, kind string
		score      float64
	}
	candidates := []candidate{}
	if src.title != "" && dst.title != "" && (strings.HasPrefix(dst.title, src.title) || strings.HasPrefix(src.title, dst.title)) {
		candidates = append(candidates, candidate{"该系列作品的延续，带你深入了解其光影宇宙", "series", 1.5})
	}
	if directorScore > 0.4 && len(commonDirectors) > 0 {
		candidates = append(candidates, candidate{fmt.Sprintf("由同位导演 %s 执导，叙事风格与艺术造诣一脉相承", commonDirectors[0]), "director", 0.9 + directorScore})
	}
	if actorScore > 0.2 && len(commonActors) > 0 {
		candidates = append(candidates, candidate{fmt.Sprintf("同样由 %s 主演，演技表现与角色气质依然出众", commonActors[0]), "actor", 0.8 + actorScore})
	}
	if len(commonGenres) > 0 {
		candidates = append(candidates, candidate{fmt.Sprintf("同属优质%s片，风格与本作高度契合", strings.Join(commonGenres, "、")), "genre", 0.7 + genreScore})
	}
	if source.Rating > 8.5 && target.Rating > 8.5 && genreScore > 0.3 {
		candidates = append(candidates, candidate{"两部作品均为 8.5+ 的顶级神作，艺术水准极高", "masterpiece", 1.2})
	}
	if target.EmbeddingContent != "" {
		candidates = append(candidates, candidate{fmt.Sprintf("剧情内核高度相关，共同探讨了关于 %s 的深刻主题", strings.Join(semanticKeywords(target.EmbeddingContent), "、")), "semantic", 0.6 + similarity*0.5})
	}
	best := candidate{"基于内容相似度深度推荐", "general", 0}
	for _, item := range candidates {
		if item.score > best.score {
			best = item
		}
	}
	return best.text, best.kind, similarity
}

// peopleOrList 把逗号分隔的人名或类型拆成集合。
func peopleOrList(value string) map[string]bool {
	result := make(map[string]bool)
	if strings.HasPrefix(strings.TrimSpace(value), "[") {
		var people []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(value), &people) == nil {
			for _, person := range people {
				if name := strings.TrimSpace(person.Name); name != "" {
					result[name] = true
				}
			}
			return result
		}
	}
	value = strings.NewReplacer("，", ",", "/", ",").Replace(value)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result[item] = true
		}
	}
	return result
}

// overlap 计算两个集合的重合度，同时返回重合的元素。
func overlap(left, right map[string]bool) (float64, []string) {
	common := make([]string, 0)
	for item := range right {
		if left[item] {
			common = append(common, item)
		}
	}
	sort.Strings(common)
	maximum := math.Max(float64(len(left)), float64(len(right)))
	if maximum == 0 {
		return 0, common
	}
	return float64(len(common)) / maximum, common
}

// parseYear 解析年份，解析不出来算 0。
func parseYear(value string) int { year, _ := strconv.Atoi(value); return year }

// ratingSimilarity 评分越接近得分越高。
func ratingSimilarity(left, right float64) float64 {
	if left <= 0 || right <= 0 {
		return 0.5
	}
	difference := math.Abs(left - right)
	if difference <= 1 {
		return 1
	}
	if difference > 2.5 {
		return 0.3
	}
	return 1 - (difference-1)/1.5*0.7
}

// eraSimilarity 年代越接近得分越高。
func eraSimilarity(left, right int) float64 {
	if left == 0 || right == 0 {
		return 0.5
	}
	difference := math.Abs(float64(left - right))
	switch {
	case difference <= 1:
		return 1
	case difference <= 3:
		return 0.8
	case difference <= 5:
		return 0.6
	case difference <= 10:
		return 0.4
	default:
		return 0.2
	}
}

// semanticKeywords 从简介里挑几个关键词拼进推荐理由。
func semanticKeywords(value string) []string {
	patterns := []string{"爱情", "友情", "亲情", "成长", "奋斗", "梦想", "现实", "社会", "人性", "犯罪", "悬疑", "科幻", "家庭", "战争"}
	result := make([]string, 0, 3)
	for _, pattern := range patterns {
		if strings.Contains(value, pattern) {
			result = append(result, pattern)
			if len(result) == 3 {
				break
			}
		}
	}
	if len(result) == 0 {
		return []string{"人性、情感"}
	}
	return result
}
