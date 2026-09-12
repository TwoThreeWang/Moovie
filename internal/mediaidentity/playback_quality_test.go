package mediaidentity

import (
	"sync"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
)

// 同一尝试并发重传只记一次；新播放列表不能接受旧页面的结果。
func TestPlaybackResultsAreAtomicAndVersionBound(t *testing.T) {
	pool := testdb.Pool(t)
	raw := "正片$https://video.example/main.m3u8"
	_, err := pool.Exec(t.Context(), `INSERT INTO sites(key,base_url,enabled) VALUES('source','https://source.example',TRUE)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `INSERT INTO vod_items(source_key,vod_id,vod_name,vod_play_url,updated_at) VALUES('source','42','影片',$1,'2020-01-01')`, raw)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(pool)
	event := PlaybackAttemptEvent{AttemptID: "attempt-123456", SourceKey: "source", VodID: "42", EventType: "success", PlaybackVersion: playurl.Version(raw), ElapsedMs: 800}
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.RecordPlaybackEvent(t.Context(), event); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var successes, failures, total, results int
	var updated time.Time
	err = pool.QueryRow(t.Context(), `SELECT success_count,failure_count,total_load_ms,updated_at,(SELECT count(*) FROM playback_results) FROM vod_items WHERE source_key='source' AND vod_id='42'`).Scan(&successes, &failures, &total, &updated, &results)
	if err != nil {
		t.Fatal(err)
	}
	if successes != 1 || failures != 0 || total != 800 || results != 1 || updated.Year() != 2020 {
		t.Fatalf("stats=%d/%d/%d results=%d updated=%v", successes, failures, total, results, updated)
	}
	event.EventType = "failure"
	if accepted, err := store.RecordPlaybackEvent(t.Context(), event); err != nil || accepted {
		t.Fatalf("second terminal=%v/%v", accepted, err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE vod_items SET vod_play_url='正片$https://video.example/new.m3u8',success_count=0,total_load_ms=0 WHERE vod_id='42'`); err != nil {
		t.Fatal(err)
	}
	event.AttemptID = "attempt-stale"
	if accepted, err := store.RecordPlaybackEvent(t.Context(), event); err != nil || accepted {
		t.Fatalf("stale=%v/%v", accepted, err)
	}
	event.PlaybackVersion = playurl.Version("正片$https://video.example/new.m3u8")
	if accepted, err := store.RecordPlaybackEvent(t.Context(), event); err != nil || !accepted {
		t.Fatalf("failure=%v/%v", accepted, err)
	}
}

func TestRecordPlaybackEventRejectsUnboundIdentity(t *testing.T) {
	store := NewPostgresStore(&identityFoundationExecutor{})
	if _, err := store.RecordPlaybackEvent(t.Context(), PlaybackAttemptEvent{AttemptID: "short"}); err == nil {
		t.Fatal("invalid result accepted")
	}
}
