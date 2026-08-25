package history

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestTodayUpdatesUsesFirstScheduledEpisodeAndContinuesHistoryEntry(t *testing.T) {
	if got := historyContinueURL(Record{EntryPage: "watch", DoubanID: "1292052", Episode: "第28集", Source: "source", VodID: "42"}); got != "/watch/1292052?ep=%E7%AC%AC28%E9%9B%86&source_key=source&vod_id=42" {
		t.Fatalf("watch continue URL = %s", got)
	}
	if got := historyContinueURL(Record{EntryPage: "play", DoubanID: "1292052", Episode: "第28集", Source: "source", VodID: "42"}); got != "/play/source/42?ep=%E7%AC%AC28%E9%9B%86&douban_id=1292052" {
		t.Fatalf("play continue URL = %s", got)
	}

	units := make([]mediaidentity.MediaUnit, 0, 5)
	for episode := 29; episode <= 33; episode++ {
		units = append(units, mediaidentity.MediaUnit{
			MediaID: 9, SeasonNumber: 1, EpisodeNumber: episode,
			EpisodeKey: mediaidentity.EpisodeLabel(1, episode),
		})
	}

	playable := renderTodayUpdates(t, units, map[string][]mediaidentity.ResourceCandidate{
		"S01E29": {{Episode: mediaidentity.Episode{SourceKey: "source-a", VodID: "42"}}},
	})
	if !strings.Contains(playable, "更新至 S01E29") || strings.Contains(playable, "S01E33") ||
		!strings.Contains(playable, `/watch/1292052?ep=%E7%AC%AC28%E9%9B%86&amp;source_key=history-source&amp;vod_id=history-vod`) ||
		strings.Contains(playable, "资源待同步") {
		t.Fatalf("playable today update = %s", playable)
	}

	pending := renderTodayUpdates(t, units, nil)
	if !strings.Contains(pending, "更新至 S01E29") ||
		!strings.Contains(pending, `/watch/1292052?ep=%E7%AC%AC28%E9%9B%86&amp;source_key=history-source&amp;vod_id=history-vod`) ||
		!strings.Contains(pending, "资源待同步") {
		t.Fatalf("pending today update = %s", pending)
	}
}

func renderTodayUpdates(t *testing.T, units []mediaidentity.MediaUnit, candidates map[string][]mediaidentity.ResourceCandidate) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(todayUpdateStore{records: []Record{{
		UserID: 42, MediaID: 9, DoubanID: "1292052", Title: "重器", Episode: "第28集",
		Source: "history-source", VodID: "history-vod", EntryPage: "watch",
	}}}, "secret", WithTodayUpdateReader(todayUpdateReader{units: units}, "Asia/Shanghai"), WithEpisodeReader(todayEpisodeReader{candidates: candidates}))
	handler.now = func() time.Time { return time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC) }
	router := gin.New()
	router.HTMLRender = renderer
	handler.Register(router)
	token, err := auth.Sign(auth.Claims{UserID: 42, Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/htmx/history/today-updates", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("today updates status/body = %d/%s", response.Code, response.Body.String())
	}
	return response.Body.String()
}

type todayUpdateStore struct {
	Store
	records []Record
}

func (store todayUpdateStore) ListContinue(context.Context, int, int, int) ([]Record, error) {
	return store.records, nil
}

type todayUpdateReader struct{ units []mediaidentity.MediaUnit }

func (reader todayUpdateReader) ListDailyUpdatesForMedia(context.Context, []int, time.Time) ([]mediaidentity.MediaUnit, error) {
	return reader.units, nil
}

type todayEpisodeReader struct {
	candidates map[string][]mediaidentity.ResourceCandidate
}

func (reader todayEpisodeReader) ListResourceCandidates(_ context.Context, _ int, _ int, episodeKey string) ([]mediaidentity.ResourceCandidate, error) {
	return reader.candidates[episodeKey], nil
}

func (todayEpisodeReader) ListAllEpisodes(context.Context, int) ([]mediaidentity.EpisodeInfo, error) {
	return nil, nil
}
