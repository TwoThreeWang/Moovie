package history

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestSyncV2BootstrapsExistingServerRecords(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 9)
	testdb.MediaUnit(t, testdb.Pool(t), 99, 9)
	testdb.User(t, testdb.Pool(t), 42)
	store := NewPostgresStore(testdb.Pool(t))
	watchedAt := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	if err := store.Upsert(t.Context(), Record{UserID: 42, MediaID: 9, Source: "source-a", VodID: "vod",
		Title: "旧云端记录", Episode: "第01集", SeasonNumber: 1, EpisodeKey: "S01E01", WatchedAt: watchedAt}); err != nil {
		t.Fatal(err)
	}
	request := SyncV2Request{DeviceID: "device-bootstrap"}
	if err := normalizeSyncRequest(&request, watchedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	first, err := store.SyncV2(t.Context(), 42, request, watchedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if first.Cursor != 1 || len(first.Changes) != 1 || first.Changes[0].OperationID != "bootstrap-position-1" {
		t.Fatalf("bootstrap = %#v", first)
	}
	second, err := store.SyncV2(t.Context(), 42, SyncV2Request{DeviceID: "device-bootstrap", Cursor: first.Cursor}, watchedAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if second.Cursor != first.Cursor || len(second.Changes) != 0 {
		t.Fatalf("repeated bootstrap = %#v", second)
	}
}

func TestBrowserHistoryClientUsesOnlyScopedCursorOutbox(t *testing.T) {
	app, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "js", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	player, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "js", "player.js"))
	if err != nil {
		t.Fatal(err)
	}
	base, err := os.ReadFile(filepath.Join("..", "..", "web", "templates", "layouts", "base.html"))
	if err != nil {
		t.Fatal(err)
	}
	watch, err := os.ReadFile(filepath.Join("..", "..", "web", "templates", "pages", "watch.html"))
	if err != nil {
		t.Fatal(err)
	}
	play, err := os.ReadFile(filepath.Join("..", "..", "web", "templates", "pages", "play.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"/api/v2/history/sync", "moovie_history_cursor_v2", "moovie_history_outbox_v2", "historyUserScope()", "keepalive: true"} {
		if !strings.Contains(string(app), expected) {
			t.Fatalf("app.js missing %q", expected)
		}
	}
	if strings.Contains(string(app), "syncHistoryLegacy") || strings.Contains(string(app), "/api/history/sync") {
		t.Fatal("app.js still contains the retired history protocol")
	}
	if !strings.Contains(string(player), "window.queueHistoryUpsert(item)") {
		t.Fatal("player progress does not enter the v2 outbox")
	}
	if !strings.Contains(string(player), "media_unit_id: options.media_unit_id || 0") {
		t.Fatal("player progress does not retain the canonical media unit identity")
	}
	if !strings.Contains(string(player), "entry_page: options.entryPage === 'watch' ? 'watch' : 'play'") ||
		!strings.Contains(string(app), "entry_page: item.entry_page === 'watch' ? 'watch' : 'play'") ||
		!strings.Contains(string(watch), "entryPage: 'watch'") || !strings.Contains(string(play), "entryPage: 'play'") {
		t.Fatal("player progress does not preserve whether playback started on play or watch")
	}
	if !strings.Contains(string(watch), "document.addEventListener('DOMContentLoaded', function()") {
		t.Fatal("watch player can start before the shared history outbox is ready")
	}
	if strings.Contains(string(player), "if (!video.paused && lastPlaybackTick") || !strings.Contains(string(player), "var playbackVideo = art.video;") {
		t.Fatal("player timeupdate must read the current Artplayer video instead of a ready-handler local")
	}
	if !strings.Contains(string(base), `data-user-id="{{ if .UserInfo }}{{ .UserInfo.ID }}{{ end }}"`) {
		t.Fatal("history cursor is not scoped by the authenticated user")
	}
}

func TestSyncV2UsesMonotonicCursorIdempotencyConflictsAndTombstones(t *testing.T) {
	router, token := historyTestRouter(t)
	now := time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC)

	first := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-alpha", Operations: []SyncOperation{{
		OperationID: "operation-0001", DeviceSeq: 1, Type: "upsert", MediaID: 9,
		Source: "slow", VodID: "vod-a", Title: "测试影片", Episode: "第03集",
		Position: 30, Duration: 120, OccurredAt: now.Add(-time.Minute),
	}}})
	if first.Cursor != 1 || len(first.Changes) != 1 || len(first.Conflicts) != 0 {
		t.Fatalf("first sync = %#v", first)
	}
	if record := first.Changes[0].Record; record == nil || record.MediaID != 9 || record.EpisodeKey != "S01E03" || record.Progress != 25 || record.EntryPage != "play" {
		t.Fatalf("first record = %#v", record)
	}

	// 重试同一个 operation 绝不能再次创建事件或历史行。
	retry := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-alpha", Operations: []SyncOperation{{
		OperationID: "operation-0001", DeviceSeq: 1, Type: "upsert", MediaID: 9,
		Source: "slow", VodID: "vod-a", Title: "重复请求", Episode: "第03集",
		OccurredAt: now,
	}}})
	if retry.Cursor != 1 || len(retry.Changes) != 1 || retry.Changes[0].Record.Title != "测试影片" {
		t.Fatalf("idempotent retry = %#v", retry)
	}

	stale := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-beta", Cursor: 1, Operations: []SyncOperation{{
		OperationID: "operation-0002", DeviceSeq: 1, Type: "upsert", MediaID: 9,
		Source: "backup", VodID: "vod-b", Title: "过期设备标题", Episode: "S01E03",
		OccurredAt: now.Add(-2 * time.Minute),
	}}})
	if stale.Cursor != 2 || len(stale.Changes) != 0 || len(stale.Conflicts) != 1 || stale.Conflicts[0].Reason != "server_record_is_newer" {
		t.Fatalf("stale sync = %#v", stale)
	}
	if stale.Conflicts[0].Current == nil || stale.Conflicts[0].Current.Title != "测试影片" {
		t.Fatalf("stale current = %#v", stale.Conflicts[0].Current)
	}

	deleted := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-alpha", Cursor: 2, Operations: []SyncOperation{{
		OperationID: "operation-0003", DeviceSeq: 2, Type: "delete", MediaID: 9,
		Episode: "第03集", OccurredAt: now,
	}}})
	if deleted.Cursor != 3 || len(deleted.Changes) != 1 || deleted.Changes[0].Type != "delete" {
		t.Fatalf("delete sync = %#v", deleted)
	}

	// 新设备可以重放只追加事件流，包括删除标记和冲突说明，且不依赖浏览器时间戳。
	pulled := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-gamma"})
	if pulled.Cursor != 3 || len(pulled.Changes) != 2 || len(pulled.Conflicts) != 1 || pulled.Changes[1].Type != "delete" {
		t.Fatalf("full pull = %#v", pulled)
	}

	// 如果恢复后的数据库游标低于浏览器游标，协议必须明确重置并重放，不能静默丢数据。
	restored := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-gamma", Cursor: 99})
	if !restored.RequiresFullSync || restored.Cursor != 3 || len(restored.Changes) != 2 || len(restored.Conflicts) != 1 {
		t.Fatalf("restored database replay = %#v", restored)
	}
}

