package playback

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

func TestSnapshotProviderFallsBackWhenNoReadySnapshotExists(t *testing.T) {
	store := NewPopularitySnapshotStore(&emptySnapshotDatabase{})
	fallback := popularProviderFunc(func(context.Context, string) ([]PopularSubject, error) {
		return []PopularSubject{{ID: "129", Title: "豆瓣回退"}}, nil
	})
	items, err := NewSnapshotPopularProvider(store, fallback).Popular(context.Background(), "movie")
	if err != nil || len(items) != 1 || items[0].Title != "豆瓣回退" {
		t.Fatalf("fallback items/error = %+v/%v", items, err)
	}
}

func TestSnapshotReadPrefersCanonicalDisplayFields(t *testing.T) {
	payload := []byte(`{"id":"external","title":"外部标题","year":"2020","cover":"external-poster","rate":"6.0"}`)
	database := &snapshotReadDatabase{rows: &snapshotRows{values: [][]any{{
		payload, "1292052", "主资料标题", "2026", "canonical-poster", 9.2,
	}}}}
	items, err := NewPopularitySnapshotStore(database).Popular(t.Context(), "movie")
	if err != nil || len(items) != 1 {
		t.Fatalf("popular items/error = %+v/%v", items, err)
	}
	item := items[0]
	if item.ID != "1292052" || item.Title != "主资料标题" || item.Year != "2026" || item.Rate != "9.2" || item.Cover != proxyImagePath("canonical-poster") {
		t.Fatalf("canonical popular item = %+v", item)
	}
	for _, expected := range []string{"LEFT JOIN media ON media.id = snapshot.media_id", "COALESCE(media.poster, '')"} {
		if !strings.Contains(database.query, expected) {
			t.Fatalf("snapshot query missing %q: %s", expected, database.query)
		}
	}
}

func TestPopularSubjectsKeepUnmatchedItemsAndCapAtFifty(t *testing.T) {
	primary := []PopularSubject{{ID: "0", Title: "快照第一名"}}
	supplement := make([]PopularSubject, 0, 51)
	for index := 0; index < 51; index++ {
		supplement = append(supplement, PopularSubject{ID: fmt.Sprint(index), Title: fmt.Sprintf("热门 %02d", index)})
	}
	items := mergePopularSubjects(primary, supplement, popularitySnapshotSize)
	if len(items) != 50 || items[0].Title != "快照第一名" || items[1].ID != "1" {
		t.Fatalf("merged popular items = %d/%+v", len(items), items[:2])
	}
}

func TestSnapshotReplaceRejectsEmptyPublishedRuns(t *testing.T) {
	store := NewPopularitySnapshotStore(&emptySnapshotDatabase{})
	err := store.Replace(t.Context(), "tv", nil, time.Hour)
	if !errors.Is(err, ErrEmptyPopularitySnapshot) {
		t.Fatalf("Replace error = %v, want ErrEmptyPopularitySnapshot", err)
	}
}

type emptySnapshotDatabase struct{}

func (*emptySnapshotDatabase) Query(context.Context, string, ...any) (database.Rows, error) {
	return emptyRows{}, nil
}

func (*emptySnapshotDatabase) QueryRow(context.Context, string, ...any) database.Row { return nil }
func (*emptySnapshotDatabase) Exec(context.Context, string, ...any) (int64, error)   { return 0, nil }
func (*emptySnapshotDatabase) Begin(context.Context) (database.Transaction, error)   { return nil, nil }

type emptyRows struct{}

func (emptyRows) Next() bool        { return false }
func (emptyRows) Scan(...any) error { return nil }
func (emptyRows) Err() error        { return nil }
func (emptyRows) Close()            {}

type snapshotReadDatabase struct {
	query string
	rows  database.Rows
}

func (fake *snapshotReadDatabase) Query(_ context.Context, query string, _ ...any) (database.Rows, error) {
	fake.query = query
	return fake.rows, nil
}
func (*snapshotReadDatabase) QueryRow(context.Context, string, ...any) database.Row { return nil }
func (*snapshotReadDatabase) Exec(context.Context, string, ...any) (int64, error)   { return 0, nil }
func (*snapshotReadDatabase) Begin(context.Context) (database.Transaction, error)   { return nil, nil }

type snapshotRows struct {
	values [][]any
	index  int
}

func (rows *snapshotRows) Next() bool { return rows.index < len(rows.values) }
func (rows *snapshotRows) Scan(destinations ...any) error {
	values := rows.values[rows.index]
	rows.index++
	if len(values) != len(destinations) {
		return fmt.Errorf("values/destinations = %d/%d", len(values), len(destinations))
	}
	for index, value := range values {
		reflect.ValueOf(destinations[index]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}
func (rows *snapshotRows) Err() error { return nil }
func (rows *snapshotRows) Close()     {}
