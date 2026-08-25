package report

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresReportStoreUsesFrozenUpsertAndGeneratedFilters(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	values := reportValues(now)
	fake := &reportFakeDatabase{rows: &reportFakeRows{values: [][]any{values}}}
	store := NewPostgresStore(fake)
	saved, err := store.Save(t.Context(), MonthlyReport{UserID: 7, YearMonth: "2026-07", Status: StatusGenerated, CreatedAt: now})
	if err != nil || saved == nil || saved.YearMonth != "2026-07" {
		t.Fatalf("saved/error = %+v/%v", saved, err)
	}
	for _, expected := range []string{"ON CONFLICT (user_id, year_month)", "persona_title=EXCLUDED.persona_title", "status=EXCLUDED.status", "RETURNING id, user_id"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("save query missing %q: %s", expected, fake.query)
		}
	}
	fake.rows = &reportFakeRows{values: [][]any{values}}
	latest, err := store.LatestByUser(t.Context(), 7)
	if err != nil || latest == nil || !strings.Contains(fake.query, "status = 'generated' ORDER BY year_month DESC") {
		t.Fatalf("latest/error/query = %+v/%v/%s", latest, err, fake.query)
	}
}

func reportValues(now time.Time) []any {
	generated := now
	return []any{1, 7, "2026-07", 5, 0, 4.2, "[]", "1", "最佳", "poster", 5, 2,
		"推理爱好者", "判词", 50, "短评", "[]", string(StatusGenerated), "", &generated, now, now}
}

type reportFakeDatabase struct {
	query string
	args  []any
	rows  database.Rows
}

func (fake *reportFakeDatabase) Query(_ context.Context, query string, args ...any) (database.Rows, error) {
	fake.query, fake.args = query, args
	return fake.rows, nil
}

func (fake *reportFakeDatabase) QueryRow(context.Context, string, ...any) database.Row { return nil }

func (fake *reportFakeDatabase) Exec(_ context.Context, query string, args ...any) (int64, error) {
	fake.query, fake.args = query, args
	return 1, nil
}

type reportFakeRows struct {
	values [][]any
	index  int
}

func (rows *reportFakeRows) Next() bool { return rows.index < len(rows.values) }

func (rows *reportFakeRows) Scan(destinations ...any) error {
	if rows.index >= len(rows.values) {
		return fmt.Errorf("scan after end")
	}
	values := rows.values[rows.index]
	rows.index++
	if len(values) != len(destinations) {
		return fmt.Errorf("values/destinations = %d/%d", len(values), len(destinations))
	}
	for index, value := range values {
		destination := reflect.ValueOf(destinations[index]).Elem()
		if value == nil {
			destination.Set(reflect.Zero(destination.Type()))
			continue
		}
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(destination.Type()) {
			destination.Set(source)
		} else if source.Type().ConvertibleTo(destination.Type()) {
			destination.Set(source.Convert(destination.Type()))
		} else {
			return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
		}
	}
	return nil
}

func (rows *reportFakeRows) Err() error { return nil }
func (rows *reportFakeRows) Close()     {}
