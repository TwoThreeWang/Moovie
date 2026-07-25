package service

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
)

// MonthlyReportService 月度报告服务
type MonthlyReportService struct {
	repos *repository.Repositories
}

// NewMonthlyReportService 创建月度报告服务
func NewMonthlyReportService(repos *repository.Repositories) *MonthlyReportService {
	return &MonthlyReportService{repos: repos}
}

// GenreStat 类型统计
type GenreStat struct {
	Genre string `json:"genre"`
	Count int    `json:"count"`
	Pct   int    `json:"pct"`
}

// posterWallSize 分享卡片海报墙固定展示的海报数量（不随观影数量变化，保证卡片形状统一）
const posterWallSize = 4

// MonthlyReportData 月度报告数据（生成过程中的中间结果）
type MonthlyReportData struct {
	WatchedCount         int
	TotalDurationMinutes int
	AvgRating            float64
	GenreStats           []GenreStat
	TopMovie             *TopMovieData
	ContinuousDays       int
	PersonaTitle         string
	PersonaLine          string
	FeaturedQuote        string
	PosterWall           []model.PosterWallItem
}

// TopMovieData 本月最佳电影
type TopMovieData struct {
	DoubanID string `json:"douban_id"`
	Title    string `json:"title"`
	Poster   string `json:"poster"`
	Rating   int    `json:"rating"`
}

// GenerateReport 为指定用户生成指定月份的报告
// allCounts 是这个月全站活跃用户的观影数分布（user_id -> 观影数），用于计算百分位；
// 传 nil 时不计算百分位（比如单独给某个用户手动重新生成时）
func (s *MonthlyReportService) GenerateReport(userID int, yearMonth string, allCounts map[int]int) error {
	// 解析月份范围
	startTime, endTime, err := s.parseMonthRange(yearMonth)
	if err != nil {
		return fmt.Errorf("解析月份失败: %w", err)
	}

	// 查询或创建报告记录
	report, _ := s.repos.MonthlyReport.GetByUserAndMonth(userID, yearMonth)
	if report == nil {
		report = &model.MonthlyReport{
			UserID:    userID,
			YearMonth: yearMonth,
			Status:    model.MonthlyReportStatusPending,
		}
		if err := s.repos.MonthlyReport.Upsert(report); err != nil {
			return fmt.Errorf("创建报告记录失败: %w", err)
		}
	}

	// 直接在 SQL 层按月份过滤，不再拉全量记录到内存里比对日期
	monthWatched, err := s.repos.UserMovie.ListByUserAndDateRange(userID, "watched", startTime, endTime)
	if err != nil {
		s.repos.MonthlyReport.UpdateStatus(report.ID, model.MonthlyReportStatusFailed, "查询观影记录失败")
		return fmt.Errorf("查询观影记录失败: %w", err)
	}

	if len(monthWatched) == 0 {
		s.repos.MonthlyReport.UpdateStatus(report.ID, model.MonthlyReportStatusFailed, "本月无观影记录")
		return fmt.Errorf("用户 %d 本月无观影记录", userID)
	}

	// 计算统计数据
	data := s.calculateStats(monthWatched)

	// 计算连续观影天数
	data.ContinuousDays = s.calculateContinuousDays(userID, endTime)

	// 生成人设标题 + 判词
	data.PersonaTitle, data.PersonaLine = s.buildPersona(data, monthWatched)

	// 精选短评摘录
	data.FeaturedQuote = s.pickFeaturedQuote(data.TopMovie, monthWatched)

	// 海报墙抽样
	data.PosterWall = s.samplePosterWall(data.TopMovie, monthWatched)

	// 更新报告
	report.WatchedCount = data.WatchedCount
	report.TotalDurationMinutes = data.TotalDurationMinutes
	report.AvgRating = math.Round(data.AvgRating*10) / 10
	report.ContinuousDays = data.ContinuousDays
	report.PersonaTitle = data.PersonaTitle
	report.PersonaLine = data.PersonaLine
	report.FeaturedQuote = data.FeaturedQuote
	report.Status = model.MonthlyReportStatusGenerated

	// 序列化类型统计数据，处理可能的错误
	genreJSON, err := json.Marshal(data.GenreStats)
	if err != nil {
		log.Printf("[MonthlyReport] 警告：序列化类型统计失败，用户 %d, 月份 %s: %v", userID, yearMonth, err)
		report.GenreStats = "[]" // 设置为空数组作为默认值
	} else {
		report.GenreStats = string(genreJSON)
	}

	// 序列化海报墙
	posterJSON, err := json.Marshal(data.PosterWall)
	if err != nil {
		log.Printf("[MonthlyReport] 警告：序列化海报墙失败，用户 %d, 月份 %s: %v", userID, yearMonth, err)
		report.PosterWall = "[]"
	} else {
		report.PosterWall = string(posterJSON)
	}

	if data.TopMovie != nil {
		report.TopMovieID = data.TopMovie.DoubanID
		report.TopMovieTitle = data.TopMovie.Title
		report.TopMoviePoster = data.TopMovie.Poster
		report.TopMovieRating = data.TopMovie.Rating
	}

	// 百分位：依赖全站分布，样本太小时不计算（避免"超过0%用户"这种没有说服力的数字）
	report.PercentileRank = s.calculatePercentile(userID, data.WatchedCount, allCounts)

	if err := s.repos.MonthlyReport.Save(report); err != nil {
		return fmt.Errorf("更新报告失败: %w", err)
	}

	log.Printf("[MonthlyReport] 用户 %d 月报 %s 已生成: 看过 %d 部, 评分 %.1f, 人设 %s", userID, yearMonth, data.WatchedCount, data.AvgRating, data.PersonaTitle)
	return nil
}

