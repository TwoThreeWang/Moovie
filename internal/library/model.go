// Package library 是用户片单：想看和看过两种状态，看过可以打分和写短评。
// 只涉及一张 user_movies 表，但这张表被很多地方读：
// 首页热门榜的站内行为、月度报告、片场（social）、推荐（recommendation）。
package library

import "time"

// 片单的两种状态。
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
