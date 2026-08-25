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
	WatchedCount   int
	AvgRating      float64
	GenreStats     []GenreStat
	TopMovie       *TopMovie
	ContinuousDays int
	PersonaTitle   string
	PersonaLine    string
	FeaturedQuote  string
	PosterWall     []PosterWallItem
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
	data.ContinuousDays = service.continuousDays(ctx, userID, end)
	data.PersonaTitle, data.PersonaLine = buildPersona(data, watched)
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
			for _, genre := range splitGenres(movie.Genres) {
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
	sort.SliceStable(data.GenreStats, func(i, j int) bool { return data.GenreStats[i].Count > data.GenreStats[j].Count })
	return data
}

// continuousDays 算最长连续观影天数。
func (service *Service) continuousDays(ctx context.Context, userID int, end time.Time) int {
	days := 0
	current := end.AddDate(0, 0, -1)
	for {
		startOfDay := time.Date(current.Year(), current.Month(), current.Day(), 0, 0, 0, 0, time.Local)
		count, err := service.library.CountByUserAndDateRange(ctx, userID, library.StatusWatched, startOfDay, startOfDay.AddDate(0, 0, 1))
		if err != nil || count == 0 {
			break
		}
		days++
		current = current.AddDate(0, 0, -1)
		if days > 30 {
			break
		}
	}
	return days
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
	{[2]string{"悬疑", "犯罪"}, "深夜悬疑侦探"},
	{[2]string{"悬疑", "惊悚"}, "心跳过山车玩家"},
	{[2]string{"恐怖", "惊悚"}, "胆量测试员"},
	{[2]string{"爱情", "剧情"}, "治愈系情感玩家"},
	{[2]string{"喜剧", "爱情"}, "快乐制造机"},
	{[2]string{"动作", "科幻"}, "刺激感猎人"},
	{[2]string{"战争", "历史"}, "时代旁观者"},
	{[2]string{"纪录片", "传记"}, "现实观察员"},
	{[2]string{"动画", "奇幻"}, "造梦者"},
	{[2]string{"音乐", "歌舞"}, "浪漫主义者"},
}

// personaSingles 是单一类型对应的人格标签表。
var personaSingles = map[string]string{
	"剧情": "文艺片鉴赏家", "喜剧": "快乐制造机", "动作": "肾上腺素依赖者", "爱情": "感情充沛玩家",
	"科幻": "未来主义者", "动画": "造梦者", "悬疑": "推理爱好者", "惊悚": "刺激感猎人", "犯罪": "案件旁观者",
	"恐怖": "胆量测试员", "纪录片": "现实观察员", "历史": "时代旁观者", "战争": "时代旁观者", "音乐": "浪漫主义者",
	"歌舞": "浪漫主义者", "奇幻": "造梦者", "冒险": "造梦者", "传记": "现实观察员",
}

// personaTitle 按类型分布挑一个人格标签，先看组合再看单一类型。
func personaTitle(stats []GenreStat) string {
	if len(stats) == 0 {
		return "全能观众"
	}
	first, second := stats[0].Genre, ""
	if len(stats) > 1 {
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
	return "全能观众"
}

// buildPersona 生成人格标题和描述句，深夜观影多的会额外提一句。
func buildPersona(data reportData, watched []library.Record) (string, string) {
	title := personaTitle(data.GenreStats)
	nightCount, latestHour, hasNight := nightOwlStats(watched)
	nightRatio := 0
	if data.WatchedCount > 0 {
		nightRatio = nightCount * 100 / data.WatchedCount
	}
	switch {
	case data.ContinuousDays >= 5 && hasNight:
		return title, fmt.Sprintf("连续 %d 天没有一天缺席，最晚一次是%s。", data.ContinuousDays, formatHourZH(latestHour))
	case data.ContinuousDays >= 5:
		return title, fmt.Sprintf("连续 %d 天没有一天缺席，看片比吃饭还准时。", data.ContinuousDays)
	case nightRatio >= 30 && hasNight:
		return title, fmt.Sprintf("有 %d%% 的观影发生在深夜，最晚一次是%s。", nightRatio, formatHourZH(latestHour))
	case data.WatchedCount >= 20:
		return title, fmt.Sprintf("这个月看得很勤，一共刷完了 %d 部。", data.WatchedCount)
	case data.TopMovie != nil:
		return title, fmt.Sprintf("这个月最让你惊喜的是《%s》。", data.TopMovie.Title)
	default:
		return title, fmt.Sprintf("这个月认真看完了 %d 部，每一部都算数。", data.WatchedCount)
	}
}

// nightOwlStats 统计深夜观影次数和最晚的时间点。
func nightOwlStats(watched []library.Record) (count, latestHour int, has bool) {
	bestScore := -1
	for _, record := range watched {
		hour := record.CreatedAt.Hour()
		if hour >= 23 || hour < 5 {
			count++
			score := hour
			if hour < 5 {
				score += 24
			}
			if score > bestScore {
				bestScore, latestHour, has = score, hour, true
			}
		}
	}
	return count, latestHour, has
}

// formatHourZH 把小时数写成中文时间说法。
func formatHourZH(hour int) string {
	switch {
	case hour < 6:
		return fmt.Sprintf("凌晨 %d 点", hour)
	case hour < 12:
		return fmt.Sprintf("上午 %d 点", hour)
	case hour == 12:
		return "中午 12 点"
	case hour < 18:
		return fmt.Sprintf("下午 %d 点", hour-12)
	default:
		return fmt.Sprintf("晚上 %d 点", hour-12)
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
	current := ""
	for _, character := range genres {
		if character == '/' || character == ',' || character == '、' {
			if current != "" {
				result, current = append(result, current), ""
			}
		} else {
			current += string(character)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
