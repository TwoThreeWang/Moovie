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

const TaskDaily = "douban_daily"

type UserStore interface {
	FindByID(ctx context.Context, userID int) (*identity.User, error)
	UpdateDoubanUserID(ctx context.Context, userID int, doubanUserID string) error
	ListBoundDoubanUsers(ctx context.Context) ([]identity.User, error)
}

type SyncExecutor interface {
	SyncFull(ctx context.Context, userID int, doubanUserID string, jobID int) error
	SyncIncremental(ctx context.Context, userID int, doubanUserID string, jobID int) error
}

type MonthlyGenerator interface{ GeneratePreviousMonth(context.Context) error }

type TaskHandler struct {
	jobs     JobStore
	users    UserStore
	executor SyncExecutor
	monthly  MonthlyGenerator
	now      func() time.Time
	logger   *slog.Logger
}

type TaskHandlerOption func(*TaskHandler)

func WithMonthlyGenerator(generator MonthlyGenerator) TaskHandlerOption {
	return func(handler *TaskHandler) { handler.monthly = generator }
}
func WithLogger(logger *slog.Logger) TaskHandlerOption {
	return func(handler *TaskHandler) {
		if logger != nil {
			handler.logger = logger
		}
	}
}

func NewTaskHandler(jobs JobStore, users UserStore, executor SyncExecutor, options ...TaskHandlerOption) *TaskHandler {
	handler := &TaskHandler{jobs: jobs, users: users, executor: executor, now: time.Now, logger: slog.Default()}
	for _, option := range options {
		option(handler)
	}
	return handler
}

func (handler *TaskHandler) CreateFull(ctx context.Context, userID int) (int, error) {
	return handler.create(ctx, userID, TypeFull)
}
func (handler *TaskHandler) CreateIncremental(ctx context.Context, userID int) (int, error) {
	return handler.create(ctx, userID, TypeIncremental)
}
func (handler *TaskHandler) create(ctx context.Context, userID int, syncType SyncType) (int, error) {
	job, err := handler.jobs.Create(ctx, userID, syncType)
	if err != nil {
		return 0, err
	}
	return job.ID, nil
}

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
