package repository

import (
	"database/sql"
	"time"

	"github.com/user/moovie/internal/model"
	"golang.org/x/crypto/bcrypt"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create 创建用户
func (r *UserRepository) Create(email, username, password string) (*model.User, error) {
	// 密码哈希
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Email:        email,
		Username:     username,
		PasswordHash: string(hash),
		Role:         "user",
		CreatedAt:    time.Now(),
	}

	err = r.db.QueryRow(`
		INSERT INTO users (email, username, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, user.Email, user.Username, user.PasswordHash, user.Role, user.CreatedAt).Scan(&user.ID)

	if err != nil {
		return nil, err
	}

	return user, nil
}

// FindByEmail 根据邮箱查找用户
func (r *UserRepository) FindByEmail(email string) (*model.User, error) {
	user := &model.User{}
	err := r.db.QueryRow(`
		SELECT id, email, username, password_hash, role, created_at
		FROM users WHERE email = $1
	`, email).Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return user, nil
}

// FindByID 根据 ID 查找用户
func (r *UserRepository) FindByID(id int) (*model.User, error) {
	user := &model.User{}
	err := r.db.QueryRow(`
		SELECT id, email, username, password_hash, role, created_at
		FROM users WHERE id = $1
	`, id).Scan(&user.ID, &user.Email, &user.Username, &user.PasswordHash, &user.Role, &user.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return user, nil
}

// CheckPassword 验证密码
func (r *UserRepository) CheckPassword(user *model.User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// UpdateUsername 更新用户名
func (r *UserRepository) UpdateUsername(userID int, username string) error {
	_, err := r.db.Exec(`UPDATE users SET username = $1 WHERE id = $2`, username, userID)
	return err
}

// UpdateEmail 更新邮箱
func (r *UserRepository) UpdateEmail(userID int, email string) error {
	_, err := r.db.Exec(`UPDATE users SET email = $1 WHERE id = $2`, email, userID)
	return err
}

// UpdatePassword 更新密码
func (r *UserRepository) UpdatePassword(userID int, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`UPDATE users SET password_hash = $1 WHERE id = $2`, string(hash), userID)
	return err
}
