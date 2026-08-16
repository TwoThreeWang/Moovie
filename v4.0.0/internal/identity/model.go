package identity

import "time"

type User struct {
	ID           int       `json:"id"`
	Email        string    `json:"email"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	DoubanUserID string    `json:"douban_user_id"`
	IsPublic     bool      `json:"is_public"`
	Avatar       string    `json:"avatar"`
	CreatedAt    time.Time `json:"created_at"`
}
