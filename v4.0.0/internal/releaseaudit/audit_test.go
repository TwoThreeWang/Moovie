package releaseaudit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAuditClassifiesFailuresWarningsAndObservations(t *testing.T) {
	database := &fakeQuerier{values: make(map[string]int64)}
	for _, spec := range allSpecs() {
		database.values[spec.name] = 0
	}
	database.values["cold_without_timestamp"] = 2
	database.values["expired_unapplied_lifecycle_batch"] = 3
	database.values["media_total"] = 4500
	summary, err := Audit(context.Background(), database, Options{RequirePopularity: true})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Failed != 1 || summary.Warnings != 1 || len(summary.Observations) != len(observationSpecs()) {
		t.Fatalf("summary = %+v", summary)
	}
	if got := resultByName(summary.Observations, "media_total"); got.Value != 4500 || got.Status != "observed" {
		t.Fatalf("media observation = %+v", got)
	}
}

func TestAuditStopsCleanlyWhenLatestMigrationIsMissing(t *testing.T) {
	database := &fakeQuerier{values: map[string]int64{"migration_0036_missing": 1}}
	summary, err := Audit(context.Background(), database, Options{})
	if err != nil || summary.Failed != 1 || len(summary.Checks) != 1 || len(database.queries) != 1 {
		t.Fatalf("summary/error/queries = %+v/%v/%d", summary, err, len(database.queries))
	}
}

func TestPopularityChecksUseRequiredSourcesAndAge(t *testing.T) {
	options := normalizeOptions(Options{RequirePopularity: true, RequiredPopularitySources: []string{" TMDB ", "douban", "tmdb"}, MaxPopularityAge: 90 * time.Minute})
	specs := popularityChecks(options)
	if len(specs) != 4 || len(options.RequiredPopularitySources) != 2 {
		t.Fatalf("options/specs = %+v/%d", options, len(specs))
	}
	if specs[1].arguments[0] != int64(5400) {
		t.Fatalf("max age argument = %#v", specs[1].arguments)
	}
	if !strings.Contains(specs[2].query, "source_status ? source.name") {
		t.Fatalf("source check query = %s", specs[2].query)
	}
	if !strings.Contains(specs[3].query, "item_count <> 50") {
		t.Fatalf("snapshot size check query = %s", specs[3].query)
	}
}

func TestBaseChecksCoverFinalSchemaIdentityQualityAndCooling(t *testing.T) {
	seen := make(map[string]bool)
	for _, spec := range baseChecks() {
		if spec.name == "" || spec.query == "" || seen[spec.name] {
			t.Fatalf("invalid check spec: %+v", spec)
		}
		seen[spec.name] = true
	}
	for _, required := range []string{"migration_0036_missing", "compatibility_table_present", "compatibility_column_present",
		"user_movie_without_media", "resource_link_orphan", "duplicate_active_playback_position", "playback_quality_invalid_total",
		"wrong_unit_failover_session", "cold_without_timestamp", "lifecycle_batch_item_mismatch", "applied_lifecycle_count_missing"} {
		if !seen[required] {
			t.Fatalf("missing release check %q", required)
		}
	}
}

type fakeQuerier struct {
	values  map[string]int64
	queries []string
}

func (fake *fakeQuerier) QueryRow(_ context.Context, query string, _ ...any) Row {
	fake.queries = append(fake.queries, query)
	for _, spec := range allSpecs() {
		if spec.query == query {
			return fakeRow{value: fake.values[spec.name]}
		}
	}
	return fakeRow{err: errors.New("unknown query")}
}

func allSpecs() []checkSpec {
	specs := append([]checkSpec(nil), baseChecks()...)
	specs = append(specs, popularityChecks(normalizeOptions(Options{}))...)
	return append(specs, observationSpecs()...)
}

type fakeRow struct {
	value int64
	err   error
}

func (row fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	*(destinations[0].(*int64)) = row.value
	return nil
}

func resultByName(results []Result, name string) Result {
	for _, result := range results {
		if result.Name == name {
			return result
		}
	}
	return Result{}
}
