package repository

import (
	"fmt"
	"time"

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
	sqlDB.SetMaxOpenConns(15)           // 最大连接数
	sqlDB.SetMaxIdleConns(3)            // 最大空闲连接数
	sqlDB.SetConnMaxLifetime(time.Hour) // 连接最大生命周期

	// 启用 pgvector 扩展
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return nil, fmt.Errorf("无法启用 pgvector 扩展: %w", err)
	}

	// 启用 pg_trgm 扩展 (用于加速 LIKE 模糊搜索)
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error; err != nil {
		fmt.Printf("警告: 无法启用 pg_trgm 扩展: %v\n", err)
	}

	// 自动迁移
	err = db.AutoMigrate(
		&model.User{},
		&model.Movie{},
		&model.UserMovie{},
		&model.WatchHistory{},
		&model.Feedback{},
		&model.Site{},
		&model.CopyrightFilter{},
		&model.CategoryFilter{},
		&model.VodItem{},
		&model.SearchLog{},
		&model.TrendingKeyword{},
	)
	if err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	// 创建 HNSW 索引 (加速相似度搜索)
	if err := db.Exec("CREATE INDEX IF NOT EXISTS movies_embedding_idx ON movies USING hnsw (embedding vector_l2_ops);").Error; err != nil {
		fmt.Printf("警告: 创建向量索引失败: %v\n", err)
	}

	// 创建 GIN 索引 (加速 LIKE 模糊搜索)
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_movies_title_trgm ON movies USING gin (title gin_trgm_ops);").Error; err != nil {
		fmt.Printf("警告: 创建电影标题搜索索引失败: %v\n", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_vod_items_name_trgm ON vod_items USING gin (vod_name gin_trgm_ops);").Error; err != nil {
		fmt.Printf("警告: 创建视频名称搜索索引失败: %v\n", err)
	}

	// 创建 user_movies 索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_user_movies_lookup ON user_movies (user_id, updated_at DESC);").Error; err != nil {
		fmt.Printf("警告: 创建 user_movies lookup 索引失败: %v\n", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_user_movies_stats ON user_movies (user_id, created_at);").Error; err != nil {
		fmt.Printf("警告: 创建 user_movies stats 索引失败: %v\n", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_user_movies_comments ON user_movies (movie_id, updated_at DESC) WHERE status='watched' AND comment IS NOT NULL;").Error; err != nil {
		fmt.Printf("警告: 创建 user_movies comments 索引失败: %v\n", err)
	}

	// 迁移 favorites 到 user_movies 作为 wish
	if err := db.Exec(`
		INSERT INTO user_movies (user_id, movie_id, title, poster, year, status, created_at, updated_at)
		SELECT f.user_id, m.douban_id, m.title, m.poster, m.year, 'wish', f.created_at, f.created_at
		FROM favorites f
		JOIN movies m ON m.id = f.movie_id
		ON CONFLICT (user_id, movie_id) DO NOTHING
	`).Error; err != nil {
		fmt.Printf("警告: 迁移 favorites 到 user_movies 失败: %v\n", err)
	}

	return db, nil
}

// Repositories 仓库集合
type Repositories struct {
	DB              *gorm.DB
	User            *UserRepository
	Movie           *MovieRepository
	UserMovie       *UserMovieRepository
	History         *HistoryRepository
	Feedback        *FeedbackRepository
	SearchLog       *SearchLogRepository
	Site            *SiteRepository
	VodItem         *VodItemRepository
	CopyrightFilter *CopyrightFilterRepository
	CategoryFilter  *CategoryFilterRepository
}

// NewRepositories 创建仓库集合
func NewRepositories(db *gorm.DB) *Repositories {
	return &Repositories{
		DB:              db,
		User:            NewUserRepository(db),
		Movie:           NewMovieRepository(db),
		UserMovie:       NewUserMovieRepository(db),
		History:         NewHistoryRepository(db),
		Feedback:        NewFeedbackRepository(db),
		SearchLog:       NewSearchLogRepository(db),
		Site:            NewSiteRepository(db),
		VodItem:         NewVodItemRepository(db),
		CopyrightFilter: NewCopyrightFilterRepository(db),
		CategoryFilter:  NewCategoryFilterRepository(db),
	}
}
