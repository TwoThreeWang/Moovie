package report

import "time"

type Status string

const (
	StatusPending    Status = "pending"
	StatusGenerating Status = "generating"
	StatusGenerated  Status = "generated"
	StatusFailed     Status = "failed"
)

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

type GenreStat struct {
	Genre string `json:"genre"`
	Count int    `json:"count"`
	Pct   int    `json:"pct"`
}

type PosterWallItem struct {
	MovieID string `json:"movie_id"`
	Title   string `json:"title"`
	Poster  string `json:"poster"`
}

type TopMovie struct {
	DoubanID string
	Title    string
	Poster   string
	Rating   int
}
