package workqueue

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestClassifySeparatesTerminalAndThrottledErrors(t *testing.T) {
	base := errors.New("upstream returned HTTP 429")
	throttled := Classify(fmt.Errorf("fetch IMDb ID: %w", Throttled(base, 7*time.Second)))
	if throttled.Outcome != OutcomeThrottled || throttled.RetryAfter != 7*time.Second {
		t.Fatalf("throttled = %+v", throttled)
	}
	if throttled.Message != "fetch IMDb ID: upstream returned HTTP 429" {
		t.Fatalf("message = %q", throttled.Message)
	}
	terminal := Classify(fmt.Errorf("wrapped: %w", Terminal(errors.New("Douban returned HTTP 404"))))
	if terminal.Outcome != OutcomeTerminal {
		t.Fatalf("terminal = %+v", terminal)
	}
	// 既不存在又撞上限流的条目没有理由继续排队，终止优先于限流。
	both := Classify(Terminal(Throttled(base, time.Second)))
	if both.Outcome != OutcomeTerminal {
		t.Fatalf("terminal over throttle = %+v", both)
	}
	if plain := Classify(errors.New("boom")); plain.Outcome != OutcomeRetry {
		t.Fatalf("plain = %+v", plain)
	}
}

func TestThrottleBackoffGrowsAndStaysUnderFifteenMinutes(t *testing.T) {
	if first, second := ThrottleBackoff(0), ThrottleBackoff(1); first != 30*time.Second || second != time.Minute {
		t.Fatalf("backoff = %s/%s", first, second)
	}
	if capped := ThrottleBackoff(50); capped != 15*time.Minute {
		t.Fatalf("capped backoff = %s", capped)
	}
}

func TestPostgresStoreRefundsThrottledAttemptsButStillGivesUpEventually(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	id, err := store.Enqueue(t.Context(), Spec{TaskType: "tmdb", SubjectKey: "1292052", MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Claim(t.Context(), time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim = %+v/%v", job, err)
	}
	if err := store.Fail(t.Context(), *job, Failure{Message: "429", Outcome: OutcomeThrottled}); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Get(t.Context(), id)
	// 限流不是任务的错：attempt 退回，任务留在 pending 等待下一轮。
	if stored.Status != StatusPending || stored.AttemptCount != 0 || stored.ThrottleCount != 1 {
		t.Fatalf("after throttle = %+v", stored)
	}
	for index := 1; index < maxThrottleAttempts; index++ {
		// 真库会把限流任务的 available_at 推到 30 秒之后（内存实现没有这个退避）。
		// 单测不可能真等，直接把它拨回当下，等价于"退避时间已过"。
		makeAvailableNow(t, store, id)
		claimed, _ := store.Claim(t.Context(), time.Minute)
		if claimed == nil {
			t.Fatalf("claim %d returned nothing", index)
		}
		_ = store.Fail(t.Context(), *claimed, Failure{Message: "429", Outcome: OutcomeThrottled})
	}
	stored, _ = store.Get(t.Context(), id)
	if stored.Status != StatusFailed || stored.ThrottleCount != maxThrottleAttempts {
		t.Fatalf("throttle ceiling = %+v", stored)
	}
}

func TestPostgresStoreFailsTerminalErrorsWithoutBurningRetries(t *testing.T) {
	store := NewPostgresStore(testdb.Pool(t))
	id, _ := store.Enqueue(t.Context(), Spec{TaskType: "douban_metadata", SubjectKey: "1292052", MaxAttempts: 5})
	job, _ := store.Claim(t.Context(), time.Minute)
	if err := store.Fail(t.Context(), *job, Failure{Message: "HTTP 404", Outcome: OutcomeTerminal}); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Get(t.Context(), id)
	if stored.Status != StatusFailed || stored.AttemptCount != 1 {
		t.Fatalf("terminal failure = %+v", stored)
	}
	if next, _ := store.Claim(t.Context(), time.Minute); next != nil {
		t.Fatalf("terminal job was reclaimed: %+v", next)
	}
}

// makeAvailableNow 把任务的退避时间拨回当下，让测试不必真的等待退避窗口。
func makeAvailableNow(t *testing.T, store *PostgresStore, jobID int) {
	t.Helper()
	if _, err := store.database.Exec(t.Context(), `UPDATE worker_jobs SET available_at = NOW() WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}
}
