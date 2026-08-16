package identity

import (
	"context"
	"fmt"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

type PostgresStore struct{ database database.Executor }

func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

const userColumns = `id, email, username, password_hash, role, douban_user_id, is_public, avatar, created_at`

func (store *PostgresStore) FindByEmail(ctx context.Context, email string) (*User, error) {
	return store.find(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1 LIMIT 1`, email)
}

func (store *PostgresStore) FindByID(ctx context.Context, id int) (*User, error) {
	return store.find(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1 LIMIT 1`, id)
}

func (store *PostgresStore) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := store.database.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY id ASC`)
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

func (store *PostgresStore) Create(ctx context.Context, user User) (*User, error) {
	row := store.database.QueryRow(ctx, `INSERT INTO users (email, username, password_hash, role, douban_user_id, is_public, avatar, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, user.Email, user.Username, user.PasswordHash, user.Role, user.DoubanUserID, user.IsPublic, user.Avatar, user.CreatedAt)
	if err := row.Scan(&user.ID); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &user, nil
}

func (store *PostgresStore) UpdateUsername(ctx context.Context, userID int, username string) error {
	return store.update(ctx, "username", userID, username)
}

func (store *PostgresStore) UpdateEmail(ctx context.Context, userID int, email string) error {
	return store.update(ctx, "email", userID, email)
}

func (store *PostgresStore) UpdatePassword(ctx context.Context, userID int, passwordHash string) error {
	return store.update(ctx, "password_hash", userID, passwordHash)
}

func (store *PostgresStore) UpdateIsPublic(ctx context.Context, userID int, isPublic bool) error {
	return store.update(ctx, "is_public", userID, isPublic)
}

func (store *PostgresStore) UpdateAvatar(ctx context.Context, userID int, avatar string) error {
	return store.update(ctx, "avatar", userID, avatar)
}

func (store *PostgresStore) UpdateRole(ctx context.Context, userID int, role string) error {
	return store.update(ctx, "role", userID, role)
}

func (store *PostgresStore) Delete(ctx context.Context, userID int) error {
	if _, err := store.database.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

func (store *PostgresStore) UpdateDoubanUserID(ctx context.Context, userID int, doubanUserID string) error {
	return store.update(ctx, "douban_user_id", userID, doubanUserID)
}

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