func TestSyncV2SourceSwitchKeepsOneCanonicalUnitRecord(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 9)
	testdb.MediaUnit(t, testdb.Pool(t), 99, 9)
	testdb.User(t, testdb.Pool(t), 42)
	store := NewPostgresStore(testdb.Pool(t))
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	first := SyncV2Request{DeviceID: "device-source-a", Operations: []SyncOperation{{
		OperationID: "operation-source-a", Type: "upsert", MediaID: 9, MediaUnitID: 99,
		Source: "slow", VodID: "vod-a", Episode: "第03集", EntryPage: "play", OccurredAt: now,
	}}}
	if err := normalizeSyncRequest(&first, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncV2(t.Context(), 42, first, now); err != nil {
		t.Fatal(err)
	}
	second := SyncV2Request{DeviceID: "device-source-b", Operations: []SyncOperation{{
		OperationID: "operation-source-b", Type: "upsert", MediaID: 9, MediaUnitID: 99,
		Source: "fast", VodID: "vod-b", Episode: "S01E03", EntryPage: "watch", OccurredAt: now.Add(time.Minute),
	}}}
	if err := normalizeSyncRequest(&second, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncV2(t.Context(), 42, second, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListByUser(t.Context(), 42, 10, 0)
	if err != nil || len(records) != 1 || records[0].MediaUnitID != 99 || records[0].Source != "fast" || records[0].EntryPage != "watch" {
		t.Fatalf("source-switched records = %+v, error=%v", records, err)
	}
}

func TestSyncV2RejectsInvalidBatchAndClampsFutureClock(t *testing.T) {
	router, token := historyTestRouter(t)
	invalidEntry := SyncV2Request{DeviceID: "device-entry", Operations: []SyncOperation{{
		OperationID: "operation-entry", Type: "upsert", Source: "source", VodID: "vod",
		Episode: "正片", EntryPage: "detail", OccurredAt: time.Now(),
	}}}
	if err := normalizeSyncRequest(&invalidEntry, time.Now()); err == nil {
		t.Fatal("invalid entry_page was accepted")
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/v2/history/sync", bytes.NewBufferString(`{"device_id":"short","cursor":-1,"operations":[]}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.AddCookie(&http.Cookie{Name: "token", Value: token})
	invalidRecorder := httptest.NewRecorder()
	router.ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid status/body = %d/%s", invalidRecorder.Code, invalidRecorder.Body.String())
	}

	future := syncV2Request(t, router, token, SyncV2Request{DeviceID: "device-clock", Operations: []SyncOperation{{
		OperationID: "operation-clock", Type: "complete", Source: "source", VodID: "vod",
		Episode: "正片", Duration: 120, OccurredAt: time.Date(2036, time.July, 29, 16, 0, 0, 0, time.UTC),
	}}})
	if len(future.Changes) != 1 || future.Changes[0].Record == nil || future.Changes[0].Record.Progress != 100 ||
		!future.Changes[0].Record.UpdatedAt.Equal(time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("future clock sync = %#v", future)
	}
}

func TestSyncV2RequiresAuthenticationBeforeParsingBody(t *testing.T) {
	router, _ := historyTestRouter(t)
	request := httptest.NewRequest(http.MethodPost, "/api/v2/history/sync", bytes.NewBufferString(`not-json`))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func syncV2Request(t *testing.T, router http.Handler, token string, request SyncV2Request) SyncV2Result {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/v2/history/sync", bytes.NewReader(encoded))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("sync status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	var result SyncV2Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

// 保留期清理删掉的是账本，不是进度：过期事件要删干净，没过期的不许动，
// 而被删掉那条进度必须靠 bootstrap 自动补回来——否则老设备就永远收不到它了。
func TestExpiredSyncEventsAreDeletedAndBootstrappedAgain(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 9)
	testdb.User(t, testdb.Pool(t), 42)
	store := NewPostgresStore(testdb.Pool(t))
	watchedAt := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	for _, episode := range []string{"S01E01", "S01E02"} {
		if err := store.Upsert(t.Context(), Record{UserID: 42, MediaID: 9, Source: "source-a", VodID: "vod",
			Title: "旧云端记录", Episode: episode, SeasonNumber: 1, EpisodeKey: episode, WatchedAt: watchedAt}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.SyncV2(t.Context(), 42, SyncV2Request{DeviceID: "device-retention"}, watchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Changes) != 2 {
		t.Fatalf("bootstrap changes = %d, want 2", len(first.Changes))
	}

	// 只把第一条事件挪到 40 天前，另一条保持新鲜。
	stale := first.Changes[0].Version
	if _, err := testdb.Pool(t).Exec(t.Context(),
		`UPDATE history_sync_events SET created_at = NOW() - INTERVAL '40 days' WHERE version = $1`, stale); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteExpiredSyncEvents(t.Context(), time.Now().AddDate(0, 0, -30), 100)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1（没过期的那条不该被删）", deleted)
	}

	// 账本少了一条，但进度还在：下次同步必须把它补回来发给客户端。
	second, err := store.SyncV2(t.Context(), 42, SyncV2Request{DeviceID: "device-retention", Cursor: first.Cursor}, watchedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changes) != 1 || second.Changes[0].OperationID != first.Changes[0].OperationID {
		t.Fatalf("被清掉的进度没有补回来：%#v", second.Changes)
	}
}
