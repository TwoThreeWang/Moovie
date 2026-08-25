package danmaku

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresCreateGuardedSerializesPerUserAndCommitsAllChecks(t *testing.T) {
	transaction := &danmakuFakeTransaction{count: 2, duplicate: false, insertedID: 19}
	store := NewPostgresStore(danmakuFakeBeginner{transaction: transaction})
	now := time.Now()
	record, err := store.CreateGuarded(t.Context(), Record{UserID: 7, VodKey: "三体|S01|E001", Text: "好看", Time: 2, Mode: 0, Color: "#FFFFFF", CreatedAt: now}, now.Add(-time.Minute), now.Add(-5*time.Minute), 10)
	if err != nil || record == nil || record.ID != 19 || !transaction.committed {
		t.Fatalf("record/error/committed = %+v/%v/%v", record, err, transaction.committed)
	}
	joined := strings.Join(transaction.queries, "\n")
	for _, expected := range []string{"pg_advisory_xact_lock", "COUNT(*) FROM danmakus", "SELECT EXISTS", "INSERT INTO danmakus"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("query sequence missing %q: %s", expected, joined)
		}
	}
	if len(transaction.lockArguments) != 1 || !reflect.DeepEqual(transaction.lockArguments[0], int64(7)) {
		t.Fatalf("lock arguments = %#v", transaction.lockArguments)
	}
}

func TestPostgresCreateGuardedStopsAtRateLimitBeforeDuplicateCheck(t *testing.T) {
	transaction := &danmakuFakeTransaction{count: 10, duplicate: true}
	_, err := NewPostgresStore(danmakuFakeBeginner{transaction: transaction}).CreateGuarded(t.Context(), Record{UserID: 7}, time.Now().Add(-time.Minute), time.Now().Add(-5*time.Minute), 10)
	if !errors.Is(err, ErrRateLimited) || transaction.committed {
		t.Fatalf("error/committed = %v/%v", err, transaction.committed)
	}
	joined := strings.Join(transaction.queries, "\n")
	if strings.Contains(joined, "SELECT EXISTS") || strings.Contains(joined, "INSERT INTO danmakus") {
		t.Fatalf("rate-limited create continued: %s", joined)
	}
}

type danmakuFakeBeginner struct{ transaction *danmakuFakeTransaction }

func (beginner danmakuFakeBeginner) Begin(context.Context) (database.Transaction, error) {
	return beginner.transaction, nil
}

type danmakuFakeTransaction struct {
	count         int
	duplicate     bool
	insertedID    int
	queries       []string
	lockArguments []any
	committed     bool
}

func (transaction *danmakuFakeTransaction) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, fmt.Errorf("unexpected Query")
}

func (transaction *danmakuFakeTransaction) QueryRow(_ context.Context, query string, _ ...any) database.Row {
	transaction.queries = append(transaction.queries, query)
	switch {
	case strings.Contains(query, "COUNT(*)"):
		return danmakuFakeRow{value: transaction.count}
	case strings.Contains(query, "SELECT EXISTS"):
		return danmakuFakeRow{value: transaction.duplicate}
	case strings.Contains(query, "INSERT INTO danmakus"):
		return danmakuFakeRow{value: transaction.insertedID}
	default:
		return danmakuFakeRow{err: fmt.Errorf("unexpected QueryRow")}
	}
}

func (transaction *danmakuFakeTransaction) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	transaction.queries = append(transaction.queries, query)
	if strings.Contains(query, "pg_advisory_xact_lock") {
		transaction.lockArguments = append(transaction.lockArguments, arguments...)
	}
	return 0, nil
}

func (transaction *danmakuFakeTransaction) Commit(context.Context) error {
	transaction.committed = true
	return nil
}

func (transaction *danmakuFakeTransaction) Rollback(context.Context) error { return nil }

type danmakuFakeRow struct {
	value any
	err   error
}

func (row danmakuFakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return fmt.Errorf("destinations = %d", len(destinations))
	}
	reflect.ValueOf(destinations[0]).Elem().Set(reflect.ValueOf(row.value))
	return nil
}
