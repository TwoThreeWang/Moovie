package database

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestEmbeddedMigrationsIncludeCanonicalCutover(t *testing.T) {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	versions := make([]string, 0, len(migrations))
	for _, migration := range migrations {
		versions = append(versions, migration.version)
	}
	expectedVersions := make([]string, 52)
	for index := range expectedVersions {
		expectedVersions[index] = fmt.Sprintf("%04d", index+1)
	}
	if !reflect.DeepEqual(versions, expectedVersions) {
		t.Fatalf("migrations = %+v", migrations)
	}
	upperSQL := ""
	for _, migration := range migrations {
		upperSQL += "\n" + strings.ToUpper(migration.sql)
	}
	for _, required := range []string{"CREATE TABLE SITES", "CREATE TABLE VOD_ITEMS", "CREATE TABLE COPYRIGHT_FILTERS", "CREATE TABLE CATEGORY_FILTERS", "CREATE TABLE SEARCH_LOGS", "CREATE TABLE SITE_STATS", "CREATE TABLE WATCH_HISTORIES", "CREATE TABLE USERS", "CREATE TABLE USER_MOVIES", "CREATE TABLE MOVIES", "CREATE TABLE DOUBAN_SYNC_JOBS", "CREATE TABLE MONTHLY_REPORTS", "CREATE TABLE COMMENT_LIKES", "CREATE TABLE COMMENT_REPLIES", "CREATE TABLE FEEDBACKS", "CREATE TABLE DANMAKUS", "CREATE TABLE IF NOT EXISTS MEDIA_FIELD_SOURCES", "ALTER TABLE VOD_ITEMS ADD COLUMN IF NOT EXISTS RESOURCE_STATUS", "CREATE TABLE IF NOT EXISTS RESOURCE_PLAYBACK_HEALTH", "CREATE TABLE IF NOT EXISTS HISTORY_SYNC_EVENTS", "CREATE TABLE USER_RECOMMENDATION_SNAPSHOTS", "PLAYBACK_ATTEMPT_EVENTS_TRENDING_IDX"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	if !strings.Contains(upperSQL, "'系统告警'") {
		t.Fatal("feedback migration does not allow operations system alerts")
	}
	if !strings.Contains(upperSQL, "ADD COLUMN ATTEMPT_COUNT") {
		t.Fatal("Douban job migration does not track claim attempts")
	}
	// 限流重试必须独立计数，否则退还 attempt 之后就没有兜底上限了。
	if !strings.Contains(upperSQL, "ADD COLUMN THROTTLE_COUNT") {
		t.Fatal("worker queue migration does not track throttled retries separately")
	}
	// 没有尝试时间，回填任务每轮都会重新捞同一批查不到的条目。
	if !strings.Contains(upperSQL, "ADD COLUMN IMDB_LOOKUP_AT") {
		t.Fatal("IMDb backfill migration does not record lookup attempts")
	}
	// 批量源和兜底源的成本差三个数量级，必须各记一列：共用一列的话，
	// 批量源的「查不到」结论无处安放，那批候选就会永远压在队首。
	if !strings.Contains(upperSQL, "ADD COLUMN IMDB_BATCH_LOOKUP_AT") {
		t.Fatal("IMDb lookup stage migration does not separate batch attempts from fallback attempts")
	}
	for _, required := range []string{"MEDIA_IMDB_BATCH_LOOKUP_IDX", "MEDIA_IMDB_FALLBACK_LOOKUP_IDX", "^[0-9]{6,9}$"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("IMDb lookup stage migration missing %q", required)
		}
	}
	for _, required := range []string{"ADD COLUMN IF NOT EXISTS EXTERNAL_TYPE", "(PROVIDER, EXTERNAL_TYPE, EXTERNAL_ID)", "(MEDIA_ID, PROVIDER, EXTERNAL_TYPE)"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("external ID namespace migration missing %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS LEGACY_MEDIA_MAPPINGS", "CREATE TABLE IF NOT EXISTS MEDIA_ALIASES", "CREATE TABLE IF NOT EXISTS MEDIA_UNITS", "ADD COLUMN IF NOT EXISTS MEDIA_UNIT_ID"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("media identity foundation migration missing %q", required)
		}
	}
	if !strings.Contains(upperSQL, "CREATE TABLE IF NOT EXISTS RESOURCE_MATCH_CANDIDATES") {
		t.Fatal("resource match review migration is missing")
	}
	if !strings.Contains(upperSQL, "CREATE TABLE IF NOT EXISTS RESOURCE_MATCH_AUDITS") {
		t.Fatal("resource match audit migration is missing")
	}
	// 建表迁移必须留着（老库跑过），但最终状态是删掉的：0044 负责收尾。
	if !strings.Contains(upperSQL, "DROP TABLE IF EXISTS RESOURCE_MATCH_AUDITS") {
		t.Fatal("resource match audit drop migration is missing")
	}
	for _, required := range []string{"ADD COLUMN IF NOT EXISTS ID BIGSERIAL", "ADD COLUMN IF NOT EXISTS CANDIDATE_ID", "ADD COLUMN IF NOT EXISTS PREVIOUS_MEDIA_ID", "ADD COLUMN IF NOT EXISTS RESOLVED_MEDIA_ID"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("resource match API migration missing %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS RESOURCE_PLAY_LINES", "CREATE TABLE IF NOT EXISTS RESOURCE_EPISODE_CANDIDATES", "UNIQUE (LINE_ID, SEASON_NUMBER, EPISODE_KEY)"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("resource play line migration missing %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS PLAYBACK_POSITIONS", "PLAYBACK_POSITIONS_UNIT_UIDX", "PLAYBACK_POSITIONS_MEDIA_EPISODE_UIDX", "SERVER_VERSION", "DELETED_AT", "LEGACY_PAYLOAD"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("playback position migration missing %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS METADATA_REFRESH_JOBS", "METADATA_REFRESH_JOBS_ACTIVE_UIDX", "ADD COLUMN IF NOT EXISTS CONTENT_HASH", "ADD COLUMN IF NOT EXISTS SEMANTIC_HASH", "ADD COLUMN IF NOT EXISTS COMPLETENESS_SCORE", "ADD COLUMN IF NOT EXISTS MERGE_RULE_VERSION", "ADD COLUMN IF NOT EXISTS EMBEDDING_SEMANTIC_HASH", "LOCKED_BY", "LOCKED_UNTIL"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("metadata refresh migration missing %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS PLAYBACK_ATTEMPT_EVENTS", "UNIQUE (ATTEMPT_ID, EVENT_TYPE)", "CREATE TABLE IF NOT EXISTS PLAYBACK_QUALITY_ROLLUPS", "MEDIA_UNIT_ID", "FIRST_FRAME_TOTAL_MS", "TIMEOUT_COUNT"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("playback quality migration missing %q", required)
		}
	}
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS POPULARITY_SNAPSHOT_RUNS", "CREATE TABLE IF NOT EXISTS POPULARITY_SNAPSHOTS", "SOURCE_RANKS", "CREATE TABLE IF NOT EXISTS RESOURCE_LIFECYCLE_BATCHES", "CREATE TABLE IF NOT EXISTS RESOURCE_LIFECYCLE_BATCH_ITEMS", "ADD COLUMN IF NOT EXISTS COLD_AT"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("popularity and lifecycle migration missing %q", required)
		}
	}
	for _, required := range []string{"ADD COLUMN IF NOT EXISTS CANDIDATE_SESSION_ID", "PLAYBACK_ATTEMPT_EVENTS_SESSION_IDX", "ADD COLUMN IF NOT EXISTS APPLIED_COUNT"} {
		if !strings.Contains(upperSQL, required) {
			t.Fatalf("playback observability migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"TRUNCATE ", "DELETE FROM"} {
		if strings.Contains(upperSQL, forbidden) {
			t.Fatalf("migration contains destructive statement %q", forbidden)
		}
	}
	// 迁移编号在 0031 之后仍会继续增长，因此这里按版本号定位割接迁移，
	// 而不是取最后一个文件——否则每加一条新迁移都会误报。
	const cutoverVersion = "0031"
	allowedTableDrops := map[string]bool{"0036": true, "0044": true, "0047": true, "0048": true, "0050": true}
	cutoverSQL := ""
	for _, migration := range migrations {
		if migration.version == cutoverVersion {
			cutoverSQL = strings.ToUpper(migration.sql)
			continue
		}
		// 删表必须是刻意的：0036 合并任务队列，0044/0047/0048/0050 删除已退役结构，
		// 其余迁移一律不许出现 DROP TABLE，防止误删。
		if !allowedTableDrops[migration.version] && strings.Contains(strings.ToUpper(migration.sql), "DROP TABLE") {
			t.Fatalf("only explicit cutover migrations may drop retired tables: version=%s", migration.version)
		}
	}
	if cutoverSQL == "" {
		t.Fatalf("canonical cutover migration %s is missing", cutoverVersion)
	}
	for _, required := range []string{
		"DATA-CANONICAL-CUTOVER-READY",
		"USER_MOVIES WHERE MEDIA_ID IS NULL",
		"DROP TABLE IF EXISTS LEGACY_MEDIA_MAPPINGS",
		"DROP TABLE IF EXISTS RESOURCE_PLAYBACK_HEALTH",
		"DROP TABLE IF EXISTS RESOURCE_EPISODES",
		"DROP TABLE IF EXISTS WATCH_HISTORIES",
		"DROP TABLE IF EXISTS MOVIES",
		"ALTER TABLE VOD_ITEMS DROP COLUMN IF EXISTS AVG_SPEED_MS",
		"ALTER TABLE PLAYBACK_POSITIONS DROP COLUMN IF EXISTS LEGACY_PAYLOAD",
	} {
		if !strings.Contains(cutoverSQL, required) {
			t.Fatalf("canonical cutover missing %q", required)
		}
	}
	queueCutoverSQL := ""
	for _, migration := range migrations {
		if migration.version == "0036" {
			queueCutoverSQL = strings.ToUpper(migration.sql)
		}
	}
	for _, required := range []string{"DATA-WORKER-QUEUE-CUTOVER-READY", "CREATE TABLE WORKER_JOBS", "DROP TABLE METADATA_REFRESH_JOBS", "DROP TABLE DOUBAN_SYNC_JOBS"} {
		if !strings.Contains(queueCutoverSQL, required) {
			t.Fatalf("worker queue cutover missing %q", required)
		}
	}
}

func TestMigrateAppliesPendingMigrationAndCommits(t *testing.T) {
	transaction := &fakeTransaction{applied: false}
	if err := Migrate(context.Background(), fakeBeginner{transaction: transaction}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	if !transaction.committed || transaction.rolledBackBeforeCommit {
		t.Fatalf("transaction lifecycle: committed=%v prematureRollback=%v", transaction.committed, transaction.rolledBackBeforeCommit)
	}
	joined := strings.Join(transaction.execQueries, "\n")
	for _, expected := range []string{"pg_advisory_xact_lock", "schema_migrations", "CREATE TABLE sites", "INSERT INTO schema_migrations"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("migration execution missing %q: %s", expected, joined)
		}
	}
}

func TestMigrateSkipsAlreadyAppliedVersion(t *testing.T) {
	transaction := &fakeTransaction{applied: true}
	if err := Migrate(context.Background(), fakeBeginner{transaction: transaction}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	joined := strings.Join(transaction.execQueries, "\n")
	if strings.Contains(joined, "CREATE TABLE sites") || strings.Contains(joined, "INSERT INTO schema_migrations") {
		t.Fatalf("applied migration ran again: %s", joined)
	}
}

func TestMigrateThroughStopsBeforeFinalization(t *testing.T) {
	transaction := &fakeTransaction{}
	if err := MigrateThrough(context.Background(), fakeBeginner{transaction: transaction}, "0030"); err != nil {
		t.Fatalf("MigrateThrough() error = %v", err)
	}
	joined := strings.Join(transaction.execQueries, "\n")
	if !strings.Contains(joined, "ALTER TABLE user_movies ADD COLUMN IF NOT EXISTS media_id") {
		t.Fatal("preparation migration 0030 did not run")
	}
	if strings.Contains(joined, "DROP TABLE IF EXISTS watch_histories") {
		t.Fatal("finalization migration 0031 ran before data verification")
	}
}

type fakeBeginner struct{ transaction *fakeTransaction }

func (beginner fakeBeginner) Begin(context.Context) (Transaction, error) {
	return beginner.transaction, nil
}

type fakeTransaction struct {
	applied                bool
	execQueries            []string
	committed              bool
	rolledBackBeforeCommit bool
}

func (transaction *fakeTransaction) Query(context.Context, string, ...any) (Rows, error) {
	return nil, fmt.Errorf("unexpected Query call")
}

func (transaction *fakeTransaction) QueryRow(_ context.Context, _ string, _ ...any) Row {
	return boolRow{value: transaction.applied}
}

func (transaction *fakeTransaction) Exec(_ context.Context, query string, _ ...any) (int64, error) {
	transaction.execQueries = append(transaction.execQueries, query)
	return 0, nil
}

func (transaction *fakeTransaction) Commit(context.Context) error {
	transaction.committed = true
	return nil
}

func (transaction *fakeTransaction) Rollback(context.Context) error {
	if !transaction.committed {
		transaction.rolledBackBeforeCommit = true
	}
	return nil
}

type boolRow struct{ value bool }

func (row boolRow) Scan(destinations ...any) error {
	if len(destinations) != 1 {
		return fmt.Errorf("destinations = %d", len(destinations))
	}
	reflect.ValueOf(destinations[0]).Elem().SetBool(row.value)
	return nil
}
