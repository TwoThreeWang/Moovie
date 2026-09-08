package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

// posterWallSize 是海报墙的影片数量。
const posterWallSize = 4

// Service 负责生成月报。
type Service struct {
	store   Store
	library library.Store
	catalog catalog.Store
	now     func() time.Time
}

// NewService 创建月报服务。
func NewService(store Store, libraryStore library.Store, catalogStore catalog.Store) *Service {
	return &Service{store: store, library: libraryStore, catalog: catalogStore, now: time.Now}
}

// reportData 是算好但还没落库的报告内容。
type reportData struct {
	WatchedCount    int
	GenreKnownCount int
	AvgRating       float64
	GenreStats      []GenreStat
	TopMovie        *TopMovie
	ContinuousDays  int
	PersonaTitle    string
	PersonaLine     string
	FeaturedQuote   string
	PosterWall      []PosterWallItem
}

// Generate 生成某个用户某个月的报告，本月没有观影记录时标记为失败。
// allCounts 是所有用户当月的观影数量，用来算排名百分位。
func (service *Service) Generate(ctx context.Context, userID int, yearMonth string, allCounts map[int]int) error {
	start, end, err := monthRange(yearMonth)
	if err != nil {
		return fmt.Errorf("解析月份失败: %w", err)
	}
	report, err := service.store.GetByUserAndMonth(ctx, userID, yearMonth)
	if err != nil {
		return fmt.Errorf("查询报告失败: %w", err)
	}
	if report == nil {
		report, err = service.store.Save(ctx, MonthlyReport{UserID: userID, YearMonth: yearMonth, Status: StatusPending})
		if err != nil {
			return fmt.Errorf("创建报告记录失败: %w", err)
		}
	}
	if err := service.store.UpdateStatus(ctx, report.ID, StatusGenerating, ""); err != nil {
		return err
	}
	watched, err := service.library.ListByUserAndDateRange(ctx, userID, library.StatusWatched, start, end)
	if err != nil {
		_ = service.store.UpdateStatus(ctx, report.ID, StatusFailed, "查询观影记录失败")
		return fmt.Errorf("查询观影记录失败: %w", err)
	}
	if len(watched) == 0 {
		_ = service.store.UpdateStatus(ctx, report.ID, StatusFailed, "本月无观影记录")
		return fmt.Errorf("用户 %d 本月无观影记录", userID)
	}
	data := service.calculateStats(ctx, watched)
	data.ContinuousDays = continuousDays(watched)
	data.PersonaTitle, data.PersonaLine = buildPersona(data)
	data.FeaturedQuote = featuredQuote(data.TopMovie, watched)
	data.PosterWall = samplePosterWall(data.TopMovie, watched)

	genreJSON, _ := json.Marshal(data.GenreStats)
	posterJSON, _ := json.Marshal(data.PosterWall)
	report.WatchedCount = data.WatchedCount
	report.TotalDurationMinutes = 0
	report.AvgRating = math.Round(data.AvgRating*10) / 10
	report.GenreStats = string(genreJSON)
	report.ContinuousDays = data.ContinuousDays
	report.PersonaTitle, report.PersonaLine = data.PersonaTitle, data.PersonaLine
	report.PercentileRank = percentile(userID, data.WatchedCount, allCounts)
	report.FeaturedQuote, report.PosterWall = data.FeaturedQuote, string(posterJSON)
	report.TopMovieID, report.TopMovieTitle, report.TopMoviePoster, report.TopMovieRating = "", "", "", 0
	if data.TopMovie != nil {
		report.TopMovieID, report.TopMovieTitle = data.TopMovie.DoubanID, data.TopMovie.Title
		report.TopMoviePoster, report.TopMovieRating = data.TopMovie.Poster, data.TopMovie.Rating
	}
	report.Status, report.ErrorMessage = StatusGenerated, ""
	if _, err := service.store.Save(ctx, *report); err != nil {
		_ = service.store.UpdateStatus(ctx, report.ID, StatusFailed, "更新报告失败")
		return fmt.Errorf("更新报告失败: %w", err)
	}
	return nil
}

// GeneratePreviousMonth 给所有有记录的用户批量生成上个月的报告，由每日任务触发。
func (service *Service) GeneratePreviousMonth(ctx context.Context) error {
	now := service.now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local).AddDate(0, -1, 0)
	end := start.AddDate(0, 1, 0)
	counts, err := service.library.CountWatchedByAllUsersInRange(ctx, start, end)
	if err != nil {
		return err
	}
	var generationError error
	for userID := range counts {
		if err := service.Generate(ctx, userID, start.Format("2006-01"), counts); err != nil {
			generationError = errors.Join(generationError, err)
		}
	}
	return generationError
}

