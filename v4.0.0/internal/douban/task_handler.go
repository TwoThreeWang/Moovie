package douban

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

// TaskDaily 是每日定时同步在任务队列中的类型名。
const TaskDaily = "douban_daily"

// UserStore 是同步需要的账号接口。
type UserStore interface {
	FindByID(ctx context.Context, userID int) (*identity.User, error)
	UpdateDoubanUserID(ctx context.Context, userID int, doubanUserID string) error
	ListBoundDoubanUsers(ctx context.Context) ([]identity.User, error)
}

// SyncExecutor 是实际执行同步的接口。
type SyncExecutor interface {
	SyncFull(ctx context.Context, userID int, doubanUserID string, jobID int) error
	SyncIncremental(ctx context.Context, userID int, doubanUserID string, jobID int) error
}

// MonthlyGenerator 是可选的月报生成器，每日任务顺带触发上月月报。
type MonthlyGenerator interface{ GeneratePreviousMonth(context.Context) error }

// TaskHandler 是豆瓣同步的任务处理器。
type TaskHandler struct {
	jobs     JobStore
	users    UserStore
	executor SyncExecutor
	monthly  MonthlyGenerator
	now      func() time.Time
	logger   *slog.Logger
}

// TaskHandlerOption 用于注入可选依赖。
type TaskHandlerOption func(*TaskHandler)

// WithMonthlyGenerator 注入月报生成器。
func WithMonthlyGenerator(generator MonthlyGenerator) TaskHandlerOption {
	return func(handler *TaskHandler) { handler.monthly = generator }
}

// WithLogger 替换日志器。
func WithLogger(logger *slog.Logger) TaskHandlerOption {
	return func(handler *TaskHandler) {
		if logger != nil {
			handler.logger = logger
		}
	}
}

// NewTaskHandler 创建任务处理器。
func NewTaskHandler(jobs JobStore, users UserStore, executor SyncExecutor, options ...TaskHandlerOption) *TaskHandler {
	handler := &TaskHandler{jobs: jobs, users: users, executor: executor, now: time.Now, logger: slog.Default()}
	for _, option := range options {
		option(handler)
	}
	return handler
}

// CreateFull 创建全量同步任务。
func (handler *TaskHandler) CreateFull(ctx context.Context, userID int) (int, error) {
	return handler.create(ctx, userID, TypeFull)
}

// CreateIncremental 创建增量同步任务。
func (handler *TaskHandler) CreateIncremental(ctx context.Context, userID int) (int, error) {
	return handler.create(ctx, userID, TypeIncremental)
}

// create 是两种任务创建的公共实现，已有进行中的任务时直接复用。
func (handler *TaskHandler) create(ctx context.Context, userID int, syncType SyncType) (int, error) {
	job, err := handler.jobs.Create(ctx, userID, syncType)
	if err != nil {
		return 0, err
	}
	return job.ID, nil
}

// Handle 执行一个同步任务。
func (handler *TaskHandler) Handle(ctx context.Context, job workqueue.Job) error {
	userID, err := strconv.Atoi(job.SubjectKey)
	if err != nil {
		return fmt.Errorf("invalid user id %q", job.SubjectKey)
	}
	user, err := handler.users.FindByID(ctx, userID)
	if err != nil {
		return err
	}
	if user == nil || user.DoubanUserID == "" {
		return fmt.Errorf("用户未绑定豆瓣")
	}
	domainJob, err := mapJob(job)
	if err != nil {
		return err
	}
	if domainJob.SyncType == TypeIncremental {
		err = handler.executor.SyncIncremental(ctx, userID, user.DoubanUserID, job.ID)
	} else {
		err = handler.executor.SyncFull(ctx, userID, user.DoubanUserID, job.ID)
	}
	return err
}

// HandleDaily 是每日定时任务：给所有绑定豆瓣的用户排增量同步，并触发上月月报。
func (handler *TaskHandler) HandleDaily(ctx context.Context, _ workqueue.Job) error {
	failed, err := handler.jobs.RetryableBefore(ctx, handler.now().Add(-24*time.Hour), 50)
	if err != nil {
		return err
	}
	for _, job := range failed {
		if err := handler.jobs.ResetPending(ctx, job.ID); err != nil {
			handler.logger.Error("reset Douban sync job", "job_id", job.ID, "error", err)
		}
	}
	users, err := handler.users.ListBoundDoubanUsers(ctx)
	if err != nil {
		return err
	}
	for _, user := range users {
		active, err := handler.jobs.HasActive(ctx, user.ID)
		if err != nil {
			return err
		}
		if !active {
			if _, err := handler.CreateIncremental(ctx, user.ID); err != nil {
				return err
			}
		}
	}
	if handler.monthly != nil && handler.now().Day() == 1 {
		return handler.monthly.GeneratePreviousMonth(ctx)
	}
	return nil
}
