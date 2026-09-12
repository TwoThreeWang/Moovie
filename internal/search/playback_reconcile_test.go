package search

import (
	"strings"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

// 关联、采集、停用来源均在事务里重算；另一站仍有该集时不能误置灰。
func TestResourceLifecycleMaintainsCanonicalAvailability(t *testing.T) {
	pool := testdb.Pool(t)
	store := NewPostgresStore(pool)
	for _, source := range []string{"a", "b"} {
		if _, err := store.CreateSite(t.Context(), Site{Key: source, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"a", "b"} {
		if err := store.Upsert(t.Context(), VodItem{SourceKey: source, VodId: "42", VodDoubanId: "1292052", VodName: "剧集", TypeName: "国产剧", VodPlayUrl: "第1集$https://video.example/1.m3u8#预告$https://video.example/trailer.m3u8"}); err != nil {
			t.Fatal(err)
		}
	}
	var mediaID, unitID int
	var available bool
	if err := pool.QueryRow(t.Context(), `SELECT media.id,u.id,u.has_resource FROM media JOIN media_units u ON u.media_id=media.id WHERE douban_id='1292052'`).Scan(&mediaID, &unitID, &available); err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("expected available episode")
	}
	if err := store.Upsert(t.Context(), VodItem{SourceKey: "a", VodId: "42", VodPlayUrl: "TC$https://video.example/tc.m3u8"}); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT has_resource FROM media_units WHERE id=$1`, unitID).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if !available {
		t.Fatal("other site still has episode")
	}
	sites, err := store.ListSites(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	site := sites[1]
	site.Enabled = false
	if err := store.UpdateSite(t.Context(), site); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT has_resource FROM media_units WHERE id=$1`, unitID).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available {
		t.Fatal("disabled source kept episode available")
	}
}

// 在线回填只清洗资源、重算单元；历史合并留到显式维护阶段，两次运行不重复已完成记录。
func TestOnlinePlaybackReconcileDefersHistoryUntilMaintenance(t *testing.T) {
	pool := testdb.Pool(t)
	store := NewPostgresStore(pool)
	testdb.Media(t, pool, 7)
	testdb.User(t, pool, 1)
	// 0065 之后新写入的行默认已完成；这里显式回到旧值，代表迁移前留下的存量数据。
	_, err := pool.Exec(t.Context(), `UPDATE media SET playback_reconciled_at=NULL,playback_history_repaired=FALSE WHERE id=7;
 INSERT INTO sites(key,base_url,enabled)VALUES('a','https://a.example',TRUE);
 INSERT INTO vod_items(source_key,vod_id,vod_name,vod_play_url,playback_cleaned,updated_at) VALUES('a','42','电影','720P$https://video.example/a.m3u8#TC$https://video.example/tc.m3u8',FALSE,'2020-01-01');
 INSERT INTO resource_media_links(source_key,vod_id,media_id,confidence,matched_by)VALUES('a','42',7,1,'manual');
 INSERT INTO media_units(id,media_id,unit_type,season_number,episode_key,title)VALUES(500,7,'episode',1,'720P','720P'),(501,7,'episode',1,'HD','HD');
 INSERT INTO playback_positions(user_id,media_id,media_unit_id,episode_key,activity_at,position_seconds)VALUES(1,7,500,'720P','2020-01-01',15),(1,7,501,'HD','2020-01-02',30);`)
	if err != nil {
		t.Fatal(err)
	}
	raw := func() string {
		var value string
		if err := pool.QueryRow(t.Context(), `SELECT vod_play_url FROM vod_items WHERE source_key='a' AND vod_id='42'`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	counts := func() (units, positions int) {
		if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM media_units WHERE media_id=7),(SELECT count(*) FROM playback_positions WHERE user_id=1)`).Scan(&units, &positions); err != nil {
			t.Fatal(err)
		}
		return units, positions
	}

	check, err := store.ReconcilePlayback(t.Context(), PlaybackReconcileOptions{Phase: "resources"})
	if err != nil || check.Checked != 1 || check.Changed != 1 || check.Completed != 0 || !check.Remaining {
		t.Fatalf("检查模式=%+v err=%v", check, err)
	}
	if !strings.Contains(raw(), "tc.m3u8") {
		t.Fatal("检查模式不应写入")
	}

	online, err := store.ReconcilePlayback(t.Context(), PlaybackReconcileOptions{Apply: true})
	if err != nil || online.Changed != 1 || online.Skipped != 0 || online.Remaining {
		t.Fatalf("在线回填=%+v err=%v", online, err)
	}
	if raw() != "720P$https://video.example/a.m3u8" {
		t.Fatalf("清洗结果=%q", raw())
	}
	var available bool
	if err := pool.QueryRow(t.Context(), `SELECT has_resource FROM media_units WHERE media_id=7 AND unit_type='feature'`).Scan(&available); err != nil || !available {
		t.Fatalf("正片可用=%t err=%v", available, err)
	}
	// 在线阶段不动历史：旧版本单元和两条进度都还在。
	if units, positions := counts(); units != 3 || positions != 2 {
		t.Fatalf("在线阶段改动了历史 单元=%d 进度=%d", units, positions)
	}

	again, err := store.ReconcilePlayback(t.Context(), PlaybackReconcileOptions{Apply: true})
	if err != nil || again.Checked != 0 {
		t.Fatalf("重跑=%+v err=%v", again, err)
	}

	if _, err := store.ReconcilePlayback(t.Context(), PlaybackReconcileOptions{Phase: "history", Apply: true}); err == nil {
		t.Fatal("history 写入必须要求 -maintenance")
	}
	history, err := store.ReconcilePlayback(t.Context(), PlaybackReconcileOptions{Phase: "history", Apply: true, Maintenance: true})
	if err != nil || history.Completed != 1 || history.Remaining {
		t.Fatalf("历史阶段=%+v err=%v", history, err)
	}
	if units, positions := counts(); units != 1 || positions != 1 {
		t.Fatalf("历史合并后 单元=%d 进度=%d", units, positions)
	}
	var key string
	var pos, year int
	if err := pool.QueryRow(t.Context(), `SELECT episode_key,position_seconds::int FROM playback_positions WHERE user_id=1`).Scan(&key, &pos); err != nil || key != "feature" || pos != 30 {
		t.Fatalf("进度=%s/%d err=%v", key, pos, err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT extract(year FROM updated_at)::int FROM vod_items WHERE source_key='a' AND vod_id='42'`).Scan(&year); err != nil || year != 2020 {
		t.Fatalf("回填改了资源更新时间 年=%d err=%v", year, err)
	}
}
