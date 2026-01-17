package repository

import (
	"errors"
	"time"

	"github.com/user/moovie/internal/model"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) *UserRepository {
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

	if err := r.db.Create(user).Error; err != nil {
		return nil, err
	}

	return user, nil
}

// FindByEmail 根据邮箱查找用户
func (r *UserRepository) FindByEmail(email string) (*model.User, error) {
	var user model.User
	err := r.db.Where("email = ?", email).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// FindByID 根据 ID 查找用户
func (r *UserRepository) FindByID(id int) (*model.User, error) {
	var user model.User
	err := r.db.First(&user, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return &user, nil
}

// CheckPassword 验证密码
func (r *UserRepository) CheckPassword(user *model.User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// UpdateUsername 更新用户名
func (r *UserRepository) UpdateUsername(userID int, username string) error {
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("username", username).Error
}

// UpdateEmail 更新邮箱
func (r *UserRepository) UpdateEmail(userID int, email string) error {
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("email", email).Error
}

// UpdatePassword 更新密码
func (r *UserRepository) UpdatePassword(userID int, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("password_hash", string(hash)).Error
}

// ListAll 获取所有用户列表
func (r *UserRepository) ListAll() ([]*model.User, error) {
	var users []*model.User
	err := r.db.Order("id ASC").Find(&users).Error
	return users, err
}

// Count 获取用户总数
func (r *UserRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&model.User{}).Count(&count).Error
	return count, err
}

// UpdateRole 更新用户角色
func (r *UserRepository) UpdateRole(userID int, role string) error {
	return r.db.Model(&model.User{}).Where("id = ?", userID).Update("role", role).Error
}

// Delete 删除用户
func (r *UserRepository) Delete(userID int) error {
	return r.db.Delete(&model.User{}, userID).Error
}
