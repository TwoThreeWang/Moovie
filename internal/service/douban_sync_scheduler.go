package service

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
)

// DoubanSyncScheduler 豆瓣同步调度器
type DoubanSyncScheduler struct {
	repos   *repository.Repositories
	syncSvc *DoubanSyncService
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	sem     chan struct{} // 限制并发数
}

// NewDoubanSyncScheduler 创建调度器
func NewDoubanSyncScheduler(repos *repository.Repositories, syncSvc *DoubanSyncService) *DoubanSyncScheduler {
	return &DoubanSyncScheduler{
		repos:   repos,
		syncSvc: syncSvc,
		sem:     make(chan struct{}, 2), // 最多同时跑 2 个同步任务
	}
}

// Start 启动调度器
func (s *DoubanSyncScheduler) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// 每 5 分钟检查一次 pending 任务
	pendingTicker := time.NewTicker(5 * time.Minute)
	// 每天凌晨 3 点触发增量同步和失败重试
	dailyTicker := time.NewTicker(24 * time.Hour)

	// 启动时立即运行一次 pending 任务检查
	utils.GoSafe(0, func(ctx context.Context) {
		s.runPendingJobs(ctx)
	})

	// 启动时延迟 1 分钟后运行一次日常同步，避免启动时资源争抢
	utils.GoSafe(0, func(ctx context.Context) {
		select {
		case <-time.After(1 * time.Minute):
			s.runDailySync(ctx)
		case <-ctx.Done():
			return
		}
	})

	go func() {
		defer pendingTicker.Stop()
		defer dailyTicker.Stop()
		for {
			select {
			case <-pendingTicker.C:
				s.runPendingJobs(s.ctx)
			case <-dailyTicker.C:
				s.runDailySync(s.ctx)
			case <-s.ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止调度器
func (s *DoubanSyncScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// CreateFullSyncJob 为用户创建全量同步任务
func (s *DoubanSyncScheduler) CreateFullSyncJob(userID int) (int, error) {
	job := &model.DoubanSyncJob{
		UserID:   userID,
		Status:   model.DoubanSyncStatusPending,
		SyncType: model.DoubanSyncTypeFull,
	}
	if err := s.repos.DoubanSyncJob.Create(job); err != nil {
		return 0, err
	}
	return job.ID, nil
}

// CreateIncrementalSyncJob 为用户创建增量同步任务
func (s *DoubanSyncScheduler) CreateIncrementalSyncJob(userID int) (int, error) {
	job := &model.DoubanSyncJob{
		UserID:   userID,
		Status:   model.DoubanSyncStatusPending,
		SyncType: model.DoubanSyncTypeIncremental,
	}
	if err := s.repos.DoubanSyncJob.Create(job); err != nil {
		return 0, err
	}
	return job.ID, nil
}

// ExecuteJobNow 立即异步执行指定任务
func (s *DoubanSyncScheduler) ExecuteJobNow(jobID int) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.sem <- struct{}{}
		defer func() { <-s.sem }()

		job, err := s.repos.DoubanSyncJob.GetByID(jobID)
		if err != nil {
			log.Printf("[DoubanSyncScheduler] 立即执行任务 %d 时获取任务失败: %v", jobID, err)
			return
		}
		if job.Status != model.DoubanSyncStatusPending {
			log.Printf("[DoubanSyncScheduler] 任务 %d 状态不是 pending，跳过立即执行", jobID)
			return
		}
		s.executeJob(s.ctx, job)
	}()
}

// runPendingJobs 执行待处理任务
func (s *DoubanSyncScheduler) runPendingJobs(ctx context.Context) {
	jobs, err := s.repos.DoubanSyncJob.ListPending(10)
	if err != nil {
		log.Printf("[DoubanSyncScheduler] 获取待执行任务失败: %v", err)
		return
	}

	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.wg.Add(1)
		go func(job *model.DoubanSyncJob) {
			defer s.wg.Done()
			s.sem <- struct{}{}
			defer func() { <-s.sem }()
			s.executeJob(ctx, job)
		}(job)
	}
}

// runDailySync 日常同步：为已绑定豆瓣的用户创建增量任务，并重试失败任务
func (s *DoubanSyncScheduler) runDailySync(ctx context.Context) {
	log.Println("[DoubanSyncScheduler] 开始日常增量同步...")

	// 重试昨天之前失败的
	failedJobs, err := s.repos.DoubanSyncJob.ListFailedBefore(time.Now().Add(-24*time.Hour), 50)
	if err != nil {
		log.Printf("[DoubanSyncScheduler] 获取失败任务失败: %v", err)
	} else {
		for _, job := range failedJobs {
			_ = s.repos.DoubanSyncJob.UpdateStatus(job.ID, model.DoubanSyncStatusPending, "")
		}
	}

	// 为已绑定用户创建增量同步任务
	users, err := s.repos.User.ListAll()
	if err != nil {
		log.Printf("[DoubanSyncScheduler] 获取用户列表失败: %v", err)
		return
	}

	for _, user := range users {
		if user.DoubanUserID == "" {
			continue
		}
		// 检查是否已有运行中或待执行的任务
		hasRunning, _ := s.repos.DoubanSyncJob.HasRunningJob(user.ID)
		if hasRunning {
			continue
		}
		if _, err := s.CreateIncrementalSyncJob(user.ID); err != nil {
			log.Printf("[DoubanSyncScheduler] 为用户 %d 创建增量任务失败: %v", user.ID, err)
		}
	}
}

// executeJob 执行单个同步任务
func (s *DoubanSyncScheduler) executeJob(ctx context.Context, job *model.DoubanSyncJob) {
	user, err := s.repos.User.FindByID(job.UserID)
	if err != nil {
		log.Printf("[DoubanSyncScheduler] 获取用户 %d 失败: %v", job.UserID, err)
		_ = s.repos.DoubanSyncJob.UpdateStatus(job.ID, model.DoubanSyncStatusFailed, "用户不存在")
		return
	}
	if user == nil || user.DoubanUserID == "" {
		_ = s.repos.DoubanSyncJob.UpdateStatus(job.ID, model.DoubanSyncStatusFailed, "用户未绑定豆瓣")
		return
	}

	if err := s.repos.DoubanSyncJob.UpdateStatus(job.ID, model.DoubanSyncStatusRunning, ""); err != nil {
		log.Printf("[DoubanSyncScheduler] 更新任务 %d 状态失败: %v", job.ID, err)
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	if job.SyncType == model.DoubanSyncTypeIncremental {
		err = s.syncSvc.SyncIncremental(jobCtx, job.UserID, user.DoubanUserID, job.ID)
	} else {
		err = s.syncSvc.SyncFull(jobCtx, job.UserID, user.DoubanUserID, job.ID)
	}

	if err != nil {
		log.Printf("[DoubanSyncScheduler] 任务 %d 执行失败: %v", job.ID, err)
	}
}
