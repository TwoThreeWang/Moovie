package model

import (
	"time"

	"github.com/pgvector/pgvector-go"
)

// Movie 电影模型（豆瓣信息）
type Movie struct {
	ID               int              `json:"id" db:"id"`
	DoubanID         string           `json:"douban_id" db:"douban_id" gorm:"unique"`
	Title            string           `json:"title" db:"title"`
	OriginalTitle    string           `json:"original_title" db:"original_title"`
	Year             string           `json:"year" db:"year"`
	Poster           string           `json:"poster" db:"poster"`
	Rating           float64          `json:"rating" db:"rating" gorm:"index"`
	Genres           string           `json:"genres" db:"genres"`
	Countries        string           `json:"countries" db:"countries"`
	Directors        string           `json:"directors" db:"directors"`
	Actors           string           `json:"actors" db:"actors"`
	Summary          string           `json:"summary" db:"summary"`
	Duration         string           `json:"duration" db:"duration"`
	IMDbID           string           `json:"imdb_id" db:"imdb_id"`
	EmbeddingContent string           `json:"embedding_content" db:"embedding_content"`
	Embedding        *pgvector.Vector `json:"embedding" db:"embedding" gorm:"type:vector(768)"`
	UpdatedAt        time.Time        `json:"updated_at" db:"updated_at" gorm:"index"`
}

// Person 人物（导演/演员）
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"` // 角色（仅演员）
}
