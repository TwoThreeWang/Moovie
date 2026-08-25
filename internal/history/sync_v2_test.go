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

func TestSyncV2AppliesUpsertAndDelete(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 9)
	testdb.MediaUnit(t, testdb.Pool(t), 99, 9)
	testdb.User(t, testdb.Pool(t), 42)
	store := NewPostgresStore(testdb.Pool(t))
	now := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)

	upsert := SyncV2Request{DeviceID: "device-test", Operations: []SyncOperation{{
		OperationID: "op-upsert-1", Type: "upsert", MediaID: 9, MediaUnitID: 99,
		Source: "source-a", VodID: "vod-1", Title: "测试影片", Episode: "第01集",
		Position: 60, Duration: 120, OccurredAt: now,
	}}}
	if err := normalizeSyncRequest(&upsert, now); err != nil {
		t.Fatal(err)
	}
	result, err := store.SyncV2(t.Context(), 42, upsert, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Cursor != 0 || len(result.Changes) != 0 {
		t.Fatalf("sync result should be empty: %#v", result)
	}
	records, err := store.ListByUser(t.Context(), 42, 10, 0)
	if err != nil || len(records) != 1 || records[0].Title != "测试影片" {
		t.Fatalf("upsert not applied: records=%+v, err=%v", records, err)
	}

	del := SyncV2Request{DeviceID: "device-test", Operations: []SyncOperation{{
		OperationID: "op-delete-1", Type: "delete", MediaID: 9,
		Episode: "第01集", OccurredAt: now.Add(time.Minute),
	}}}
	if err := normalizeSyncRequest(&del, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncV2(t.Context(), 42, del, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	records, err = store.ListByUser(t.Context(), 42, 10, 0)
	if err != nil || len(records) != 0 {
		t.Fatalf("delete not applied: records=%+v, err=%v", records, err)
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

func TestSyncV2StaleOperationDoesNotOverwriteNewerRecord(t *testing.T) {
	testdb.Media(t, testdb.Pool(t), 9)
	testdb.User(t, testdb.Pool(t), 42)
	store := NewPostgresStore(testdb.Pool(t))
	now := time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC)

	fresh := SyncV2Request{DeviceID: "device-alpha", Operations: []SyncOperation{{
		OperationID: "op-fresh", Type: "upsert", MediaID: 9,
		Source: "slow", VodID: "vod-a", Title: "新记录", Episode: "第03集",
		Position: 30, Duration: 120, OccurredAt: now,
	}}}
	if err := normalizeSyncRequest(&fresh, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncV2(t.Context(), 42, fresh, now); err != nil {
		t.Fatal(err)
	}

	stale := SyncV2Request{DeviceID: "device-beta", Operations: []SyncOperation{{
		OperationID: "op-stale", Type: "upsert", MediaID: 9,
		Source: "backup", VodID: "vod-b", Title: "过期标题", Episode: "S01E03",
		OccurredAt: now.Add(-2 * time.Minute),
	}}}
	if err := normalizeSyncRequest(&stale, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncV2(t.Context(), 42, stale, now); err != nil {
		t.Fatal(err)
	}

	records, err := store.ListByUser(t.Context(), 42, 10, 0)
	if err != nil || len(records) != 1 || records[0].Title != "新记录" {
		t.Fatalf("stale operation should not overwrite: records=%+v, err=%v", records, err)
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
	if future.Cursor != 0 || len(future.Changes) != 0 {
		t.Fatalf("future clock sync should return empty result: %#v", future)
	}
	store := NewPostgresStore(testdb.Pool(t))
	records, err := store.ListByUser(t.Context(), 42, 10, 0)
	if err != nil || len(records) != 1 || records[0].Progress != 100 {
		t.Fatalf("future clock record = %+v, err=%v", records, err)
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

