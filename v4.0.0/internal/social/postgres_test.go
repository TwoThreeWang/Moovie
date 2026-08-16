package social

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestPostgresToggleLikeIsAtomicAndUsesUniqueConflictGuard(t *testing.T) {
	fake := &socialFakeDatabase{row: socialFakeRow{values: []any{true, 3}}}
	count, liked, err := NewPostgresStore(fake).ToggleLike(t.Context(), 9, 7)
	if err != nil || !liked || count != 3 {
		t.Fatalf("ToggleLike() = %d/%v/%v", count, liked, err)
	}
	for _, expected := range []string{"WITH deleted AS", "DELETE FROM comment_likes", "INSERT INTO comment_likes", "ON CONFLICT (user_movie_id, user_id) DO NOTHING", "SELECT COUNT(*)"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("toggle query missing %q: %s", expected, fake.query)
		}
	}
	if !reflect.DeepEqual(fake.arguments, []any{9, 7}) {
		t.Fatalf("arguments = %#v", fake.arguments)
	}
}

func TestCinemaQueriesPreferCanonicalFieldsAndAvoidSingleUserFlooding(t *testing.T) {
	fake := &socialFakeDatabase{}
	_, _ = NewPostgresStore(fake).ListWeeklyFilms(t.Context(), time.Now(), 6)
	for _, expected := range []string{"LEFT JOIN media ON media.id = um.media_id", "COALESCE(NULLIF(media.title, ''), MAX(um.title))", "COUNT(DISTINCT um.user_id)", "um.created_at >= $1"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("weekly program query missing %q: %s", expected, fake.query)
		}
	}
	_, _ = NewPostgresStore(fake).ListFeaturedComments(t.Context(), 6)
	for _, expected := range []string{"ROW_NUMBER() OVER (PARTITION BY um.user_id", "ranked.user_rank <= 2", "COALESCE(NULLIF(media.title, ''), um.title)"} {
		if !strings.Contains(fake.query, expected) {
			t.Fatalf("featured comments query missing %q: %s", expected, fake.query)
		}
	}
}

type socialFakeDatabase struct {
	query     string
	arguments []any
	row       database.Row
}

func (fake *socialFakeDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query, fake.arguments = query, arguments
	return nil, fmt.Errorf("unexpected Query")
}

func (fake *socialFakeDatabase) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	fake.query, fake.arguments = query, arguments
	return fake.row
}

func (fake *socialFakeDatabase) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	fake.query, fake.arguments = query, arguments
	return 0, fmt.Errorf("unexpected Exec")
}

type socialFakeRow struct{ values []any }

func (row socialFakeRow) Scan(destinations ...any) error {
	if len(destinations) != len(row.values) {
		return fmt.Errorf("destinations = %d", len(destinations))
	}
	for index, value := range row.values {
		reflect.ValueOf(destinations[index]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}
