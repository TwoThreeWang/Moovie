package model

import (
	"time"
)

// User 用户模型
type User struct {
	ID           int       `json:"id" db:"id"`
	Email        string    `json:"email" db:"email"`
	Username     string    `json:"username" db:"username"`
	PasswordHash string    `json:"-" db:"password_hash"`
	Role         string    `json:"role" db:"role"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// SessionUser 专门用于 Session 存储的用户信息结构
type SessionUser struct {
	ID       int
	Email    string
	Username string
	Role     string
}

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

// SearchResult 搜索结果（从缓存 JSON 解析）
type SearchResult struct {
	Title    string   `json:"title"`
	Year     string   `json:"year"`
	Poster   string   `json:"poster"`
	DoubanID string   `json:"douban_id"`
	Sources  []Source `json:"sources"`
}

// Source 播放源
type Source struct {
	Name     string    `json:"name"`
	Episodes []Episode `json:"episodes"`
}

// Episode 剧集/播放链接
type Episode struct {
	Title string `json:"title"` // 如 "第1集" 或 "HD"
	URL   string `json:"url"`   // m3u8 链接
}

// Favorite 收藏
type Favorite struct {
	ID        int       `json:"id" db:"id"`
	UserID    int       `json:"user_id" db:"user_id"`
	MovieID   int       `json:"movie_id" db:"movie_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	Movie     *Movie    `json:"movie,omitempty"` // 关联查询时填充
}

// WatchHistory 观影历史
type WatchHistory struct {
	ID        int       `json:"id" db:"id"`
	UserID    int       `json:"user_id" db:"user_id"`
	DoubanID  string    `json:"douban_id" db:"douban_id"`
	Title     string    `json:"title" db:"title"`
	Poster    string    `json:"poster" db:"poster"`
	Episode   string    `json:"episode" db:"episode"`
	Progress  int       `json:"progress" db:"progress"`
	Source    string    `json:"source" db:"source"`
	WatchedAt time.Time `json:"watched_at" db:"watched_at"`
}

// SearchLog 搜索日志
type SearchLog struct {
	ID        int       `json:"id" db:"id"`
	Keyword   string    `json:"keyword" db:"keyword"`
	UserID    *int      `json:"user_id" db:"user_id"`
	IPHash    string    `json:"ip_hash" db:"ip_hash"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// Feedback 反馈
type Feedback struct {
	ID        int       `json:"id" db:"id"`
	UserID    *int      `json:"user_id" db:"user_id"`
	Type      string    `json:"type" db:"type"`
	Content   string    `json:"content" db:"content"`
	MovieURL  string    `json:"movie_url" db:"movie_url"`
	Status    string    `json:"status" db:"status"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// TrendingKeyword 热搜关键词
type TrendingKeyword struct {
	Keyword        string    `json:"keyword" db:"keyword"`
	Count          int       `json:"count" db:"count"`
	LastSearchedAt time.Time `json:"last_searched_at" db:"last_searched_at"`
}
