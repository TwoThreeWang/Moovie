package model

import (
	"time"
)

// Movie 电影模型（豆瓣信息）
type Movie struct {
	ID            int       `json:"id" db:"id"`
	DoubanID      string    `json:"douban_id" db:"douban_id"`
	Title         string    `json:"title" db:"title"`
	OriginalTitle string    `json:"original_title" db:"original_title"`
	Year          string    `json:"year" db:"year"`
	Poster        string    `json:"poster" db:"poster"`
	Rating        float64   `json:"rating" db:"rating"`
	Genres        []string  `json:"genres" db:"genres"`
	Countries     []string  `json:"countries" db:"countries"`
	Directors     []Person  `json:"directors" db:"directors"`
	Actors        []Person  `json:"actors" db:"actors"`
	Summary       string    `json:"summary" db:"summary"`
	Duration      string    `json:"duration" db:"duration"`
	IMDbID        string    `json:"imdb_id" db:"imdb_id"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// Person 人物（导演/演员）
type Person struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role,omitempty"` // 角色（仅演员）
}
