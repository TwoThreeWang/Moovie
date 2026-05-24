package service

import (
	"context"
	"log"
	"time"

	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
)

// CleanupService 清理服务
type CleanupService struct {
	repos  *repository.Repositories
	cancel context.CancelFunc
}

// NewCleanupService 创建清理服务
func NewCleanupService(repos *repository.Repositories) *CleanupService {
	return &CleanupService{repos: repos}
}

// Start 启动定时清理任务
func (s *CleanupService) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	ticker := time.NewTicker(24 * time.Hour)

	// 启动时先运行一次
	utils.GoSafe(0, func(ctx context.Context) {
		s.runCleanup()
	})

	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runCleanup()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止定时清理任务
func (s *CleanupService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
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

	// 3. 清理超过 30 天的原始搜索日志
	cleanedLogs, err := s.repos.SearchLog.DeleteOldLogs(30)
	if err != nil {
		log.Printf("[CleanupService] 清理旧搜索日志失败: %v", err)
	} else if cleanedLogs > 0 {
		log.Printf("[CleanupService] 已清理 %d 条超过 30 天的原始搜索日志", cleanedLogs)
	}
}