// parseMonthRange 解析月份范围
func (s *MonthlyReportService) parseMonthRange(yearMonth string) (time.Time, time.Time, error) {
	t, err := time.Parse("2006-01", yearMonth)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 1, 0)
	return start, end, nil
}

// calculateStats 计算统计数据
func (s *MonthlyReportService) calculateStats(watchedList []*model.UserMovie) MonthlyReportData {
	data := MonthlyReportData{}
	genreMap := map[string]int{}
	var topRating int
	var topMovie *TopMovieData
	ratingSum := 0
	ratingCount := 0

	for _, item := range watchedList {
		data.WatchedCount++

		if item.Rating > 0 {
			ratingSum += item.Rating
			ratingCount++

			if item.Rating > topRating {
				topRating = item.Rating
				topMovie = &TopMovieData{
					DoubanID: item.MovieID,
					Title:    item.Title,
					Poster:   item.Poster,
					Rating:   item.Rating,
				}
			}
		}

		// 尝试从 movies 表获取类型
		movie, _ := s.repos.Movie.FindByDoubanID(item.MovieID)
		if movie != nil && movie.Genres != "" {
			for _, g := range splitGenres(movie.Genres) {
				genreMap[g]++
			}
		}
	}

	if ratingCount > 0 {
		data.AvgRating = float64(ratingSum) / float64(ratingCount)
	}

	data.TopMovie = topMovie

	// 转换类型统计为切片并计算百分比
	total := 0
	for _, count := range genreMap {
		total += count
	}
	for genre, count := range genreMap {
		pct := 0
		if total > 0 {
			pct = count * 100 / total
		}
		data.GenreStats = append(data.GenreStats, GenreStat{
			Genre: genre,
			Count: count,
			Pct:   pct,
		})
	}
	// 按数量排序
	sortGenreStats(data.GenreStats)

	return data
}

// calculateContinuousDays 计算连续观影天数
func (s *MonthlyReportService) calculateContinuousDays(userID int, endTime time.Time) int {
	// 基于 user_movies 的 created_at 计算连续观影天数
	// 从 endTime 往前数连续有观看记录的天数
	days := 0
	currentDate := endTime.AddDate(0, 0, -1) // 从月末前一天开始

	for {
		// 检查当天是否有观看记录
		startOfDay := time.Date(currentDate.Year(), currentDate.Month(), currentDate.Day(), 0, 0, 0, 0, time.Local)
		endOfDay := startOfDay.AddDate(0, 0, 1)

		var count int64
		s.repos.DB.Model(&model.UserMovie{}).
			Where("user_id = ? AND status = ? AND created_at >= ? AND created_at < ?",
				userID, "watched", startOfDay, endOfDay).
			Count(&count)

		if count > 0 {
			days++
			currentDate = currentDate.AddDate(0, 0, -1)
		} else {
			break
		}

		if days > 30 { // 最多30天
			break
		}
	}

	return days
}

