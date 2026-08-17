package workqueue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Handler func(context.Context, Job) error

type handlerEntry struct {
	run     Handler
	timeout time.Duration
}

type Schedule struct {
	Spec         Spec
	Interval     time.Duration
	InitialDelay time.Duration
}

type Dispatcher struct {
	store       Store
	concurrency int
	poll        time.Duration
	lease       time.Duration
	handlers    map[string]handlerEntry
	schedules   []Schedule
	logger      *slog.Logger
	cancel      context.CancelFunc
	wait        sync.WaitGroup
	mu          sync.Mutex
}

func NewDispatcher(store Store, concurrency int, poll time.Duration) *Dispatcher {
	if concurrency < 1 {
		concurrency = 1
	}
	if poll <= 0 {
		poll = 2 * time.Second
	}
	return &Dispatcher{store: store, concurrency: concurrency, poll: poll, lease: 30 * time.Minute,
		handlers: make(map[string]handlerEntry), logger: slog.Default()}
}

func (dispatcher *Dispatcher) Handle(taskType string, timeout time.Duration, handler Handler) {
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	dispatcher.handlers[taskType] = handlerEntry{run: handler, timeout: timeout}
}

func (dispatcher *Dispatcher) Schedule(schedule Schedule) {
	dispatcher.schedules = append(dispatcher.schedules, schedule)
}

func (dispatcher *Dispatcher) Start() error {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	if dispatcher.cancel != nil {
		return nil
	}
	if dispatcher.store == nil {
		return fmt.Errorf("worker queue store is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := dispatcher.store.Recover(ctx, time.Now()); err != nil {
		cancel()
		return err
	}
	dispatcher.cancel = cancel
	for index := 0; index < dispatcher.concurrency; index++ {
		dispatcher.wait.Add(1)
		go dispatcher.worker(ctx, index+1)
	}
	dispatcher.wait.Add(1)
	go dispatcher.recoverExpired(ctx)
	for _, schedule := range dispatcher.schedules {
		dispatcher.wait.Add(1)
		go dispatcher.schedule(ctx, schedule)
	}
	return nil
}

func (dispatcher *Dispatcher) recoverExpired(ctx context.Context) {
	defer dispatcher.wait.Done()
	interval := dispatcher.lease / 3
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			if err := dispatcher.store.Recover(ctx, now); err != nil {
				dispatcher.logger.Error("recover expired worker jobs", "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (dispatcher *Dispatcher) Stop(ctx context.Context) error {
	dispatcher.mu.Lock()
	if dispatcher.cancel != nil {
		dispatcher.cancel()
		dispatcher.cancel = nil
	}
	dispatcher.mu.Unlock()
	done := make(chan struct{})
	go func() { dispatcher.wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (dispatcher *Dispatcher) worker(ctx context.Context, workerID int) {
	defer dispatcher.wait.Done()
	for {
		job, err := dispatcher.store.Claim(ctx, dispatcher.lease)
		if err != nil {
			dispatcher.logger.Error("claim worker job", "worker", workerID, "error", err)
		} else if job != nil {
			dispatcher.execute(ctx, workerID, *job)
			continue
		}
		select {
		case <-time.After(dispatcher.poll):
		case <-ctx.Done():
			return
		}
	}
}

func (dispatcher *Dispatcher) execute(ctx context.Context, workerID int, job Job) {
	entry, ok := dispatcher.handlers[job.TaskType]
	if !ok {
		entry = handlerEntry{timeout: time.Minute, run: func(context.Context, Job) error {
			return Terminal(fmt.Errorf("unsupported task type %q", job.TaskType))
		}}
	}
	jobCtx, cancel := context.WithTimeout(ctx, entry.timeout)
	started := time.Now()
	err := entry.run(jobCtx, job)
	cancel()
	terminalCtx := context.WithoutCancel(ctx)
	if err != nil {
		failure := Classify(err)
		if failure.Outcome == OutcomeThrottled && failure.RetryAfter <= 0 {
			failure.RetryAfter = ThrottleBackoff(job.ThrottleCount)
		}
		if finishErr := dispatcher.store.Fail(terminalCtx, job, failure); finishErr != nil {
			dispatcher.logger.Error("fail worker job", "job_id", job.ID, "error", finishErr)
		}
		// 限流是上游状态而不是任务缺陷，用 WARN 记录，避免刷掉真正需要排查的 ERROR。
		if failure.Outcome == OutcomeThrottled {
			dispatcher.logger.Warn("worker job throttled", "worker", workerID, "job_id", job.ID,
				"task_type", job.TaskType, "duration_ms", time.Since(started).Milliseconds(),
				"throttle_count", job.ThrottleCount+1, "retry_in", failure.RetryAfter, "error", err)
			return
		}
		dispatcher.logger.Error("worker job failed", "worker", workerID, "job_id", job.ID,
			"task_type", job.TaskType, "duration_ms", time.Since(started).Milliseconds(),
			"terminal", failure.Outcome == OutcomeTerminal, "error", err)
		return
	}
	if err := dispatcher.store.Complete(terminalCtx, job.ID); err != nil {
		dispatcher.logger.Error("complete worker job", "job_id", job.ID, "error", err)
	}
	dispatcher.logger.Info("worker job completed", "worker", workerID, "job_id", job.ID, "task_type", job.TaskType, "duration_ms", time.Since(started).Milliseconds())
}

func (dispatcher *Dispatcher) schedule(ctx context.Context, schedule Schedule) {
	defer dispatcher.wait.Done()
	if schedule.Interval <= 0 {
		return
	}
	if schedule.InitialDelay > 0 {
		select {
		case <-time.After(schedule.InitialDelay):
		case <-ctx.Done():
			return
		}
	}
	for {
		if _, err := dispatcher.store.Enqueue(ctx, schedule.Spec); err != nil {
			dispatcher.logger.Error("enqueue scheduled worker job", "task_type", schedule.Spec.TaskType, "error", err)
		}
		select {
		case <-time.After(schedule.Interval):
		case <-ctx.Done():
			return
		}
	}
}
