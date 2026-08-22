package identity

import "context"

// Store 是账号的读写接口。
type Store interface {
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id int) (*User, error)
	ListUsers(ctx context.Context) ([]User, error)
	Create(ctx context.Context, user User) (*User, error)
	UpdateUsername(ctx context.Context, userID int, username string) error
	UpdateEmail(ctx context.Context, userID int, email string) error
	UpdatePassword(ctx context.Context, userID int, passwordHash string) error
	UpdateIsPublic(ctx context.Context, userID int, isPublic bool) error
	UpdateAvatar(ctx context.Context, userID int, avatar string) error
	UpdateRole(ctx context.Context, userID int, role string) error
	Delete(ctx context.Context, userID int) error
}
