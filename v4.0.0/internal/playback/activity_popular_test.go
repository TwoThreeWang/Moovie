package playback

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestActivityPopularProviderCombinesFirstPartySignals(t *testing.T) {
	fake := &activityDatabase{rows: &activityRows{values: [][]any{{"129", "影片", "2026", "https://img.example/poster.jpg", 8.8, 7.9, 4.2}}}}
	provider := NewActivityPopularProvider(fake)
	items, err := provider.Popular(context.Background(), "movie")
	if err != nil || len(items) != 1 {
		t.Fatalf("items/error = %+v/%v", items, err)
	}
	if items[0].ID != "129" || items[0].Rate != "8.8" || items[0].URL != "/movie/129" || !strings.HasPrefix(items[0].Cover, "/api/proxy/image/") {
		t.Fatalf("activity item = %+v", items[0])
	}
	if !strings.Contains(fake.query, "playback_attempt_events") || !strings.Contains(fake.query, "event_type = 'played_10s'") || !strings.Contains(fake.query, "MAX(created_at) AS created_at") || strings.Contains(fake.query, "GROUP BY event.media_id, CASE") || !strings.Contains(fake.query, "playback_positions") || !strings.Contains(fake.query, "user_movies") || strings.Contains(fake.query, "search_logs") || !reflect.DeepEqual(fake.arguments, []any{"movie"}) {
		t.Fatalf("activity query/args = %s/%#v", fake.query, fake.arguments)
	}
	if _, err := provider.Popular(context.Background(), "show"); err != nil {
		t.Fatalf("show activity fallback query = %v", err)
	}
	if fake.arguments[0] != "tv" {
		t.Fatalf("show media type = %#v", fake.arguments)
	}
}

func TestActivityMediaTypeRejectsUnknownType(t *testing.T) {
	if _, err := activityMediaType("unknown"); err == nil {
		t.Fatal("unknown media type accepted")
	}
}

type activityDatabase struct {
	query     string
	arguments []any
	rows      database.Rows
}

func (fake *activityDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query, fake.arguments = query, arguments
	return fake.rows, nil
}

func (fake *activityDatabase) QueryRow(context.Context, string, ...any) database.Row { return nil }
func (fake *activityDatabase) Exec(context.Context, string, ...any) (int64, error)   { return 0, nil }

type activityRows struct {
	values [][]any
	index  int
}

func (rows *activityRows) Next() bool { return rows.index < len(rows.values) }

func (rows *activityRows) Scan(destinations ...any) error {
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
		source := reflect.ValueOf(value)
		if source.Type().ConvertibleTo(destination.Type()) {
			destination.Set(source.Convert(destination.Type()))
			continue
		}
		return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
	}
	return nil
}

func (rows *activityRows) Err() error { return nil }
func (rows *activityRows) Close()     {}
