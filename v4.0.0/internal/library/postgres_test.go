package library

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresStorePreservesLegacyIdentityOrderingAndOwnership(t *testing.T) {
	fake := &libraryFakeDatabase{}
	store := NewPostgresStore(fake)
	createdAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	if err := store.Upsert(t.Context(), Record{UserID: 7, MovieID: "1292052", Title: "霸王别姬", Status: StatusWatched, Rating: 5, CreatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ON CONFLICT (user_id, movie_id)", "status = EXCLUDED.status", "rating = EXCLUDED.rating", "updated_at = EXCLUDED.updated_at", "created_at = CASE WHEN $11"} {
		if !strings.Contains(fake.execQuery, expected) {
			t.Fatalf("upsert query missing %q: %s", expected, fake.execQuery)
		}
	}
	if len(fake.arguments) != 11 || fake.arguments[0] != 7 || fake.arguments[1] != "1292052" || fake.arguments[8] != createdAt || fake.arguments[10] != true {
		t.Fatalf("upsert arguments = %#v", fake.arguments)
	}

	fake.rows = &libraryFakeRows{values: [][]any{{1, 7, "1292052", "霸王别姬", "poster", "1993", StatusWatched, 5, "经典", createdAt, createdAt}}}
	records, err := store.ListByUser(t.Context(), 7, StatusWatched, 24, 48)
	if err != nil || len(records) != 1 || records[0].Comment != "经典" {
		t.Fatalf("records/error = %+v/%v", records, err)
	}
	if !strings.Contains(fake.query, "WHERE um.user_id = $1 AND um.status = $2 ORDER BY um.updated_at DESC LIMIT $3 OFFSET $4") || !reflect.DeepEqual(fake.arguments, []any{7, StatusWatched, 24, 48}) {
		t.Fatalf("list query/args = %s / %#v", fake.query, fake.arguments)
	}
	for _, expected := range []string{"LEFT JOIN media ON media.id = um.media_id", "COALESCE(NULLIF(media.title, ''), um.title)", "COALESCE(NULLIF(media.poster, ''), um.poster)", "COALESCE(NULLIF(media.year, ''), um.year)"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("canonical display projection missing %q: %s", expected, fake.query)
		}
	}

	if err := store.UpdateRatingComment(t.Context(), 7, 1, 4, "更新"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.execQuery, "WHERE user_id = $1 AND id = $2") || !reflect.DeepEqual(fake.arguments, []any{7, 1, 4, "更新"}) {
		t.Fatalf("update query/args = %s / %#v", fake.execQuery, fake.arguments)
	}
	if err := store.Remove(t.Context(), 7, "1292052"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake.execQuery, "WHERE user_id = $1 AND movie_id = $2") {
		t.Fatalf("remove query = %s", fake.execQuery)
	}
}

type libraryFakeDatabase struct {
	query     string
	execQuery string
	arguments []any
	rows      database.Rows
}

func (fake *libraryFakeDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query, fake.arguments = query, arguments
	return fake.rows, nil
}

func (fake *libraryFakeDatabase) QueryRow(context.Context, string, ...any) database.Row {
	return libraryCountRow(0)
}

func (fake *libraryFakeDatabase) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	fake.execQuery, fake.arguments = query, arguments
	return 1, nil
}

type libraryCountRow int64

func (row libraryCountRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return fmt.Errorf("destinations = %d", len(destinations))
	}
	reflect.ValueOf(destinations[0]).Elem().SetInt(int64(row))
	return nil
}

type libraryFakeRows struct {
	values [][]any
	index  int
}

func (rows *libraryFakeRows) Next() bool { return rows.index < len(rows.values) }

func (rows *libraryFakeRows) Scan(destinations ...any) error {
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
		if !source.Type().ConvertibleTo(destination.Type()) {
			return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
		}
		destination.Set(source.Convert(destination.Type()))
	}
	return nil
}

func (rows *libraryFakeRows) Err() error { return nil }
func (rows *libraryFakeRows) Close()     {}
