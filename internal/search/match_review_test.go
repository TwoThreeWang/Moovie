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
