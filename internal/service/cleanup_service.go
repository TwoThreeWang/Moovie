package service

import (
	"log"
	"time"

	"github.com/user/moovie/internal/repository"
)

// CleanupService 清理服务
type CleanupService struct {
	repos *repository.Repositories
}

// NewCleanupService 创建清理服务
func NewCleanupService(repos *repository.Repositories) *CleanupService {
	return &CleanupService{repos: repos}
}

// Start 启动定时清理任务
func (s *CleanupService) Start() {
	// 每天凌晨 3 点执行一次
	ticker := time.NewTicker(24 * time.Hour)

	// 启动时先运行一次
	go s.runCleanup()

	go func() {
		for range ticker.C {
			s.runCleanup()
		}
	}()
}

func (s *CleanupService) runCleanup() {
	log.Println("[CleanupService] 开始清理过期数据...")

	// 1. 清理 10 天内无人访问的视频详情
	affected, err := s.repos.VodItem.DeleteInactive(10)
	if err != nil {
		log.Printf("[CleanupService] 清理视频详情失败: %v", err)
	} else {
		log.Printf("[CleanupService] 已清理 %d 条过期视频详情", affected)
	}

	// 2. 清理超过 30 天未搜索的热搜关键词
	cleanedKeywords, err := s.repos.SearchLog.DeleteOldKeywords(30)
	if err != nil {
		log.Printf("[CleanupService] 清理旧热搜关键词失败: %v", err)
	} else if cleanedKeywords > 0 {
		log.Printf("[CleanupService] 已清理 %d 条超过 30 天未搜索的热搜关键词", cleanedKeywords)
	}
}
