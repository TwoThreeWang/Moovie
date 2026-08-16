package history

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestLegacyHistoryRoutesAreRemoved(t *testing.T) {
	router, _ := historyTestRouter(t)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/history/sync", nil),
		httptest.NewRequest(http.MethodDelete, "/api/history/1", nil),
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("legacy route %s %s = %d", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestV2RemoveRequiresLoginAndPreservesHTMXBehavior(t *testing.T) {
	router, token := historyTestRouter(t)
	anonymous := httptest.NewRequest(http.MethodDelete, "/api/v2/history/1", nil)
	anonymousRecorder := httptest.NewRecorder()
	router.ServeHTTP(anonymousRecorder, anonymous)
	assertResponse(t, anonymousRecorder, http.StatusUnauthorized, "未登录", false)

	htmx := httptest.NewRequest(http.MethodDelete, "/api/v2/history/1", nil)
	htmx.AddCookie(&http.Cookie{Name: "token", Value: token})
	htmx.Header.Set("HX-Request", "true")
	htmxRecorder := httptest.NewRecorder()
	router.ServeHTTP(htmxRecorder, htmx)
	if htmxRecorder.Code != http.StatusOK || htmxRecorder.Body.Len() != 0 {
		t.Fatalf("HTMX delete status/body = %d/%q", htmxRecorder.Code, htmxRecorder.Body.String())
	}
}

func TestDashboardHistoryPreservesPaginationAndFragments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	base := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	for index := 1; index <= 25; index++ {
		if err := store.Upsert(t.Context(), Record{UserID: 42, VodID: strconv.Itoa(index), DoubanID: "1292052", Title: "影片" + strconv.Itoa(index), Source: "source", Episode: "第01集", WatchedAt: base.Add(time.Duration(index) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(store, "secret").Register(router)
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: 42, Email: "user@example.com", Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")

	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/htmx/dashboard/history", nil))
	if unauthorized.Code != http.StatusOK || unauthorized.Body.String() != "未登录" {
		t.Fatalf("unauthorized dashboard = %d/%q", unauthorized.Code, unauthorized.Body.String())
	}
	firstRequest := httptest.NewRequest(http.MethodGet, "/api/htmx/dashboard/history?page=1", nil)
	firstRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	first := httptest.NewRecorder()
	router.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK || strings.Count(first.Body.String(), `class="movie-card-wrapper history-card-wrapper"`) != 24 || !strings.Contains(first.Body.String(), "page=2") ||
		!strings.Contains(first.Body.String(), "/play/source/25") || strings.Contains(first.Body.String(), "manualSync") {
		t.Fatalf("first dashboard page = %d cards=%d", first.Code, strings.Count(first.Body.String(), `class="movie-card-wrapper history-card-wrapper"`))
	}
	secondRequest := httptest.NewRequest(http.MethodGet, "/api/htmx/dashboard/history?page=2", nil)
	secondRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	second := httptest.NewRecorder()
	router.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusOK || strings.Count(second.Body.String(), `class="movie-card-wrapper history-card-wrapper"`) != 1 || strings.Contains(second.Body.String(), `id="history-grid"`) {
		t.Fatalf("second dashboard page = %d cards=%d body=%s", second.Code, strings.Count(second.Body.String(), `class="movie-card-wrapper history-card-wrapper"`), second.Body.String())
	}
}

func TestRecentHistoryUsesCanonicalEpisodeAcrossSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	base := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	for _, record := range []Record{
		{UserID: 42, MediaID: 9, DoubanID: "1292052", SeasonNumber: 1, EpisodeKey: "S01E03", Episode: "第03集", Source: "slow", VodID: "old", Title: "影片", Poster: "https://image.example/poster.jpg", WatchedAt: base},
		{UserID: 42, MediaID: 9, DoubanID: "1292052", SeasonNumber: 1, EpisodeKey: "S01E03", Episode: "S01E03", Source: "fast", VodID: "new", EntryPage: "watch", Title: "影片", Poster: "https://image.example/poster.jpg", WatchedAt: base.Add(time.Minute)},
	} {
		if err := store.Upsert(t.Context(), record); err != nil {
			t.Fatal(err)
		}
	}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(store, "secret").Register(router)
	token, _ := auth.Sign(auth.Claims{UserID: 42, Email: "user@example.com", Role: "user", Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/api/htmx/history/recent", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.Count(recorder.Body.String(), `class="movie-card-wrapper history-card-wrapper"`) != 1 ||
		!strings.Contains(recorder.Body.String(), `id="history-grid"`) || strings.Contains(recorder.Body.String(), "manualSync") ||
		!strings.Contains(recorder.Body.String(), "/watch/1292052?ep=S01E03") || !strings.Contains(recorder.Body.String(), "source_key=fast") ||
		!strings.Contains(recorder.Body.String(), "vod_id=new") || strings.Contains(recorder.Body.String(), "/play/fast/new") ||
		!strings.Contains(recorder.Body.String(), "/api/proxy/image/") ||
		!strings.Contains(recorder.Body.String(), `onerror="this.onerror=null;this.src='/static/img/placeholder.svg'"`) {
		t.Fatalf("recent history = %d/%s", recorder.Code, recorder.Body.String())
	}
	dashboardRequest := httptest.NewRequest(http.MethodGet, "/api/htmx/dashboard/history?page=1", nil)
	dashboardRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	dashboard := httptest.NewRecorder()
	router.ServeHTTP(dashboard, dashboardRequest)
	if dashboard.Code != http.StatusOK || dashboard.Body.String() != recorder.Body.String() {
		t.Fatalf("dashboard and homepage first-page partials differ: dashboard=%d/%q recent=%q", dashboard.Code, dashboard.Body.String(), recorder.Body.String())
	}
}

func TestHistoryReadsCanonicalPlaybackPositions(t *testing.T) {
	base := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	store := &playbackReadMemoryStore{MemoryStore: NewMemoryStore(), positions: []Record{{
		ID: 9, UserID: 42, MediaID: 7, MediaUnitID: 70, Source: "canonical", VodID: "new",
		Title: "规范进度", Episode: "第03集", SeasonNumber: 1, EpisodeKey: "S01E03", WatchedAt: base,
	}}}
	if err := store.Upsert(t.Context(), Record{UserID: 42, Source: "source-a", VodID: "older", Title: "较早进度", WatchedAt: base}); err != nil {
		t.Fatal(err)
	}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: 42, Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	request := func() string {
		router := gin.New()
		router.HTMLRender = renderer
		NewHandler(store, "secret").Register(router)
		req := httptest.NewRequest(http.MethodGet, "/api/htmx/history/recent", nil)
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
		return recorder.Body.String()
	}
	canonical := request()
	if !strings.Contains(canonical, "规范进度") || strings.Contains(canonical, "旧进度") {
		t.Fatalf("v2 read = %s", canonical)
	}
}

type playbackReadMemoryStore struct {
	*MemoryStore
	positions []Record
}

func (store *playbackReadMemoryStore) ListContinue(_ context.Context, _ int, limit, offset int) ([]Record, error) {
	if offset >= len(store.positions) {
		return []Record{}, nil
	}
	end := len(store.positions)
	if offset+limit < end {
		end = offset + limit
	}
	return append([]Record(nil), store.positions[offset:end]...), nil
}

func historyTestRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	return historyRouterWithStore(t, NewMemoryStore())
}

func historyRouterWithStore(t *testing.T, store Store) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler := NewHandler(store, "secret")
	handler.now = func() time.Time { return time.Date(2026, time.July, 29, 16, 0, 0, 0, time.UTC) }
	router := gin.New()
	handler.Register(router)
	token, err := auth.Sign(auth.Claims{UserID: 42, Email: "user@example.com", Role: "user", Issued: time.Now().Add(-time.Hour).Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return router, token
}

func assertResponse(t *testing.T, recorder *httptest.ResponseRecorder, status int, message string, success bool) map[string]any {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["message"] != message || payload["success"] != success || payload["code"] != float64(status) {
		t.Fatalf("payload = %#v", payload)
	}
	return payload
}
