package workqueue

import (
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func failJob(t *testing.T, store *PostgresStore, outcome Outcome) Job {
	t.Helper()
	job, err := store.Claim(t.Context(), time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim = %+v/%v", job, err)
	}
	if err := store.Fail(t.Context(), *job, Failure{Message: "boom", Outcome: outcome}); err != nil {
		t.Fatal(err)
	}
	return *job
}

func TestRetryJobRestoresTheFullAttemptBudget(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	id, _ := store.Enqueue(t.Context(), Spec{TaskType: "tmdb", SubjectKey: "1292052", MaxAttempts: 1})
	failJob(t, store, OutcomeRetry)
	if stored, _ := store.Get(t.Context(), id); stored.Status != StatusFailed {
		t.Fatalf("job did not fail: %+v", stored)
	}
	retried, err := store.RetryJob(t.Context(), id)
	if err != nil || retried != 1 {
		t.Fatalf("retry = %d/%v", retried, err)
	}
	stored, _ := store.Get(t.Context(), id)
	// 不清零 attempt_count 的话，任务被领取后失败一次就又是 failed，重试按钮等于只按了半下。
	if stored.Status != StatusPending || stored.AttemptCount != 0 || stored.ErrorMessage != "" {
		t.Fatalf("retried job = %+v", stored)
	}
	if claimed, _ := store.Claim(t.Context(), time.Minute); claimed == nil {
		t.Fatal("retried job was not claimable")
	}
}

func TestRetryJobSkipsWhenTheSameSubjectIsAlreadyQueued(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	failedID, _ := store.Enqueue(t.Context(), Spec{TaskType: "douban_metadata", SubjectKey: "1292052"})
	failJob(t, store, OutcomeTerminal)
	// 调度器随后为同一对象建了新任务；活跃唯一索引不允许两条同时 pending。
	if _, err := store.Enqueue(t.Context(), Spec{TaskType: "douban_metadata", SubjectKey: "1292052"}); err != nil {
		t.Fatal(err)
	}
	retried, err := store.RetryJob(t.Context(), failedID)
	if err != nil || retried != 0 {
		t.Fatalf("retry = %d/%v", retried, err)
	}
	if stored, _ := store.Get(t.Context(), failedID); stored.Status != StatusFailed {
		t.Fatalf("failed job should stay failed: %+v", stored)
	}
}

func TestRetryFailedFiltersByTaskTypeAndHonorsLimit(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	// MaxAttempts 设成 1，让第一次失败就进入终态，省去铺垫。
	for _, subject := range []string{"1292052", "1291864", "1296139"} {
		_, _ = store.Enqueue(t.Context(), Spec{TaskType: "tmdb", SubjectKey: subject, MaxAttempts: 1})
		failJob(t, store, OutcomeRetry)
	}
	_, _ = store.Enqueue(t.Context(), Spec{TaskType: "embedding", SubjectKey: "1292052", MaxAttempts: 1})
	failJob(t, store, OutcomeRetry)

	retried, err := store.RetryFailed(t.Context(), "tmdb", 2)
	if err != nil || retried != 2 {
		t.Fatalf("limited retry = %d/%v", retried, err)
	}
	if jobs, _ := store.List(t.Context(), "tmdb", StatusPending, time.Time{}, 100); len(jobs) != 2 {
		t.Fatalf("pending tmdb jobs = %d", len(jobs))
	}
	if embeddings, _ := store.List(t.Context(), "embedding", StatusFailed, time.Time{}, 10); len(embeddings) != 1 {
		t.Fatalf("embedding jobs must not be touched by a tmdb-only retry: %+v", embeddings)
	}
	if retried, _ := store.RetryFailed(t.Context(), "", 100); retried != 2 {
		t.Fatalf("remaining retry = %d, want the last tmdb job and the embedding job", retried)
	}
}
