package identity

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// PostgresStore 是账号的 PostgreSQL 实现。
type PostgresStore struct{ database database.Executor }

// NewPostgresStore 创建存储实现。
func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

// userColumns 是各查询共用的字段列表。
const userColumns = `id, email, username, password_hash, role, douban_user_id, is_public, avatar, created_at`

// FindByEmail 按邮箱查账号，查不到返回 nil 而不是错误。
func (store *PostgresStore) FindByEmail(ctx context.Context, email string) (*User, error) {
	return store.find(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1 LIMIT 1`, email)
}

// FindByID 按 ID 查账号，查不到返回 nil 而不是错误。
func (store *PostgresStore) FindByID(ctx context.Context, id int) (*User, error) {
	return store.find(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1 LIMIT 1`, id)
}

// ListUsers 列出全部账号，供后台使用。
func (store *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := store.database.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.Role,
			&user.DoubanUserID, &user.IsPublic, &user.Avatar, &user.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

// find 是两个查询方法的公共实现。
func (store *PostgresStore) find(ctx context.Context, query string, argument any) (*User, error) {
	rows, err := store.database.Query(ctx, query, argument)
	if err != nil {
		return nil, fmt.Errorf("find user: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var user User
	if err := rows.Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.Role, &user.DoubanUserID, &user.IsPublic, &user.Avatar, &user.CreatedAt); err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	return &user, nil
}

// Create 新建账号。
func (store *PostgresStore) Create(ctx context.Context, user User) (*User, error) {
	row := store.database.QueryRow(ctx, `INSERT INTO users (email, username, password_hash, role, douban_user_id, is_public, avatar, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, user.Email, user.Username, user.PasswordHash, user.Role, user.DoubanUserID, user.IsPublic, user.Avatar, user.CreatedAt)
	if err := row.Scan(&user.ID); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &user, nil
}

// UpdateUsername 修改用户名。
func (store *PostgresStore) UpdateUsername(ctx context.Context, userID int, username string) error {
	return store.update(ctx, "username", userID, username)
}

// UpdateEmail 修改邮箱。
func (store *PostgresStore) UpdateEmail(ctx context.Context, userID int, email string) error {
	return store.update(ctx, "email", userID, email)
}

// UpdatePassword 修改密码哈希。
func (store *PostgresStore) UpdatePassword(ctx context.Context, userID int, passwordHash string) error {
	return store.update(ctx, "password_hash", userID, passwordHash)
}

// UpdateIsPublic 切换主页公开状态。
func (store *PostgresStore) UpdateIsPublic(ctx context.Context, userID int, isPublic bool) error {
	return store.update(ctx, "is_public", userID, isPublic)
}

// UpdateAvatar 修改头像。
func (store *PostgresStore) UpdateAvatar(ctx context.Context, userID int, avatar string) error {
	return store.update(ctx, "avatar", userID, avatar)
}

// UpdateRole 修改角色（user / admin）。
func (store *PostgresStore) UpdateRole(ctx context.Context, userID int, role string) error {
	return store.update(ctx, "role", userID, role)
}

// Delete 删除账号。
func (store *PostgresStore) Delete(ctx context.Context, userID int) error {
	if _, err := store.database.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// UpdateDoubanUserID 绑定或解绑豆瓣账号。
func (store *PostgresStore) UpdateDoubanUserID(ctx context.Context, userID int, doubanUserID string) error {
	return store.update(ctx, "douban_user_id", userID, doubanUserID)
}

// ListBoundDoubanUsers 列出绑定了豆瓣的账号，供同步任务使用。
func (store *PostgresStore) ListBoundDoubanUsers(ctx context.Context) ([]User, error) {
	rows, err := store.database.Query(ctx, `SELECT `+userColumns+` FROM users WHERE douban_user_id != '' ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list users with Douban binding: %w", err)
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		var user User
		if err := rows.Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.Role,
			&user.DoubanUserID, &user.IsPublic, &user.Avatar, &user.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users with Douban binding: %w", err)
	}
	return users, nil
}

// update 是所有单字段更新的公共实现。
func (store *PostgresStore) update(ctx context.Context, column string, userID int, value any) error {
	allowed := map[string]bool{"username": true, "email": true, "password_hash": true, "is_public": true, "avatar": true, "douban_user_id": true, "role": true}
	if !allowed[column] {
		return fmt.Errorf("unsupported user column")
	}
	if _, err := store.database.Exec(ctx, `UPDATE users SET `+column+` = $2 WHERE id = $1`, userID, value); err != nil {
		return fmt.Errorf("update user %s: %w", column, err)
	}
	return nil
}
