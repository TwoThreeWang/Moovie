package repository

import (
	"fmt"

	_ "github.com/lib/pq"
	"github.com/user/moovie/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// InitDB 初始化数据库连接
func InitDB(databaseURL string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("无法连接数据库: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取 sql.DB 失败: %w", err)
	}

	// 设置连接池
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)

	// 启用 pgvector 扩展
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return nil, fmt.Errorf("无法启用 pgvector 扩展: %w", err)
	}

	// 自动迁移
	err = db.AutoMigrate(
		&model.User{},
		&model.Movie{},
		&model.Favorite{},
		&model.WatchHistory{},
		&model.Feedback{},
		&model.Site{},
	)
	if err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	return db, nil
}

// Repositories 仓库集合
type Repositories struct {
	DB        *gorm.DB
	User      *UserRepository
	Movie     *MovieRepository
	Favorite  *FavoriteRepository
	History   *HistoryRepository
	Feedback  *FeedbackRepository
	SearchLog *SearchLogRepository
	Site      *SiteRepository
	VodItem   *VodItemRepository
}

// NewRepositories 创建仓库集合
func NewRepositories(db *gorm.DB) *Repositories {
	return &Repositories{
		DB:        db,
		User:      NewUserRepository(db),
		Movie:     NewMovieRepository(db),
		Favorite:  NewFavoriteRepository(db),
		History:   NewHistoryRepository(db),
		Feedback:  NewFeedbackRepository(db),
		SearchLog: NewSearchLogRepository(db),
		Site:      NewSiteRepository(db),
		VodItem:   NewVodItemRepository(db),
	}
}
