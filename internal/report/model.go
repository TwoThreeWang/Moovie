// Package report 是月度观影报告，以及用户公开主页。
//
// 涉及的表：monthly_reports（报告结果，一个用户一个月一行）。
// 原始数据来自 user_movies，每月由定时任务算好存起来，页面只读不算。
//
// 报告内容包括：观影数量、平均评分、类型分布、最爱影片、连续天数、
// 观影人格标签、超过百分之多少的用户、金句和海报墙。
package report

import "time"

// Status 是报告生成状态。
type Status string

// 报告的四种状态：排队中、生成中、已生成、生成失败。
const (
	StatusPending    Status = "pending"
	StatusGenerating Status = "generating"
	StatusGenerated  Status = "generated"
	StatusFailed     Status = "failed"
)

// MonthlyReport 是一份月报。GenreStats 和 PosterWall 以 JSON 字符串存库。
type MonthlyReport struct {
	ID                   int
	UserID               int
	YearMonth            string
	WatchedCount         int
	TotalDurationMinutes int
	AvgRating            float64
	GenreStats           string
	TopMovieID           string
	TopMovieTitle        string
	TopMoviePoster       string
	TopMovieRating       int
	ContinuousDays       int
	PersonaTitle         string
	PersonaLine          string
	PercentileRank       int
	FeaturedQuote        string
	PosterWall           string
	Status               Status
	ErrorMessage         string
	GeneratedAt          *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// GenreStat 是一个类型的观影数量和占比。
type GenreStat struct {
	Genre string `json:"genre"`
	Count int    `json:"count"`
	Pct   int    `json:"pct"`
}

// PosterWallItem 是海报墙里的一部影片。
type PosterWallItem struct {
	MovieID string `json:"movie_id"`
	Title   string `json:"title"`
	Poster  string `json:"poster"`
}

// TopMovie 是本月最爱的影片。
type TopMovie struct {
	DoubanID string
	Title    string
	Poster   string
	Rating   int
}
