package operations

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// 该测试默认跳过；最终结构演练时传入隔离库 DSN，可确认整条指标 SQL 不依赖已删除表。
func TestMetricsSnapshotAgainstConfiguredPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("MOOVIE_TEST_DATABASE_DSN"))
	if envPath := strings.TrimSpace(os.Getenv("MOOVIE_TEST_ENV")); dsn == "" && envPath != "" {
		cfg, err := platformconfig.DatabaseConfigFromDotEnv(envPath)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSpace(os.Getenv("MOOVIE_TEST_DATABASE_NAME"))
		if !strings.HasPrefix(name, "moovie_v2_cutover_test_") {
			t.Fatalf("隔离测试数据库名必须以 moovie_v2_cutover_test_ 开头，当前为 %q", name)
		}
		cfg.Name = name
		dsn = cfg.DSN()
	}
	if dsn == "" {
		t.Skip("未设置 MOOVIE_TEST_DATABASE_DSN 或 MOOVIE_TEST_ENV")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, dsn, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	snapshot, err := NewMetricsStore(pool).Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.GeneratedAt == "" || snapshot.History.Total < snapshot.History.Active || snapshot.History.Total < snapshot.History.WithMedia {
		t.Fatalf("invalid snapshot = %+v", snapshot)
	}
}

func TestMetricsSnapshotDecodesOperationalDomains(t *testing.T) {
	payload := []byte(`{"generated_at":"2026-08-04T12:00:00.000Z","window_hours":24,"media":{"total":7,"completeness_low":1,"completeness_medium":2,"completeness_high":4},"matches":{"exact":3,"automatic":1,"review":2,"conflict":0,"unmatched_resources":5},"search":{"ok":10,"empty":2,"timeout":1,"error":0},"history":{"total":8,"active":7,"with_media":6,"resource_only":2,"tombstones":1,"sync_events":4},"playback":{"attempts":10,"first_frames":9,"played_10s":8,"fatal_errors":1,"source_switches":2,"successful_switches":1,"wrong_unit_sessions":0,"first_frame_rate":90,"played_10s_rate":80,"switch_success_rate":50,"startup_p50_ms":300,"startup_p90_ms":900},"refresh":{"due_media":2,"pending":1,"running":1,"failed":0,"oldest_pending_seconds":30,"provider_success":4,"provider_failure":1,"provider_unchanged":2},"resources":{"active":12,"cold":3,"removed":1,"broken":2,"preview_batches":1,"applied_batches":1,"dry_run_apply_delta":0},"popularity":{"movie":{"item_count":40,"age_seconds":60,"expires_in_seconds":3540,"sources":{"douban":30,"tmdb":20,"activity":10}}}}`)
	database := &metricsDatabase{row: metricsRow{payload: payload}}
	store := NewMetricsStore(database)
	snapshot, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Media.Total != 7 || snapshot.Playback.PlayedTenSecondsRate != 80 || snapshot.Playback.WrongUnitSessions != 0 || snapshot.Popularity["movie"].Sources["tmdb"] != 20 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if _, err := store.Snapshot(context.Background()); err != nil || database.queries != 1 {
		t.Fatalf("cached snapshot queries/error = %d/%v", database.queries, err)
	}
}

func TestMetricsQueryCorrelatesFailoverAndDoesNotSelectSensitiveURLs(t *testing.T) {
	for _, required := range []string{"candidate_session_id", "source_switched", "played_10s", "COUNT(DISTINCT media_unit_id) > 1", "dry_run_apply_delta", "latest_popularity"} {
		if !strings.Contains(metricsSnapshotSQL, required) {
			t.Fatalf("metrics query missing %q", required)
		}
	}
	for _, forbidden := range []string{"play_url", "vod_play_url", "failure_reason"} {
		if strings.Contains(metricsSnapshotSQL, forbidden) {
			t.Fatalf("metrics query exposes %q", forbidden)
		}
	}
}

type metricsDatabase struct {
	row     database.Row
	queries int
}

func (*metricsDatabase) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, nil
}
func (fake *metricsDatabase) QueryRow(context.Context, string, ...any) database.Row {
	fake.queries++
	return fake.row
}
func (*metricsDatabase) Exec(context.Context, string, ...any) (int64, error) { return 0, nil }

type metricsRow struct{ payload []byte }

func (row metricsRow) Scan(destinations ...any) error {
	*(destinations[0].(*[]byte)) = row.payload
	return nil
}
