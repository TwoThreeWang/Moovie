package service

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
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

// MonthlyReportData 月度报告数据
type MonthlyReportData struct {
	WatchedCount         int          `json:"watched_count"`
	TotalDurationMinutes int          `json:"total_duration_minutes"`
	AvgRating            float64      `json:"avg_rating"`
	GenreStats           []GenreStat  `json:"genre_stats"`
	TopMovie             *TopMovieData `json:"top_movie"`
	ContinuousDays       int          `json:"continuous_days"`
}

// TopMovieData 本月最佳电影
type TopMovieData struct {
	DoubanID string `json:"douban_id"`
	Title    string `json:"title"`
	Poster   string `json:"poster"`
	Rating   int    `json:"rating"`
}

// GenerateReport 为指定用户生成指定月份的报告
func (s *MonthlyReportService) GenerateReport(userID int, yearMonth string) error {
	// 检查是否已存在
	exists, _ := s.repos.MonthlyReport.Exists(userID, yearMonth)
	if exists {
		return nil
	}

	// 解析月份范围
	startTime, endTime, err := s.parseMonthRange(yearMonth)
	if err != nil {
		return fmt.Errorf("解析月份失败: %w", err)
	}

	// 创建 pending 记录
	report := &model.MonthlyReport{
		UserID:    userID,
		YearMonth: yearMonth,
		Status:    model.MonthlyReportStatusPending,
	}
	if err := s.repos.MonthlyReport.Upsert(report); err != nil {
		return fmt.Errorf("创建报告记录失败: %w", err)
	}

	// 查询本月 user_movies
	watchedList, err := s.repos.UserMovie.ListByUser(userID, "watched", 10000, 0)
	if err != nil {
		s.repos.MonthlyReport.UpdateStatus(report.ID, model.MonthlyReportStatusFailed, "查询观影记录失败")
		return fmt.Errorf("查询观影记录失败: %w", err)
	}

	// 过滤本月数据
	var monthWatched []*model.UserMovie
	for _, item := range watchedList {
		if item.CreatedAt.After(startTime) && item.CreatedAt.Before(endTime) {
			monthWatched = append(monthWatched, item)
		}
	}

	// 计算统计数据
	data := s.calculateStats(monthWatched)

	// 计算连续观影天数
	data.ContinuousDays = s.calculateContinuousDays(userID, endTime)

	// 更新报告
	report.WatchedCount = data.WatchedCount
	report.TotalDurationMinutes = data.TotalDurationMinutes
	report.AvgRating = math.Round(data.AvgRating*10) / 10
	report.ContinuousDays = data.ContinuousDays
	report.Status = model.MonthlyReportStatusGenerated

	// 序列化类型统计数据，处理可能的错误
	genreJSON, err := json.Marshal(data.GenreStats)
	if err != nil {
		log.Printf("[MonthlyReport] 警告：序列化类型统计失败，用户 %d, 月份 %s: %v", userID, yearMonth, err)
		report.GenreStats = "[]" // 设置为空数组作为默认值
	} else {
		report.GenreStats = string(genreJSON)
	}

	if data.TopMovie != nil {
		report.TopMovieID = data.TopMovie.DoubanID
		report.TopMovieTitle = data.TopMovie.Title
		report.TopMoviePoster = data.TopMovie.Poster
		report.TopMovieRating = data.TopMovie.Rating
	}

	if err := s.repos.MonthlyReport.Upsert(report); err != nil {
		return fmt.Errorf("更新报告失败: %w", err)
	}

	log.Printf("[MonthlyReport] 用户 %d 月报 %s 已生成: 看过 %d 部, 评分 %.1f", userID, yearMonth, data.WatchedCount, data.AvgRating)
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
