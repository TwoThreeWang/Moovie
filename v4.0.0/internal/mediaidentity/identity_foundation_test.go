package mediaidentity

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

func TestUpsertAliasPreservesDisplayValueAndWritesNormalizedKey(t *testing.T) {
	executor := &identityFoundationExecutor{}
	store := NewPostgresStore(executor)
	if err := store.UpsertAlias(t.Context(), Alias{
		MediaID: 7, Alias: " 黑袍纠察队：第二季 ", Source: " Douban ", AliasType: " Title ",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(executor.execQueries[0], "ON CONFLICT (media_id, normalized_alias)") {
		t.Fatalf("alias query = %s", executor.execQueries[0])
	}
	want := []any{7, "黑袍纠察队：第二季", "黑袍纠察队第二季", "", "douban", "title"}
	if !reflect.DeepEqual(executor.execArguments[0], want) {
		t.Fatalf("alias arguments = %#v, want %#v", executor.execArguments[0], want)
	}
}

func TestEnsureMediaUnitCreatesStableFeatureAndEpisodeIdentities(t *testing.T) {
	executor := &identityFoundationExecutor{rowValues: []int{41, 42}}
	store := NewPostgresStore(executor)
	feature, err := store.EnsureMediaUnit(t.Context(), MediaUnit{
		MediaID: 7, UnitType: "feature", SeasonNumber: 3, EpisodeKey: "S03E01", Title: "正片",
	})
	if err != nil {
		t.Fatal(err)
	}
	if feature.ID != 41 || feature.SeasonNumber != 0 || feature.EpisodeKey != "feature" {
		t.Fatalf("feature = %+v", feature)
	}
	episode, err := store.EnsureMediaUnit(t.Context(), MediaUnit{
		MediaID: 7, UnitType: "episode", SeasonNumber: 2, EpisodeKey: "s02e003", Title: "第三集",
	})
	if err != nil {
		t.Fatal(err)
	}
	if episode.ID != 42 || episode.EpisodeNumber != 3 || episode.EpisodeKey != "S02E003" {
		t.Fatalf("episode = %+v", episode)
	}
	for _, query := range executor.rowQueries {
		if !strings.Contains(query, "ON CONFLICT (media_id, unit_type, season_number, episode_key)") {
			t.Fatalf("unit query = %s", query)
		}
	}
}

func TestUpsertEpisodesBindsStructuredResourceToMediaUnit(t *testing.T) {
	executor := &identityFoundationExecutor{rowValues: []int{51, 61}}
	store := NewPostgresStore(executor)
	if err := store.UpsertEpisodes(t.Context(), []Episode{{
		SourceKey: "source", VodID: "42", MediaID: 7, UnitType: "episode",
		SeasonNumber: 2, EpisodeKey: "S02E03", EpisodeLabel: "第三集", PlayURL: "https://video.example/3.m3u8",
	}}); err != nil {
		t.Fatal(err)
	}
	if len(executor.execQueries) != 1 || !strings.Contains(executor.execQueries[0], "resource_episode_candidates") || strings.Contains(executor.execQueries[0], "resource_episodes\n") {
		t.Fatalf("resource episode query = %#v", executor.execQueries)
	}
	arguments := executor.execArguments[0]
	if len(arguments) != 12 || arguments[0] != 61 || arguments[1] != 7 || arguments[2] != 51 || arguments[3] != 2 || arguments[4] != "S02E03" {
		t.Fatalf("resource episode arguments = %#v", arguments)
	}
	if len(executor.rowQueries) != 2 || !strings.Contains(executor.rowQueries[1], "resource_play_lines") {
		t.Fatalf("resource line/candidate writes = rows:%#v args:%#v", executor.rowQueries, executor.execArguments)
	}
}

func TestUpsertEpisodesPreservesDefaultAndBackupLinesForSameEpisode(t *testing.T) {
	executor := &identityFoundationExecutor{rowValues: []int{71, 72}}
	store := NewPostgresStore(executor)
	episodes := []Episode{
		{LineKey: "default", LineLabel: "默认源", LineOrder: 0, SourceKey: "source", VodID: "42", SeasonNumber: 1, EpisodeKey: "S01E01", EpisodeLabel: "第一集", PlayURL: "https://a.example/1.m3u8"},
		{LineKey: "line-02", LineLabel: "备用源 B", LineOrder: 1, SourceKey: "source", VodID: "42", SeasonNumber: 1, EpisodeKey: "S01E01", EpisodeLabel: "第一集", PlayURL: "https://b.example/1.m3u8"},
	}
	if err := store.UpsertEpisodes(t.Context(), episodes); err != nil {
		t.Fatal(err)
	}
	if len(executor.rowQueries) != 2 || len(executor.execQueries) != 2 {
		t.Fatalf("multi-line writes = rows:%d execs:%d", len(executor.rowQueries), len(executor.execQueries))
	}
	candidateWrites := 0
	for _, query := range executor.execQueries {
		if strings.Contains(query, "INSERT INTO resource_episode_candidates") {
			candidateWrites++
		}
	}
	if candidateWrites != 2 {
		t.Fatalf("candidate writes = %d", candidateWrites)
	}
}

func TestRecordMatchCandidatePersistsReviewEvidence(t *testing.T) {
	executor := &identityFoundationExecutor{}
	store := NewPostgresStore(executor)
	if err := store.RecordMatchCandidate(t.Context(), " source ", " 42 ", 7, 0.7, "title_year"); err != nil {
		t.Fatal(err)
	}
	if len(executor.execQueries) != 1 || !strings.Contains(executor.execQueries[0], "resource_match_candidates") ||
		!strings.Contains(executor.execQueries[0], "status IN ('verified', 'rejected')") {
		t.Fatalf("candidate query = %#v", executor.execQueries)
	}
	arguments := executor.execArguments[0]
	if len(arguments) != 7 || arguments[0] != "source" || arguments[1] != "42" || arguments[2] != 7 || arguments[3] != 0.7 || arguments[5] != "review" {
		t.Fatalf("candidate arguments = %#v", arguments)
	}
}

func TestLinkResourceBindsOnlyStructuredEpisodeRows(t *testing.T) {
	executor := &identityFoundationExecutor{}
	store := NewPostgresStore(executor)
	if err := store.LinkResource(t.Context(), ResourceLink{SourceKey: "source", VodID: "42", MediaID: 7, Confidence: 0.9, MatchedBy: "weighted_features"}); err != nil {
		t.Fatal(err)
	}
	if len(executor.execQueries) != 2 || !strings.Contains(executor.execQueries[0], "resource_media_links") ||
		!strings.Contains(executor.execQueries[1], "resource_episode_candidates") {
		t.Fatalf("resource identity binding queries = %#v", executor.execQueries)
	}
}

func TestRecordDetailedMatchCandidatePersistsRejectedConflictEvidence(t *testing.T) {
	executor := &identityFoundationExecutor{}
	store := NewPostgresStore(executor)
	reason := `{"confidence":0.55,"hard_conflicts":["season_mismatch"]}`
	if err := store.RecordDetailedMatchCandidate(t.Context(), "source", "42", 7, 0.55, "weighted_features", "rejected", reason); err != nil {
		t.Fatal(err)
	}
	arguments := executor.execArguments[0]
	if len(arguments) != 7 || arguments[5] != "rejected" || arguments[6] != reason || !strings.Contains(executor.execQueries[0], "ELSE EXCLUDED.status") {
		t.Fatalf("detailed candidate = %#v / %s", arguments, executor.execQueries[0])
	}
}

type identityFoundationExecutor struct {
	execQueries   []string
	execArguments [][]any
	rowQueries    []string
	rowArguments  [][]any
	rowValues     []int
}

func (executor *identityFoundationExecutor) Exec(_ context.Context, query string, arguments ...any) (int64, error) {
	executor.execQueries = append(executor.execQueries, query)
	executor.execArguments = append(executor.execArguments, arguments)
	return 1, nil
}

func (*identityFoundationExecutor) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (executor *identityFoundationExecutor) QueryRow(_ context.Context, query string, arguments ...any) database.Row {
	executor.rowQueries = append(executor.rowQueries, query)
	executor.rowArguments = append(executor.rowArguments, arguments)
	value := 0
	if len(executor.rowValues) > 0 {
		value = executor.rowValues[0]
		executor.rowValues = executor.rowValues[1:]
	}
	return identityIntRow(value)
}

type identityIntRow int

func (row identityIntRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return errors.New("unexpected scan destination count")
	}
	pointer, ok := destinations[0].(*int)
	if !ok {
		return errors.New("unexpected scan destination type")
	}
	*pointer = int(row)
	return nil
}