// calculatePercentile 计算用户本月观影数在全站活跃用户中的百分位（超过百分之多少的用户）
// 样本用户数少于 5 时返回 0（前端据此隐藏该字段），避免出现没有说服力的数字
func (s *MonthlyReportService) calculatePercentile(userID, myCount int, allCounts map[int]int) int {
	if len(allCounts) < 5 {
		return 0
	}
	total := len(allCounts)
	below := 0
	for uid, cnt := range allCounts {
		if uid == userID {
			continue
		}
		if cnt < myCount {
			below++
		}
	}
	pct := int(math.Round(float64(below) / float64(total-1) * 100))
	if pct >= 100 {
		pct = 99 // 留一点悬念，不说"超过100%"
	}
	if pct < 1 {
		pct = 1 // 只要参与了统计就至少显示 1%，0 专门用来表示"未计算"
	}
	return pct
}

// personaCombo 人设映射规则：两种类型的组合（不区分先后顺序）→ 称呼
type personaCombo struct {
	genres [2]string
	title  string
}

// personaCombos 双类型组合，按命中优先级从上到下匹配
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

// personaSingles 单一类型兜底映射
var personaSingles = map[string]string{
	"剧情":  "文艺片鉴赏家",
	"喜剧":  "快乐制造机",
	"动作":  "肾上腺素依赖者",
	"爱情":  "感情充沛玩家",
	"科幻":  "未来主义者",
	"动画":  "造梦者",
	"悬疑":  "推理爱好者",
	"惊悚":  "刺激感猎人",
	"犯罪":  "案件旁观者",
	"恐怖":  "胆量测试员",
	"纪录片": "现实观察员",
	"历史":  "时代旁观者",
	"战争":  "时代旁观者",
	"音乐":  "浪漫主义者",
	"歌舞":  "浪漫主义者",
	"奇幻":  "造梦者",
	"冒险":  "造梦者",
	"传记":  "现实观察员",
}

// defaultPersonaTitle 兜底人设：类型分布太平均、匹配不上任何组合时使用
const defaultPersonaTitle = "全能观众"

// matchPersonaTitle 根据本月类型分布匹配人设标题
func matchPersonaTitle(genreStats []GenreStat) string {
	if len(genreStats) == 0 {
		return defaultPersonaTitle
	}
	top := genreStats[0].Genre
	var second string
	if len(genreStats) > 1 {
		second = genreStats[1].Genre
	}
	if second != "" {
		for _, combo := range personaCombos {
			if comboMatches(combo.genres, top, second) {
				return combo.title
			}
		}
	}
	if title, ok := personaSingles[top]; ok {
		return title
	}
	return defaultPersonaTitle
}

func comboMatches(comboGenres [2]string, a, b string) bool {
	return (comboGenres[0] == a && comboGenres[1] == b) || (comboGenres[0] == b && comboGenres[1] == a)
}

// buildPersona 生成人设标题和判词
// 判词按优先级选取最有记忆点的一条信号：连续观影天数 > 深夜场占比 > 观影量 > 本月最佳 > 兜底
func (s *MonthlyReportService) buildPersona(data MonthlyReportData, watchedList []*model.UserMovie) (string, string) {
	title := matchPersonaTitle(data.GenreStats)

	nightCount, latestHour, hasNight := nightOwlStats(watchedList)
	nightRatio := 0
	if data.WatchedCount > 0 {
		nightRatio = nightCount * 100 / data.WatchedCount
	}

	var line string
	switch {
	case data.ContinuousDays >= 5 && hasNight:
		line = fmt.Sprintf("连续 %d 天没有一天缺席，最晚一次是%s。", data.ContinuousDays, formatHourZH(latestHour))
	case data.ContinuousDays >= 5:
		line = fmt.Sprintf("连续 %d 天没有一天缺席，看片比吃饭还准时。", data.ContinuousDays)
	case nightRatio >= 30 && hasNight:
		line = fmt.Sprintf("有 %d%% 的观影发生在深夜，最晚一次是%s。", nightRatio, formatHourZH(latestHour))
	case data.WatchedCount >= 20:
		line = fmt.Sprintf("这个月看得很勤，一共刷完了 %d 部。", data.WatchedCount)
	case data.TopMovie != nil:
		line = fmt.Sprintf("这个月最让你惊喜的是《%s》。", data.TopMovie.Title)
	default:
		line = fmt.Sprintf("这个月认真看完了 %d 部，每一部都算数。", data.WatchedCount)
	}

	return title, line
}

