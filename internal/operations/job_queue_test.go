package operations

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestJobQueueDecodesUnifiedJobs(t *testing.T) {
	payload := []byte(`{"counts":{"pending":2,"running":1,"completed":8,"failed":1},"jobs":[{"id":9,"task_type":"douban_reviews","subject_key":"1292052","reason":"page_reviews_missing","status":"pending","attempt_count":1,"max_attempts":5,"available_at":"2026-08-15T10:00:00Z","locked_by":"","error_message":"","created_at":"2026-08-15T10:00:00Z","updated_at":"2026-08-15T10:01:00Z"}]}`)
	store := NewMetricsStore(&metricsDatabase{row: metricsRow{payload: payload}})
	snapshot, err := store.JobQueue(context.Background(), JobQueueQuery{Status: "pending", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Counts.Pending != 2 || len(snapshot.Jobs) != 1 || snapshot.Jobs[0].TaskType != "douban_reviews" {
		t.Fatalf("queue = %+v", snapshot)
	}
}

func TestJobQueuePaginatesByID(t *testing.T) {
	next := JobQueueSnapshot{Jobs: []WorkerJob{{ID: 10}, {ID: 9}, {ID: 8}}}
	next.paginate(JobQueueQuery{Direction: "next", Limit: 2})
	if ids := []int64{next.Jobs[0].ID, next.Jobs[1].ID}; !reflect.DeepEqual(ids, []int64{10, 9}) || !next.Page.HasNext || next.Page.NextCursor != 9 {
		t.Fatalf("next = %+v/%+v", ids, next.Page)
	}
	previous := JobQueueSnapshot{Jobs: []WorkerJob{{ID: 11}, {ID: 12}, {ID: 13}}}
	previous.paginate(JobQueueQuery{Direction: "prev", Cursor: 10, Limit: 2})
	if ids := []int64{previous.Jobs[0].ID, previous.Jobs[1].ID}; !reflect.DeepEqual(ids, []int64{12, 11}) || !previous.Page.HasPrevious || !previous.Page.HasNext {
		t.Fatalf("previous = %+v/%+v", ids, previous.Page)
	}
}

func TestDeleteExpiredJobsUsesOnlyUnifiedTerminalRows(t *testing.T) {
	database := &jobCleanupDatabase{}
	store := NewMetricsStore(database)
	completedBefore, failedBefore := time.Now().AddDate(0, -1, 0), time.Now().AddDate(0, -3, 0)
	affected, err := store.DeleteExpiredJobs(context.Background(), completedBefore, failedBefore, 1000)
	if err != nil || affected != 3 || !strings.Contains(database.query, "worker_jobs") || strings.Contains(database.query, "metadata_refresh_jobs") {
		t.Fatalf("cleanup = %d/%v/%s", affected, err, database.query)
	}
	if !reflect.DeepEqual(database.arguments, []any{completedBefore, failedBefore, 1000}) {
		t.Fatalf("args = %#v", database.arguments)
	}
}

func TestJobQueueQueryContainsOnlyOperationalFields(t *testing.T) {
	for _, required := range []string{"worker_jobs", "task_type", "attempt_count", "error_message", "locked_by", "progress_done"} {
		if !strings.Contains(jobQueueSQL, required) {
			t.Fatalf("missing %q", required)
		}
	}
	for _, forbidden := range []string{"metadata_refresh_jobs", "douban_sync_jobs", "password_hash", "douban_cookie"} {
		if strings.Contains(jobQueueSQL, forbidden) {
			t.Fatalf("query exposes %q", forbidden)
		}
	}
}

type jobCleanupDatabase struct {
	query     string
	arguments []any
}

func (*jobCleanupDatabase) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, nil
}
func (*jobCleanupDatabase) QueryRow(context.Context, string, ...any) database.Row {
	return metricsRow{}
}
func (fake *jobCleanupDatabase) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	fake.query, fake.arguments = query, arguments
	return 3, nil
}
