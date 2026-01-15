package repository

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

// InitDB 初始化数据库连接
func InitDB(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("无法连接数据库: %w", err)
	}

	// 测试连接
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("数据库 ping 失败: %w", err)
	}

	// 设置连接池
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	return db, nil
}

// Repositories 仓库集合
type Repositories struct {
	DB          *sql.DB
	User        *UserRepository
	Movie       *MovieRepository
	SearchCache *SearchCacheRepository
	Favorite    *FavoriteRepository
	History     *HistoryRepository
	Feedback    *FeedbackRepository
	SearchLog   *SearchLogRepository
	Site        *SiteRepository
}

// NewRepositories 创建仓库集合
func NewRepositories(db *sql.DB) *Repositories {
	return &Repositories{
		DB:          db,
		User:        NewUserRepository(db),
		Movie:       NewMovieRepository(db),
		SearchCache: NewSearchCacheRepository(db),
		Favorite:    NewFavoriteRepository(db),
		History:     NewHistoryRepository(db),
		Feedback:    NewFeedbackRepository(db),
		SearchLog:   NewSearchLogRepository(db),
		Site:        NewSiteRepository(db),
	}
}
