package library

import "time"

const (
	StatusWish    = "wish"
	StatusWatched = "watched"
)

// Record 是用户片单记录。MovieID 保持字符串类型，
// 因为公开影片 URL 和导入的豆瓣标识不能当作数据库数字主键处理。
type Record struct {
	ID        int
	UserID    int
	MovieID   string
	Title     string
	Poster    string
	Year      string
	Status    string
	Rating    int
	Comment   string
	CreatedAt time.Time
	UpdatedAt time.Time
}