// monthRange 把 2026-01 这样的月份转成起止时间。
func monthRange(yearMonth string) (time.Time, time.Time, error) {
	parsed, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start := time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, time.Local)
	return start, start.AddDate(0, 1, 0), nil
}

// calculateStats 统计数量、平均分、类型分布、最爱影片、人格标签、金句和海报墙。
func (service *Service) calculateStats(ctx context.Context, watched []library.Record) reportData {
	data := reportData{WatchedCount: len(watched)}
	genreCounts := make(map[string]int)
	ratingSum, ratingCount, topRating := 0, 0, 0
	for _, record := range watched {
		if record.Rating > 0 {
			ratingSum, ratingCount = ratingSum+record.Rating, ratingCount+1
			if record.Rating > topRating {
				topRating = record.Rating
				data.TopMovie = &TopMovie{DoubanID: record.MovieID, Title: record.Title, Poster: record.Poster, Rating: record.Rating}
			}
		}
		movie, _ := service.catalog.FindByDoubanID(ctx, record.MovieID)
		if movie != nil {
			genres := splitGenres(movie.Genres)
			if len(genres) > 0 {
				data.GenreKnownCount++
			}
			for _, genre := range genres {
				genreCounts[genre]++
			}
		}
	}
	if ratingCount > 0 {
		data.AvgRating = float64(ratingSum) / float64(ratingCount)
	}
	totalGenres := 0
	for _, count := range genreCounts {
		totalGenres += count
	}
	for genre, count := range genreCounts {
		pct := 0
		if totalGenres > 0 {
			pct = count * 100 / totalGenres
		}
		data.GenreStats = append(data.GenreStats, GenreStat{Genre: genre, Count: count, Pct: pct})
	}
	sort.Slice(data.GenreStats, func(i, j int) bool {
		if data.GenreStats[i].Count == data.GenreStats[j].Count {
			return data.GenreStats[i].Genre < data.GenreStats[j].Genre
		}
		return data.GenreStats[i].Count > data.GenreStats[j].Count
	})
	return data
}

// continuousDays 计算传入月度片单中最长连续记录天数，不推断实际观看日期。
func continuousDays(watched []library.Record) int {
	days := make(map[string]bool)
	for _, record := range watched {
		days[record.CreatedAt.In(time.Local).Format("2006-01-02")] = true
	}
	longest := 0
	for day := range days {
		date, _ := time.ParseInLocation("2006-01-02", day, time.Local)
		if days[date.AddDate(0, 0, -1).Format("2006-01-02")] {
			continue
		}
		streak := 0
		for days[date.Format("2006-01-02")] {
			streak++
			date = date.AddDate(0, 0, 1)
		}
		if streak > longest {
			longest = streak
		}
	}
	return longest
}

// percentile 算用户在所有人中的排名百分位。
func percentile(userID, myCount int, allCounts map[int]int) int {
	if len(allCounts) < 5 {
		return 0
	}
	below := 0
	for id, count := range allCounts {
		if id != userID && count < myCount {
			below++
		}
	}
	value := int(math.Round(float64(below) / float64(len(allCounts)-1) * 100))
	if value >= 100 {
		return 99
	}
	if value < 1 {
		return 1
	}
	return value
}

// personaCombo 是「两个类型组合」对应的人格标签。
type personaCombo struct {
	genres [2]string
	title  string
}

// personaCombos 是组合型人格标签表。
var personaCombos = []personaCombo{
	{[2]string{"悬疑", "犯罪"}, "悬疑侦探"},
	{[2]string{"悬疑", "惊悚"}, "心跳过山车玩家"},
	{[2]string{"恐怖", "惊悚"}, "胆量测试员"},
	{[2]string{"爱情", "剧情"}, "情感故事爱好者"},
	{[2]string{"喜剧", "爱情"}, "快乐制造机"},
	{[2]string{"动作", "科幻"}, "刺激感猎人"},
	{[2]string{"战争", "历史"}, "时代旁观者"},
	{[2]string{"纪录片", "传记"}, "现实观察员"},
	{[2]string{"动画", "奇幻"}, "造梦者"},
	{[2]string{"音乐", "歌舞"}, "浪漫主义者"},
}

// personaSingles 是单一类型对应的人格标签表。
var personaSingles = map[string]string{
	"剧情": "故事爱好者", "喜剧": "快乐制造机", "动作": "肾上腺素依赖者", "爱情": "感情充沛玩家",
	"科幻": "未来主义者", "动画": "造梦者", "悬疑": "推理爱好者", "惊悚": "刺激感猎人", "犯罪": "案件旁观者",
	"恐怖": "胆量测试员", "纪录片": "现实观察员", "历史": "时代旁观者", "战争": "时代旁观者", "音乐": "浪漫主义者",
	"歌舞": "浪漫主义者", "奇幻": "造梦者", "冒险": "造梦者", "传记": "现实观察员",
}

