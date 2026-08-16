package danmaku

import "time"

type Record struct {
	ID        int
	VodKey    string
	Time      float64
	Text      string
	Mode      int
	Color     string
	UserID    int
	Deleted   bool
	CreatedAt time.Time
}

type Item struct {
	Text  string  `json:"text"`
	Time  float64 `json:"time"`
	Mode  int     `json:"mode"`
	Color string  `json:"color"`
}
