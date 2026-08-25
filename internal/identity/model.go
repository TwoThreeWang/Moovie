// Package identity 负责账号：注册、登录、退出、用户中心和账号设置。
// 只涉及一张 users 表。密码用 bcrypt 存哈希，登录态是写在 Cookie 里的 JWT。
// 用户中心上的各项计数（观看记录、片单、反馈、月报）通过可选依赖注入，
// 任何一项不可用只是显示 0，不影响页面渲染。
package identity

import "time"

// User 是一个账号。PasswordHash 带 json:"-"，绝不会随接口返回。
type User struct {
	ID             int       `json:"id"`
	Email          string    `json:"email"`
	Username       string    `json:"username"`
	PasswordHash   string    `json:"-"`
	Role           string    `json:"role"`
	DoubanUserID   string    `json:"douban_user_id"`
	IsPublic       bool      `json:"is_public"`
	Avatar         string    `json:"avatar"`
	AdSkipEnabled  bool      `json:"ad_skip_enabled"`
	CreatedAt      time.Time `json:"created_at"`
}