// personaTitle 按类型分布挑一个人格标签，先看组合再看单一类型。
func personaTitle(stats []GenreStat, watchedCount int) string {
	if watchedCount < 3 || len(stats) == 0 {
		return "观影小记"
	}
	// ponytail: 初始启发式阈值，积累真实月报样本后再校准；次数是含该类型的影片数。
	if stats[0].Count*2 < watchedCount {
		return "多元探索者"
	}
	first, second := stats[0].Genre, ""
	if len(stats) > 1 && stats[1].Count >= 2 && stats[1].Count*2 >= stats[0].Count {
		second = stats[1].Genre
	}
	for _, combo := range personaCombos {
		if (combo.genres[0] == first && combo.genres[1] == second) || (combo.genres[0] == second && combo.genres[1] == first) {
			return combo.title
		}
	}
	if title := personaSingles[first]; title != "" {
		return title
	}
	return "观影小记"
}

// buildPersona 只描述片单记录和个人评分，不从录入时间推断观影习惯。
func buildPersona(data reportData) (string, string) {
	title := personaTitle(data.GenreStats, data.WatchedCount)
	switch {
	case data.WatchedCount < 3:
		return title, fmt.Sprintf("本月片单有 %d 部看过的作品，记录还少，暂不判断类型偏好。", data.WatchedCount)
	case data.GenreKnownCount < 3 || data.GenreKnownCount*2 < data.WatchedCount:
		return "观影小记", "影片类型资料不足，暂不判断类型偏好。"
	case data.ContinuousDays >= 5:
		return title, fmt.Sprintf("本月最长连续 %d 天都有片单记录。", data.ContinuousDays)
	case data.TopMovie != nil:
		return title, fmt.Sprintf("本月片单中，你评分最高的作品之一是《%s》。", data.TopMovie.Title)
	default:
		return title, fmt.Sprintf("本月片单收录了 %d 部看过的作品。", data.WatchedCount)
	}
}

// featuredQuote 从本月短评里挑一条当金句。
func featuredQuote(top *TopMovie, watched []library.Record) string {
	if top != nil {
		for _, record := range watched {
			if record.MovieID == top.DoubanID && strings.TrimSpace(record.Comment) != "" {
				return strings.TrimSpace(record.Comment)
			}
		}
	}
	longest := ""
	for _, record := range watched {
		if comment := strings.TrimSpace(record.Comment); len([]rune(comment)) > len([]rune(longest)) {
			longest = comment
		}
	}
	return longest
}

// samplePosterWall 挑几部片子组成海报墙，最爱的那部排第一。
func samplePosterWall(top *TopMovie, watched []library.Record) []PosterWallItem {
	candidates := make([]library.Record, 0, len(watched))
	for _, record := range watched {
		if record.Poster != "" {
			candidates = append(candidates, record)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CreatedAt.Before(candidates[j].CreatedAt) })
	pool := candidates
	if top != nil && len(watched) >= posterWallSize+1 {
		filtered := make([]library.Record, 0, len(candidates))
		for _, record := range candidates {
			if record.MovieID != top.DoubanID {
				filtered = append(filtered, record)
			}
		}
		if len(filtered) > 0 {
			pool = filtered
		}
	}
	if len(pool) <= posterWallSize {
		return posterItems(pool)
	}
	result := make([]PosterWallItem, 0, posterWallSize)
	bucketSize := float64(len(pool)) / posterWallSize
	for index := 0; index < posterWallSize; index++ {
		candidate := pool[int(float64(index)*bucketSize)]
		result = append(result, PosterWallItem{MovieID: candidate.MovieID, Title: candidate.Title, Poster: candidate.Poster})
	}
	return result
}

// posterItems 把记录转成海报墙条目。
func posterItems(records []library.Record) []PosterWallItem {
	items := make([]PosterWallItem, 0, len(records))
	for _, record := range records {
		items = append(items, PosterWallItem{MovieID: record.MovieID, Title: record.Title, Poster: record.Poster})
	}
	return items
}

// splitGenres 拆分类型字符串。
func splitGenres(genres string) []string {
	result := make([]string, 0)
	seen := make(map[string]bool)
	for _, genre := range strings.FieldsFunc(genres, func(r rune) bool { return r == '/' || r == ',' || r == '、' }) {
		genre = strings.TrimSpace(genre)
		if genre != "" && !seen[genre] {
			seen[genre] = true
			result = append(result, genre)
		}
	}
	return result
}
