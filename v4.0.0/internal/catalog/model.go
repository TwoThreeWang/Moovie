package catalog

import "time"

type Movie struct {
	ID                    int
	DoubanID              string
	Title                 string
	OriginalTitle         string
	Year                  string
	Poster                string
	Rating                float64
	Genres                string
	Countries             string
	Directors             string
	Actors                string
	Summary               string
	Duration              string
	IMDbID                string
	SeriesStatus          string
	Backdrops             string
	EmbeddingContent      string
	EmbeddingSemanticHash string
	Embedding             []float32
	ReviewsJSON           string
	ReviewsUpdatedAt      time.Time
	MetadataStatus        string
	CompletenessScore     int
	NextRefreshAt         *time.Time
	UpdatedAt             time.Time
}

// SeriesSeason 是详情页季度导航所需的最小数据。
type SeriesSeason struct {
	DoubanID     string
	Title        string
	Year         string
	Rating       float64
	SeasonNumber int
	Current      bool
}

type Director struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
