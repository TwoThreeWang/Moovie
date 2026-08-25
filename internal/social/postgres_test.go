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
	fake := &socialFakeDatabase{rows: []database.Row{
		socialFakeRow{values: []any{true}}, // 切换点赞
		socialFakeRow{values: []any{3}},    // 随后单独计数
	}}
	count, liked, err := NewPostgresStore(fake).ToggleLike(t.Context(), 9, 7)
	if err != nil || !liked || count != 3 {
		t.Fatalf("ToggleLike() = %d/%v/%v", count, liked, err)
	}
	for _, expected := range []string{"WITH deleted AS", "DELETE FROM comment_likes", "INSERT INTO comment_likes", "ON CONFLICT (user_movie_id, user_id) DO NOTHING"} {
		if !strings.Contains(fake.queries[0], expected) {
			t.Fatalf("toggle query missing %q: %s", expected, fake.queries[0])
		}
	}
	// 计数必须是独立的第二条语句：合并进上一条会读到旧快照，少算刚插入的点赞。
	if len(fake.queries) != 2 || !strings.Contains(fake.queries[1], "SELECT COUNT(*) FROM comment_likes") {
		t.Fatalf("count query = %#v", fake.queries)
	}
	if !reflect.DeepEqual(fake.argsList[0], []any{9, 7}) {
		t.Fatalf("toggle arguments = %#v", fake.argsList[0])
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
	queries   []string
	arguments []any
	argsList  [][]any
	row       database.Row
	rows      []database.Row // 按 QueryRow 调用顺序依次返回，为空时回退到 row
}

func (fake *socialFakeDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query, fake.arguments = query, arguments
	return nil, fmt.Errorf("unexpected Query")
}

func (fake *socialFakeDatabase) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	fake.query, fake.arguments = query, arguments
	fake.queries = append(fake.queries, query)
	fake.argsList = append(fake.argsList, arguments)
	if len(fake.rows) > 0 {
		row := fake.rows[0]
		fake.rows = fake.rows[1:]
		return row
	}
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