// nightOwlStats 统计本月"深夜场"（23点后或凌晨5点前）观影次数，并找出其中最晚的一次
func nightOwlStats(watchedList []*model.UserMovie) (count int, latestHour int, has bool) {
	bestScore := -1
	for _, item := range watchedList {
		h := item.CreatedAt.Hour()
		if h >= 23 || h < 5 {
			count++
			score := h
			if h < 5 {
				score = h + 24 // 让凌晨时段在比较时"更晚"
			}
			if score > bestScore {
				bestScore = score
				latestHour = h
				has = true
			}
		}
	}
	return count, latestHour, has
}

// formatHourZH 把 24 小时制的小时数格式化成中文口语时间点
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
		h := hour - 12
		if h == 0 {
			return "晚上 12 点"
		}
		return fmt.Sprintf("晚上 %d 点", h)
	}
}

// pickFeaturedQuote 精选一条短评摘录：优先用本月最佳电影的短评，否则取本月最长的一条
func (s *MonthlyReportService) pickFeaturedQuote(topMovie *TopMovieData, watchedList []*model.UserMovie) string {
	if topMovie != nil {
		for _, item := range watchedList {
			if item.MovieID == topMovie.DoubanID && strings.TrimSpace(item.Comment) != "" {
				return strings.TrimSpace(item.Comment)
			}
		}
	}
	var longest string
	for _, item := range watchedList {
		c := strings.TrimSpace(item.Comment)
		if len([]rune(c)) > len([]rune(longest)) {
			longest = c
		}
	}
	return longest
}

// samplePosterWall 从本月观影记录里抽样出固定数量的海报做拼贴墙
// 规则：按观看时间顺序均匀取样，代表"这个月的几个切片"，而不是简单取最近或最前的几部——
// 卡片数量固定，不随观影量变化，保证分享出去的卡片形状统一。
// 本月最佳已经在"特别放映"区域单独展示过：当本月看过的影片数达到 posterWallSize+1（即 5 部）
// 及以上时，海报墙里优先排除它，让海报墙尽量呈现不同的影片，提高卡片的信息密度；
// 但本月总共看得少（不足 5 部）时，可选片源本来就有限，海报墙应该完整展现这个月看过的一切
// 而不是刻意藏起最佳的那部，因此这种情况下不做排除，特别放映的电影也会出现在海报墙里。
func (s *MonthlyReportService) samplePosterWall(topMovie *TopMovieData, watchedList []*model.UserMovie) []model.PosterWallItem {
	// 只保留有海报的记录，并按观看时间正序排列
	candidates := make([]*model.UserMovie, 0, len(watchedList))
	for _, item := range watchedList {
		if item.Poster != "" {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})

	pool := candidates
	if topMovie != nil && len(watchedList) >= posterWallSize+1 {
		filtered := make([]*model.UserMovie, 0, len(candidates))
		for _, c := range candidates {
			if c.MovieID != topMovie.DoubanID {
				filtered = append(filtered, c)
			}
		}
		// 排除本月最佳后仍有内容可选，就用排除后的池子；
		// 排除后一部都不剩（比如本月唯一有海报的就是最佳这部），才允许退回去重复
		if len(filtered) > 0 {
			pool = filtered
		}
	}

	if len(pool) <= posterWallSize {
		result := make([]model.PosterWallItem, 0, len(pool))
		for _, c := range pool {
			result = append(result, model.PosterWallItem{MovieID: c.MovieID, Title: c.Title, Poster: c.Poster})
		}
		return result
	}

	// 按时间均匀分桶，每桶取一部代表这一段时间
	result := make([]model.PosterWallItem, 0, posterWallSize)
	bucketSize := float64(len(pool)) / float64(posterWallSize)
	for i := 0; i < posterWallSize; i++ {
		idx := int(float64(i) * bucketSize)
		if idx >= len(pool) {
			idx = len(pool) - 1
		}
		c := pool[idx]
		result = append(result, model.PosterWallItem{MovieID: c.MovieID, Title: c.Title, Poster: c.Poster})
	}
	return result
}

// splitGenres 分割类型字符串
func splitGenres(genres string) []string {
	// 支持 / 和 , 分隔
	var result []string
	current := ""
	for _, ch := range genres {
		if ch == '/' || ch == ',' || ch == '、' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// sortGenreStats 按数量降序排序
func sortGenreStats(stats []GenreStat) {
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].Count > stats[i].Count {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}
}
