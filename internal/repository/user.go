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
func (r *UserRepository) Create(email, password string) (*model.User, error) {
	// 密码哈希
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	user := &model.User{
		Email:        email,
		PasswordHash: string(hash),
		Role:         "user",
		CreatedAt:    time.Now(),
	}

	err = r.db.QueryRow(`
		INSERT INTO users (email, password_hash, role, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, user.Email, user.PasswordHash, user.Role, user.CreatedAt).Scan(&user.ID)

	if err != nil {
		return nil, err
	}

	return user, nil
}

// FindByEmail 根据邮箱查找用户
func (r *UserRepository) FindByEmail(email string) (*model.User, error) {
	user := &model.User{}
	err := r.db.QueryRow(`
		SELECT id, email, password_hash, role, created_at
		FROM users WHERE email = $1
	`, email).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt)

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
		SELECT id, email, password_hash, role, created_at
		FROM users WHERE id = $1
	`, id).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.CreatedAt)

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
