package history

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

func TestPostgresStoreUpsertsOnlyCanonicalPlaybackPosition(t *testing.T) {
	database := &historyFakeDatabase{}
	store := NewPostgresStore(database)
	record := Record{UserID: 42, Source: "source", VodID: "vod", Title: "影片", WatchedAt: time.Now()}
	if err := store.Upsert(context.Background(), record); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	for _, expected := range []string{"INSERT INTO playback_positions", "ON CONFLICT (user_id, last_source_key, last_vod_id", "episode = CASE", "activity_at = EXCLUDED.activity_at"} {
		if !strings.Contains(database.execQuery, expected) {
			t.Fatalf("upsert query missing %q: %s", expected, database.execQuery)
		}
	}
	if len(database.arguments) != 16 || database.arguments[0] != 42 || database.arguments[7] != "vod" || database.arguments[6] != "source" || database.arguments[15] != "play" {
		t.Fatalf("upsert arguments = %#v", database.arguments)
	}
}

func TestPostgresStoreDashboardListUsesCanonicalOrderAndPagination(t *testing.T) {
	watchedAt := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	database := &historyFakeDatabase{rows: &historyFakeRows{values: [][]any{{1, 42, nil, nil, "1292052", "vod", "影片", "poster", "第01集", 1, "S01E01", 25, 30.5, 120.0, "source", "watch", watchedAt, watchedAt}}}}
	store := NewPostgresStore(database)
	records, err := store.ListByUser(t.Context(), 42, 24, 48)
	if err != nil || len(records) != 1 || records[0].EntryPage != "watch" {
		t.Fatalf("records/error = %+v/%v", records, err)
	}
	if !strings.Contains(database.query, "position.user_id = $1 AND position.deleted_at IS NULL") || !strings.Contains(database.query, "ORDER BY position.activity_at DESC LIMIT $2 OFFSET $3") || !reflect.DeepEqual(database.arguments, []any{42, 24, 48}) {
		t.Fatalf("list query/args = %s / %#v", database.query, database.arguments)
	}
	for _, expected := range []string{"COALESCE(NULLIF(media.title, ''), position.title)", "COALESCE(NULLIF(media.poster, ''), position.poster)"} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("canonical display projection missing %q: %s", expected, database.query)
		}
	}
}

// 标记"已看"后影片应离开继续观看列表，但 playback_positions 行必须保留：
// 它是唯一的服务端进度表，物理删除会让误点变成不可恢复的数据丢失。
func TestListContinueExcludesWatchedWithoutDeletingProgress(t *testing.T) {
	database := &historyFakeDatabase{rows: &historyFakeRows{}}
	store := NewPostgresStore(database)
	if _, err := store.ListContinue(t.Context(), 42, 24, 0); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"NOT EXISTS",
		"FROM user_movies",
		"user_movies.status = 'watched'",
		"user_movies.media_id = position.media_id",
		// 时间比较把"手动点已看"和"豆瓣历史标记"区分开。
		// 少了它，一次豆瓣全量同步会把用户正在追的剧从继续观看里抹掉。
		"user_movies.updated_at >= position.activity_at",
		"ORDER BY position.activity_at DESC LIMIT $2 OFFSET $3",
	} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("continue query missing %q: %s", expected, database.query)
		}
	}
	for _, forbidden := range []string{"DELETE FROM playback_positions", "UPDATE playback_positions"} {
		if strings.Contains(database.query, forbidden) || strings.Contains(database.execQuery, forbidden) {
			t.Fatalf("continue listing must not mutate playback progress: %s / %s", database.query, database.execQuery)
		}
	}
}

func TestPlaybackPositionUpsertsCanonicalUnitAndKeepsTombstone(t *testing.T) {
	database := &historyFakeDatabase{}
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	operation := SyncOperation{OperationID: "operation-shadow", Type: "upsert", MediaID: 7, MediaUnitID: 9,
		Source: "source", VodID: "vod", Title: "影片", Season: 1, EpisodeKey: "S01E03",
		Position: 30, Duration: 120, Progress: 25, EntryPage: "watch", OccurredAt: now}
	if err := upsertPlaybackPosition(t.Context(), database, 42, operation); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"INSERT INTO playback_positions", "ON CONFLICT (user_id, media_unit_id)", "server_version = nextval", "deleted_at = EXCLUDED.deleted_at"} {
		if !strings.Contains(database.execQuery, expected) {
			t.Fatalf("shadow upsert missing %q: %s", expected, database.execQuery)
		}
	}
	if len(database.arguments) != 17 || database.arguments[0] != 42 || database.arguments[15] != "watch" || database.arguments[16] != 9 {
		t.Fatalf("shadow arguments = %#v", database.arguments)
	}

	operation.Type = "delete"
	operation.OccurredAt = now.Add(time.Minute)
	if err := upsertPlaybackPosition(t.Context(), database, 42, operation); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(database.execQuery, "DELETE FROM playback_positions") || database.arguments[14] == nil {
		t.Fatalf("delete did not produce a recoverable tombstone: %s / %#v", database.execQuery, database.arguments)
	}
}

func TestPlaybackPositionFallbackIdentityNeverMergesKnownMediaWithUnknownResource(t *testing.T) {
	mediaTarget := playbackPositionConflictTarget(7)
	resourceTarget := playbackPositionConflictTarget(0)
	if !strings.Contains(mediaTarget, "user_id, media_id, season_number, episode_key") ||
		!strings.Contains(mediaTarget, "media_id IS NOT NULL") {
		t.Fatalf("media fallback target = %s", mediaTarget)
	}
	if !strings.Contains(resourceTarget, "last_source_key, last_vod_id") ||
		!strings.Contains(resourceTarget, "media_id IS NULL") {
		t.Fatalf("resource-only fallback target = %s", resourceTarget)
	}
}

type historyFakeDatabase struct {
	query     string
	execQuery string
	arguments []any
	rows      database.Rows
}

func (fake *historyFakeDatabase) Query(_ context.Context, query string, arguments ...any) (database.Rows, error) {
	fake.query, fake.arguments = query, arguments
	return fake.rows, nil
}

func (fake *historyFakeDatabase) QueryRow(context.Context, string, ...any) database.Row {
	return historyFakeRow{err: pgx.ErrNoRows}
}

func (fake *historyFakeDatabase) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	fake.execQuery, fake.arguments = query, arguments
	return 1, nil
}

type historyFakeRows struct {
	values [][]any
	index  int
}

func (rows *historyFakeRows) Next() bool { return rows.index < len(rows.values) }

func (rows *historyFakeRows) Scan(destinations ...any) error {
	if rows.index >= len(rows.values) {
		return fmt.Errorf("scan after end")
	}
	values := rows.values[rows.index]
	rows.index++
	if len(values) != len(destinations) {
		return fmt.Errorf("values/destinations = %d/%d", len(values), len(destinations))
	}
	for index, value := range values {
		if value == nil {
			continue
		}
		destination := reflect.ValueOf(destinations[index]).Elem()
		source := reflect.ValueOf(value)
		if !source.Type().ConvertibleTo(destination.Type()) {
			return fmt.Errorf("cannot assign %T to %s", value, destination.Type())
		}
		destination.Set(source.Convert(destination.Type()))
	}
	return nil
}

func (rows *historyFakeRows) Err() error { return nil }
func (rows *historyFakeRows) Close()     {}

type historyFakeRow struct{ err error }

func (row historyFakeRow) Scan(...any) error { return row.err }
