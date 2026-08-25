package mediaidentity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestNormalizeExternalTypePreservesProviderNamespaces(t *testing.T) {
	tests := []struct {
		provider, externalType, mediaType, want string
	}{
		{provider: "tmdb", externalType: "movie", want: "movie"},
		{provider: "tmdb", externalType: "season", want: "tv"},
		{provider: "tmdb", externalType: "tv_season_2", want: "tv_season_2"},
		{provider: "imdb", externalType: "tv_season_3", want: "tv_season_3"},
		{provider: "tmdb", mediaType: "show", want: "tv"},
		{provider: "douban", externalType: "season", want: "season"},
		{provider: "douban", mediaType: "show", want: "show"},
		{provider: "imdb", mediaType: "movie", want: "movie"},
		{provider: "other", want: "movie"},
	}
	for _, test := range tests {
		if got := normalizeExternalType(test.provider, test.externalType, test.mediaType); got != test.want {
			t.Errorf("normalizeExternalType(%q, %q, %q) = %q, want %q", test.provider, test.externalType, test.mediaType, got, test.want)
		}
	}
}

func TestUpsertExternalIDKeepsExistingMediaBinding(t *testing.T) {
	executor := &externalIDExecutor{affected: 1}
	store := NewPostgresStore(executor)
	err := store.UpsertExternalID(t.Context(), ExternalID{
		MediaID: 7, Provider: " TMDB ", ExternalType: "season", ExternalID: " 101 ",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"external_type", "ON CONFLICT (provider, external_type, external_id)",
		"WHERE media_external_ids.media_id = EXCLUDED.media_id",
	} {
		if !strings.Contains(executor.query, expected) {
			t.Fatalf("upsert query missing %q: %s", expected, executor.query)
		}
	}
	if len(executor.arguments) != 7 || executor.arguments[0] != 7 || executor.arguments[1] != "tmdb" ||
		executor.arguments[2] != "tv" || executor.arguments[3] != "101" {
		t.Fatalf("upsert arguments = %#v", executor.arguments)
	}

	executor.affected = 0
	err = store.UpsertExternalID(t.Context(), ExternalID{MediaID: 8, Provider: "tmdb", ExternalType: "tv", ExternalID: "101"})
	if !errors.Is(err, ErrExternalIDConflict) {
		t.Fatalf("conflicting upsert error = %v", err)
	}
}

func TestFindMediaIDByProviderIDRejectsAmbiguousSeasonBindings(t *testing.T) {
	executor := &providerIDExecutor{mediaID: 7}
	store := NewPostgresStore(executor)
	mediaID, err := store.FindMediaIDByProviderID(t.Context(), " IMDb ", " tt14688458 ")
	if err != nil || mediaID != 7 {
		t.Fatalf("media ID/error = %d/%v", mediaID, err)
	}
	if !strings.Contains(executor.query, "HAVING COUNT(DISTINCT media_id) = 1") {
		t.Fatalf("provider lookup can guess across seasons: %s", executor.query)
	}
	if !reflect.DeepEqual(executor.arguments, []any{"imdb", "tt14688458"}) {
		t.Fatalf("arguments = %#v", executor.arguments)
	}
}

type providerIDExecutor struct {
	mediaID   int
	query     string
	arguments []any
}

func (*providerIDExecutor) Exec(context.Context, string, ...any) (int64, error) {
	return 0, errors.New("unexpected Exec")
}
func (*providerIDExecutor) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, errors.New("unexpected Query")
}
func (executor *providerIDExecutor) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	executor.query, executor.arguments = query, arguments
	return providerIDRow{mediaID: executor.mediaID}
}

type providerIDRow struct{ mediaID int }

func (row providerIDRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected destination count")
	}
	value, ok := destinations[0].(*int)
	if !ok {
		return errors.New("unexpected destination type")
	}
	*value = row.mediaID
	return nil
}

type externalIDExecutor struct {
	affected  int64
	query     string
	arguments []any
}

func (executor *externalIDExecutor) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	executor.query, executor.arguments = query, arguments
	return executor.affected, nil
}

func (*externalIDExecutor) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (*externalIDExecutor) QueryRow(context.Context, string, ...any) database.Row { return nil }
