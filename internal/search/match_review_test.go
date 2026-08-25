package search

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresMatchReviewCommitsCanonicalLinkAndCandidateTogether(t *testing.T) {
	transaction := &matchReviewTransaction{row: matchReviewRow{values: matchReviewValues(55, "source", "42", 9, MatchStatusReview, 0.71, "title_year")}}
	database := &matchReviewDatabase{transaction: transaction}
	store := NewPostgresStore(database)
	if err := store.ReviewMatchCandidate(context.Background(), "source", "42", 9, 3, MatchStatusVerified, "人工确认"); err != nil {
		t.Fatal(err)
	}
	if !transaction.committed || transaction.rolledBack {
		t.Fatalf("transaction state committed=%v rolledBack=%v", transaction.committed, transaction.rolledBack)
	}
	// 审计表已删（迁移 0044），事务里只剩三条写：关联、剧集候选、候选状态。
	if len(transaction.queries) != 3 {
		t.Fatalf("exec queries = %d: %v", len(transaction.queries), transaction.queries)
	}
	if containsQuery(transaction.queries, "resource_match_audits") {
		t.Fatalf("audit table is dropped but still written: %v", transaction.queries)
	}
	for _, expected := range []string{"INSERT INTO resource_media_links", "UPDATE resource_episode_candidates", "UPDATE resource_match_candidates"} {
		if !containsQuery(transaction.queries, expected) {
			t.Fatalf("missing %q in %v", expected, transaction.queries)
		}
	}
	if !strings.Contains(transaction.queries[0], "resource_media_links.is_locked = FALSE OR resource_media_links.media_id = EXCLUDED.media_id") {
		t.Fatalf("canonical link upsert can overwrite a conflicting manual lock: %s", transaction.queries[0])
	}
	if !reflect.DeepEqual(transaction.arguments[2], []any{int64(55), MatchStatusVerified, 9}) {
		t.Fatalf("candidate status arguments = %#v", transaction.arguments[2])
	}
}

func TestPostgresMatchReviewDoesNotOverwriteConflictingManualLock(t *testing.T) {
	transaction := &matchReviewTransaction{
		row:         matchReviewRow{values: matchReviewValues(56, "source", "42", 10, MatchStatusReview, 0.7, "title_year")},
		execResults: []int64{0},
	}
	store := NewPostgresStore(&matchReviewDatabase{transaction: transaction})
	err := store.ReviewMatchCandidate(context.Background(), "source", "42", 10, 3, MatchStatusVerified, "人工确认")
	if err == nil || !strings.Contains(err.Error(), "already locked") {
		t.Fatalf("conflicting lock error = %v", err)
	}
	if transaction.committed || !transaction.rolledBack || len(transaction.queries) != 1 {
		t.Fatalf("transaction state = committed:%v rolledBack:%v queries:%v", transaction.committed, transaction.rolledBack, transaction.queries)
	}
}

func TestPostgresMatchReviewRollsBackRejectedCandidateWithoutCanonicalLink(t *testing.T) {
	transaction := &matchReviewTransaction{row: matchReviewRow{values: matchReviewValues(57, "source", "43", 10, MatchStatusReview, 0.69, "title")}}
	store := NewPostgresStore(&matchReviewDatabase{transaction: transaction})
	if err := store.ReviewMatchCandidate(context.Background(), "source", "43", 10, 3, MatchStatusRejected, "年份不符"); err != nil {
		t.Fatal(err)
	}
	if len(transaction.queries) != 1 || containsQuery(transaction.queries, "resource_media_links") {
		t.Fatalf("rejected queries = %v", transaction.queries)
	}
}

func TestPostgresMatchReviewCanResolveStableCandidateID(t *testing.T) {
	transaction := &matchReviewTransaction{row: matchReviewRow{values: matchReviewValues(58, "source", "44", 11, MatchStatusReview, 0.73, "weighted_features")}}
	store := NewPostgresStore(&matchReviewDatabase{transaction: transaction})
	if err := store.ResolveMatchCandidateByID(context.Background(), 58, 99, 3, MatchStatusVerified, "选择了更准确的媒体实体"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(transaction.rowQuery, "WHERE id = $1") || !reflect.DeepEqual(transaction.rowArguments, []any{int64(58)}) {
		t.Fatalf("candidate ID lock = %s / %#v", transaction.rowQuery, transaction.rowArguments)
	}
	if !transaction.committed || len(transaction.queries) != 3 {
		t.Fatalf("candidate ID review = committed:%v queries:%v", transaction.committed, transaction.queries)
	}
	// 三条写都必须用管理员改选的 99，而不是候选原本猜的 11。
	if !reflect.DeepEqual(transaction.arguments[0], []any{"source", "44", 99}) ||
		!reflect.DeepEqual(transaction.arguments[1], []any{"source", "44", 99}) ||
		!reflect.DeepEqual(transaction.arguments[2], []any{int64(58), MatchStatusVerified, 99}) {
		t.Fatalf("alternative media resolution arguments = %#v", transaction.arguments)
	}
}

func matchReviewValues(id int64, sourceKey, vodID string, mediaID int, status string, confidence float64, method string) []any {
	return []any{id, sourceKey, vodID, mediaID, status, confidence, method}
}

func containsQuery(queries []string, fragment string) bool {
	for _, query := range queries {
		if strings.Contains(query, fragment) {
			return true
		}
	}
	return false
}

type matchReviewDatabase struct{ transaction *matchReviewTransaction }

func (fake *matchReviewDatabase) Begin(context.Context) (database.Transaction, error) {
	return fake.transaction, nil
}
func (*matchReviewDatabase) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (*matchReviewDatabase) QueryRow(context.Context, string, ...any) database.Row {
	return matchReviewRow{err: errors.New("unexpected query row")}
}
func (*matchReviewDatabase) Exec(context.Context, string, ...any) (int64, error) {
	return 0, errors.New("unexpected exec")
}

type matchReviewTransaction struct {
	row          matchReviewRow
	queries      []string
	arguments    [][]any
	execResults  []int64
	committed    bool
	rolledBack   bool
	rowQuery     string
	rowArguments []any
}

func (*matchReviewTransaction) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (transaction *matchReviewTransaction) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	transaction.rowQuery, transaction.rowArguments = query, arguments
	return transaction.row
}
func (transaction *matchReviewTransaction) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	transaction.queries = append(transaction.queries, query)
	transaction.arguments = append(transaction.arguments, arguments)
	if len(transaction.execResults) > 0 {
		result := transaction.execResults[0]
		transaction.execResults = transaction.execResults[1:]
		return result, nil
	}
	return 1, nil
}
func (transaction *matchReviewTransaction) Commit(context.Context) error {
	transaction.committed = true
	return nil
}
func (transaction *matchReviewTransaction) Rollback(context.Context) error {
	if !transaction.committed {
		transaction.rolledBack = true
	}
	return nil
}

type matchReviewRow struct {
	values []any
	err    error
}

func (row matchReviewRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(row.values) != len(destinations) {
		return fmt.Errorf("values/destinations = %d/%d", len(row.values), len(destinations))
	}
	for index, value := range row.values {
		destination := reflect.ValueOf(destinations[index]).Elem()
		source := reflect.ValueOf(value)
		if !source.Type().ConvertibleTo(destination.Type()) {
			return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
		}
		destination.Set(source.Convert(destination.Type()))
	}
	return nil
}
