package playback

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

type staticPopularProvider struct {
	subjects []PopularSubject
	err      error
}

func (provider staticPopularProvider) Popular(context.Context, string) ([]PopularSubject, error) {
	return provider.subjects, provider.err
}

type detailCrawlerFunc func(context.Context, string, string, string) (*search.VodItem, error)

func (function detailCrawlerFunc) GetDetail(ctx context.Context, baseURL, vodID, sourceKey string) (*search.VodItem, error) {
	return function(ctx, baseURL, vodID, sourceKey)
}

type copyrightCheckerFunc func(context.Context, string) (bool, string)

func (function copyrightCheckerFunc) IsCopyrightRestricted(ctx context.Context, title string) (bool, string) {
	return function(ctx, title)
}

type userMovieStoreFunc func(context.Context, int, string, string) (bool, error)

func (function userMovieStoreFunc) IsMarked(ctx context.Context, userID int, movieID, status string) (bool, error) {
	return function(ctx, userID, movieID, status)
}

func TestBuildEpisodeSourcesPreservesLineSelection(t *testing.T) {
	sources := buildEpisodeSources([]SourceCandidate{
		{SourceKey: "source", VodID: "42", LineLabel: "备用源 B", EpisodeLabel: "第01集"},
		{SourceKey: "source", VodID: "43", EpisodeLabel: "第01集"},
	}, "", "", "", "", "1292052")
	if len(sources) != 2 || !strings.Contains(sources[0].PlayLink, "source=%E5%A4%87%E7%94%A8%E6%BA%90+B") {
		t.Fatalf("line source link = %+v", sources)
	}
	if strings.Contains(sources[1].PlayLink, "source=") {
		t.Fatalf("empty line label added a source parameter: %s", sources[1].PlayLink)
	}
}

type episodeReaderFunc func(context.Context, int, int, string) ([]mediaidentity.ResourceCandidate, error)

func (function episodeReaderFunc) ListResourceCandidates(ctx context.Context, mediaID, season int, episodeKey string) ([]mediaidentity.ResourceCandidate, error) {
	return function(ctx, mediaID, season, episodeKey)
}

func (episodeReaderFunc) ListAllEpisodes(context.Context, int) ([]mediaidentity.EpisodeInfo, error) {
	return []mediaidentity.EpisodeInfo{}, nil
}

type combinedEpisodeReader struct {
	byEpisode episodeReaderFunc
	byUnit    func(context.Context, int) ([]mediaidentity.ResourceCandidate, error)
	all       func(context.Context, int) ([]mediaidentity.EpisodeInfo, error)
}

func (reader combinedEpisodeReader) ListResourceCandidates(ctx context.Context, mediaID, season int, episodeKey string) ([]mediaidentity.ResourceCandidate, error) {
	if reader.byEpisode == nil {
		return []mediaidentity.ResourceCandidate{}, nil
	}
	return reader.byEpisode(ctx, mediaID, season, episodeKey)
}

func (reader combinedEpisodeReader) ListUnitResourceCandidates(ctx context.Context, mediaUnitID int) ([]mediaidentity.ResourceCandidate, error) {
	return reader.byUnit(ctx, mediaUnitID)
}

func (reader combinedEpisodeReader) ListAllEpisodes(ctx context.Context, mediaID int) ([]mediaidentity.EpisodeInfo, error) {
	if reader.all != nil {
		return reader.all(ctx, mediaID)
	}
	return []mediaidentity.EpisodeInfo{}, nil
}

type mediaResolverFunc func(context.Context, string) (mediaidentity.Media, error)

func (resolver mediaResolverFunc) FindByDoubanID(ctx context.Context, doubanID string) (mediaidentity.Media, error) {
	return resolver(ctx, doubanID)
}

type linkedMediaResolverStub struct{ media mediaidentity.Media }

func (resolver linkedMediaResolverStub) FindByDoubanID(context.Context, string) (mediaidentity.Media, error) {
	return mediaidentity.Media{}, nil
}

func (resolver linkedMediaResolverStub) FindByID(_ context.Context, id int) (mediaidentity.Media, error) {
	if id == resolver.media.ID {
		return resolver.media, nil
	}
	return mediaidentity.Media{}, nil
}

func (resolver linkedMediaResolverStub) FindResourceLink(_ context.Context, sourceKey, vodID string) (mediaidentity.ResourceLink, error) {
	if sourceKey == "source" && vodID == "42" {
		return mediaidentity.ResourceLink{SourceKey: sourceKey, VodID: vodID, MediaID: resolver.media.ID}, nil
	}
	return mediaidentity.ResourceLink{}, nil
}

type playbackEventWriterFunc func(context.Context, mediaidentity.PlaybackAttemptEvent) (bool, error)

func (function playbackEventWriterFunc) RecordPlaybackEvent(ctx context.Context, event mediaidentity.PlaybackAttemptEvent) (bool, error) {
	return function(ctx, event)
}

func TestPlayerPagesPreserveStandaloneEmbedAndMetadata(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	router, cfg := playbackTestRouter(t, search.NewPostgresStore(testdb.Pool(t)), staticPopularProvider{})

	normal := performRequest(router, "/player?url=https%3A%2F%2Fvideo.example%2Fa.m3u8", nil)
	if normal.Code != http.StatusOK || !strings.Contains(normal.Body.String(), "M3U8在线播放器 - HLS直播流测试工具") {
		t.Fatalf("normal player status/body = %d/%s", normal.Code, normal.Body.String())
	}
	if !strings.Contains(normal.Body.String(), `<link rel="canonical" href="`+cfg.SiteURL+`/player">`) {
		t.Fatalf("player canonical missing: %s", normal.Body.String())
	}

	embed := performRequest(router, "/player?embed=1&url=https%3A%2F%2Fvideo.example%2Fa.m3u8", nil)
	if embed.Code != http.StatusOK || !strings.Contains(embed.Body.String(), "https://video.example/a.m3u8") {
		t.Fatalf("embed player status/body = %d/%s", embed.Code, embed.Body.String())
	}
	if strings.Contains(embed.Body.String(), "Moovie影牛 - 发现你的下一部电影") {
		t.Fatal("embed player unexpectedly rendered the shared site layout")
	}
}

func TestTVBoxConfigUsesRequestHostAndForwardedScheme(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	router, _ := playbackTestRouter(t, search.NewPostgresStore(testdb.Pool(t)), staticPopularProvider{})
	recorder := performRequest(router, "/api/tvbox.json", map[string]string{"Host": "tv.example", "X-Forwarded-Proto": "https"})
	payload := decodeJSON(t, recorder)
	sites := payload["sites"].([]any)
	if got := sites[0].(map[string]any)["api"]; got != "https://tv.example/api/vod" {
		t.Fatalf("api = %v", got)
	}
	if _, ok := payload["lives"].([]any); !ok {
		t.Fatalf("lives is not an array: %#v", payload["lives"])
	}
}

func TestTVBoxVODPreservesParameterPrecedenceAndSearchPagination(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	for index := 0; index < 21; index++ {
		_ = store.Upsert(context.Background(), search.VodItem{
			SourceKey: "source", VodId: string(rune('a' + index)), VodName: "测试影片",
			VodPlayUrl: "第01集$https://video.example/a.m3u8", TypeName: "电影",
		})
	}
	router, _ := playbackTestRouter(t, store, staticPopularProvider{})

	precedence := decodeJSON(t, performRequest(router, "/api/vod?ids=test&wd=%E6%B5%8B%E8%AF%95", nil))
	first := precedence["list"].([]any)[0].(map[string]any)
	if first["vod_id"] != "test" {
		t.Fatalf("ids did not take precedence: %#v", first)
	}

	page := decodeJSON(t, performRequest(router, "/api/vod?wd=%E6%B5%8B%E8%AF%95&pg=2", nil))
	if page["page"] != float64(2) || page["pagecount"] != float64(2) || page["total"] != float64(21) || page["limit"] != "20" {
		t.Fatalf("pagination changed: %#v", page)
	}
	if len(page["list"].([]any)) != 1 {
		t.Fatalf("second page size = %d", len(page["list"].([]any)))
	}
}

func TestTVBoxHomeAndDetailKeepPublicShapes(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(context.Background(), search.VodItem{
		SourceKey: "source", VodId: "42", VodName: "测试影片", TypeName: "电影",
		VodPlayUrl: "第01集$https://video.example/a.m3u8",
	})
	router, _ := playbackTestRouter(t, store, staticPopularProvider{subjects: []PopularSubject{{ID: "1292052", Title: "肖申克", Cover: "/api/proxy/image/cover"}}})

	home := decodeJSON(t, performRequest(router, "/api/vod", map[string]string{"Host": "tv.example", "X-Forwarded-Proto": "https"}))
	if len(home["class"].([]any)) != 4 || home["page"] != float64(1) {
		t.Fatalf("home payload = %#v", home)
	}
	homeItem := home["list"].([]any)[0].(map[string]any)
	if homeItem["vod_id"] != "douban:1292052" || homeItem["vod_pic"] != "https://tv.example/api/proxy/image/cover" {
		t.Fatalf("home item = %#v", homeItem)
	}

	detail := decodeJSON(t, performRequest(router, "/api/vod?ids=source:42", nil))
	detailItem := detail["list"].([]any)[0].(map[string]any)
	if detailItem["vod_play_from"] != "默认源" || detailItem["vod_play_url"] != "第01集$https://video.example/a.m3u8" {
		t.Fatalf("detail item = %#v", detailItem)
	}

	invalid := decodeJSON(t, performRequest(router, "/api/vod?ids=invalid", nil))
	if invalid["msg"] != "无效的ID" || len(invalid["list"].([]any)) != 0 {
		t.Fatalf("invalid detail = %#v", invalid)
	}
}

func TestPlayPageSelectsDefaultSourceEpisodeAndPreservesSEO(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(context.Background(), search.VodItem{
		SourceKey: "source", VodId: "42", VodName: "测试影片", VodPic: "https://img.example/poster.webp",
		VodDoubanId: "1292052", VodClass: "剧情, 犯罪", VodDirector: "导演甲,导演乙", VodActor: "演员甲,演员乙",
		VodYear: "2026", VodArea: "中国", VodPlayUrl: "第01集$https://video.example/1.m3u8#第02集$https://video.example/2.m3u8$$$正片$https://backup.example/main.m3u8",
	})
	router, cfg := playbackTestRouter(t, store, staticPopularProvider{})
	recorder := performRequest(router, "/play/source/42", nil)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "《测试影片》(第01集) - 在线播放免费高清线路 - Moovie影牛") {
		t.Fatalf("status/title = %d/%s", recorder.Code, body)
	}
	for _, expected := range []string{"video.example", "1.m3u8", "第02集", "剧情", "导演甲", "/movie/1292052"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("play page missing %q", expected)
		}
	}
	if strings.Contains(body, `rel="canonical"`) {
		t.Fatalf("resource play page unexpectedly gained canonical for %s", cfg.SiteURL)
	}

	backup := performRequest(router, "/play/source/42?source=%E5%A4%87%E7%94%A8%E6%BA%90%20B", nil)
	if backup.Code != http.StatusOK || !strings.Contains(backup.Body.String(), "backup.example") || !strings.Contains(backup.Body.String(), "main.m3u8") {
		t.Fatalf("backup source status/body = %d/%s", backup.Code, backup.Body.String())
	}
}

func TestConfirmedResourceUsesCanonicalDisplayOnPlayAndTVBox(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), search.VodItem{
		SourceKey: "source", VodId: "42", VodName: "资源标题", VodPic: "resource-poster", VodYear: "2020",
		VodContent: "资源简介", VodPlayUrl: "正片$https://video.example/main.m3u8",
	})
	media := mediaidentity.Media{
		ID: 7, DoubanID: "1292052", Title: "主资料标题", Poster: "canonical-poster", Year: "2026",
		Summary: "主资料简介", Genres: "剧情,犯罪", Countries: "中国",
	}
	router, _ := playbackTestRouter(t, store, staticPopularProvider{}, WithMediaResolver(linkedMediaResolverStub{media: media}))

	play := performRequest(router, "/play/source/42", nil)
	body := play.Body.String()
	for _, expected := range []string{"<title>《主资料标题》(正片) - 在线播放免费高清线路 - Moovie影牛</title>", "主资料简介", "/movie/1292052?title="} {
		if play.Code != http.StatusOK || !strings.Contains(body, expected) {
			t.Fatalf("canonical play page missing %q: status=%d", expected, play.Code)
		}
	}

	detail := decodeJSON(t, performRequest(router, "/api/vod?ids=source:42", nil))
	item := detail["list"].([]any)[0].(map[string]any)
	if item["vod_name"] != "主资料标题" || item["vod_pic"] != "canonical-poster" || item["vod_year"] != "2026" || item["vod_content"] != "主资料简介" {
		t.Fatalf("canonical TVBox item = %#v", item)
	}
}

func TestPlayPageUsesOptionalIdentityForWatchedButton(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(context.Background(), search.VodItem{
		SourceKey: "source", VodId: "42", VodName: "测试影片", VodDoubanId: "1292052",
		VodPlayUrl: "第01集$https://video.example/1.m3u8",
	})
	marker := userMovieStoreFunc(func(_ context.Context, userID int, movieID, status string) (bool, error) {
		return userID == 7 && movieID == "1292052" && status == "watched", nil
	})
	router, _ := playbackTestRouter(t, store, staticPopularProvider{}, WithUserMovieStore(marker))

	guest := performRequest(router, "/play/source/42", nil)
	if guest.Code != http.StatusOK || !strings.Contains(guest.Body.String(), `/auth/login?redirect=%2Fplay%2Fsource%2F42`) {
		t.Fatalf("guest watched action = %d/%s", guest.Code, guest.Body.String())
	}
	now := time.Now()
	token, err := auth.Sign(auth.Claims{UserID: 7, Email: "person@example.com", Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/play/source/42", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	authenticated := httptest.NewRecorder()
	router.ServeHTTP(authenticated, request)
	if authenticated.Code != http.StatusOK || !strings.Contains(authenticated.Body.String(), `class="video-action-btn is-active"`) || !strings.Contains(authenticated.Body.String(), "已看过") {
		t.Fatalf("authenticated watched action = %d/%s", authenticated.Code, authenticated.Body.String())
	}
}

func TestWatchPageUsesFirstStoredEpisodeWhenQueryIsEmpty(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(t.Context(), search.VodItem{
		SourceKey: "source", VodId: "42", VodName: "测试影片", VodDoubanId: "1292052",
		VodPlayUrl: "正片$https://video.example/main.m3u8",
	})
	resolver := mediaResolverFunc(func(_ context.Context, doubanID string) (mediaidentity.Media, error) {
		return mediaidentity.Media{ID: 7, DoubanID: doubanID, Title: "测试影片", MediaType: "movie"}, nil
	})
	reader := combinedEpisodeReader{
		all: func(context.Context, int) ([]mediaidentity.EpisodeInfo, error) {
			return []mediaidentity.EpisodeInfo{{SeasonNumber: 1, EpisodeKey: "正片", EpisodeLabel: "正片", SourceCount: 1}}, nil
		},
		byEpisode: episodeReaderFunc(func(_ context.Context, mediaID, season int, episodeKey string) ([]mediaidentity.ResourceCandidate, error) {
			if mediaID != 7 || season != 1 || episodeKey != "正片" {
				t.Fatalf("candidate lookup = %d/%d/%s", mediaID, season, episodeKey)
			}
			return []mediaidentity.ResourceCandidate{{Episode: mediaidentity.Episode{
				CandidateID: 9, LineID: 8, LineLabel: "默认源", SourceKey: "source", VodID: "42",
				MediaID: 7, MediaUnitID: 6, SeasonNumber: 1, EpisodeKey: "正片", EpisodeLabel: "正片",
				PlayURL: "https://video.example/main.m3u8",
			}, MappingConfidence: 1}}, nil
		}),
	}
	router, _ := playbackTestRouter(t, store, staticPopularProvider{}, WithMediaResolver(resolver), WithEpisodeReader(reader))
	response := performRequest(router, "/watch/1292052", nil)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `https:\/\/video.example\/main.m3u8`) || !strings.Contains(body, `var episode = '正片'`) {
		t.Fatalf("watch page did not use the first stored episode: status=%d", response.Code)
	}
}

func TestPlayPagePreservesNotFoundInvalidEpisodeAndCopyrightStatuses(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(context.Background(), search.VodItem{SourceKey: "source", VodId: "42", VodName: "受限影片", VodPlayUrl: "第01集$https://video.example/1.m3u8"})
	router, _ := playbackTestRouter(t, store, staticPopularProvider{})

	missing := performRequest(router, "/play/source/missing", nil)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "视频未找到") {
		t.Fatalf("missing status/body = %d/%s", missing.Code, missing.Body.String())
	}
	invalidEpisode := performRequest(router, "/play/source/42?ep=%E4%B8%8D%E5%AD%98%E5%9C%A8", nil)
	if invalidEpisode.Code != http.StatusOK || !strings.Contains(invalidEpisode.Body.String(), "暂无可用播放链接") {
		t.Fatalf("invalid episode status/body = %d/%s", invalidEpisode.Code, invalidEpisode.Body.String())
	}

	blockedRouter, _ := playbackTestRouter(t, store, staticPopularProvider{}, WithCopyrightChecker(copyrightCheckerFunc(func(_ context.Context, title string) (bool, string) {
		return title == "受限影片", "受限"
	})))
	blocked := performRequest(blockedRouter, "/play/source/42", nil)
	if blocked.Code != http.StatusFound || blocked.Header().Get("Location") != "/copyright-restricted?title=%E5%8F%97%E9%99%90%E5%BD%B1%E7%89%87" {
		t.Fatalf("blocked status/location = %d/%q", blocked.Code, blocked.Header().Get("Location"))
	}
}

func TestLegacyLoadSpeedEndpointsAreRemoved(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	store := search.NewPostgresStore(testdb.Pool(t))
	_ = store.Upsert(context.Background(), search.VodItem{SourceKey: "source", VodId: "42", VodName: "影片"})
	router, _ := playbackTestRouter(t, store, staticPopularProvider{})

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/report/load-speed", bytes.NewBuffer(nil)),
		httptest.NewRequest(http.MethodGet, "/api/stats/load-speed?source_key=source&vod_id=42", nil),
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("legacy route %s %s = %d", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestResourceEndpointRanksOnlyRequestedCanonicalEpisode(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	reader := episodeReaderFunc(func(_ context.Context, mediaID, season int, episodeKey string) ([]mediaidentity.ResourceCandidate, error) {
		if mediaID != 7 || season != 1 || episodeKey != "S01E03" {
			t.Fatalf("lookup = %d/%d/%s", mediaID, season, episodeKey)
		}
		return []mediaidentity.ResourceCandidate{
			{Episode: mediaidentity.Episode{SourceKey: "fast", VodID: "a", MediaID: 7, SeasonNumber: 1, EpisodeKey: "S01E03", PlayURL: "fast-3"}, SuccessCount: 9, FailureCount: 1, AvgLoadMs: 800},
			{Episode: mediaidentity.Episode{SourceKey: "wrong", VodID: "b", MediaID: 7, SeasonNumber: 1, EpisodeKey: "S01E01", PlayURL: "wrong-1"}, SuccessCount: 100},
		}, nil
	})
	router, _ := playbackTestRouter(t, search.NewPostgresStore(testdb.Pool(t)), staticPopularProvider{}, WithEpisodeReader(reader))
	payload := decodeJSON(t, performRequest(router, "/api/v2/media/7/resources?ep=%E7%AC%AC3%E9%9B%86", nil))
	resources := payload["resources"].([]any)
	if payload["episode_key"] != "S01E03" || len(resources) != 1 || resources[0].(map[string]any)["play_url"] != "fast-3" {
		t.Fatalf("resource payload = %#v", payload)
	}
}

func TestPlaybackCandidatesV2KeepsExactUnitAndRankedOrder(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	reader := combinedEpisodeReader{byUnit: func(_ context.Context, unitID int) ([]mediaidentity.ResourceCandidate, error) {
		if unitID != 51 {
			t.Fatalf("unit lookup = %d", unitID)
		}
		return []mediaidentity.ResourceCandidate{
			{Episode: mediaidentity.Episode{CandidateID: 71, LineID: 61, SourceKey: "original", VodID: "a", MediaID: 7, MediaUnitID: 51, SeasonNumber: 1, EpisodeKey: "S01E03", PlayURL: "slow"}, SuccessCount: 1, FailureCount: 4, MappingConfidence: 1},
			{Episode: mediaidentity.Episode{CandidateID: 72, LineID: 62, SourceKey: "healthy", VodID: "b", MediaID: 7, MediaUnitID: 51, SeasonNumber: 1, EpisodeKey: "S01E03", PlayURL: "fast"}, SuccessCount: 90, FailureCount: 10, MappingConfidence: 0.95},
			{Episode: mediaidentity.Episode{CandidateID: 73, MediaID: 7, MediaUnitID: 99, PlayURL: "wrong-unit"}},
		}, nil
	}}
	router, _ := playbackTestRouter(t, search.NewPostgresStore(testdb.Pool(t)), staticPopularProvider{}, WithEpisodeReader(reader))
	payload := decodeJSON(t, performRequest(router, "/api/v2/media-units/51/playback-candidates", nil))
	candidates := payload["candidates"].([]any)
	// 排序已固定启用：健康度更高的 72 必须排在 71 之前，且不同单元的 73 仍被排除。
	if len(candidates) != 2 || candidates[0].(map[string]any)["candidate_id"] != float64(72) || candidates[1].(map[string]any)["candidate_id"] != float64(71) {
		t.Fatalf("candidate payload = %#v", payload)
	}
}

func TestPlaybackEventV2ForwardsIdempotentAttemptIdentity(t *testing.T) {
	testdb.User(t, testdb.Pool(t), 7)
	var recorded mediaidentity.PlaybackAttemptEvent
	writer := playbackEventWriterFunc(func(_ context.Context, event mediaidentity.PlaybackAttemptEvent) (bool, error) {
		recorded = event
		return true, nil
	})
	router, _ := playbackTestRouter(t, search.NewPostgresStore(testdb.Pool(t)), staticPopularProvider{}, WithPlaybackEventWriter(writer))
	request := httptest.NewRequest(http.MethodPost, "/api/v2/playback/events", bytes.NewBufferString(`{"attempt_id":"attempt-123456","candidate_session_id":"session-123456","event_type":"played_10s","candidate_id":71,"media_unit_id":51,"source_key":"source","vod_id":"42","elapsed_ms":10000}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"accepted":true`) || recorded.AttemptID != "attempt-123456" || recorded.CandidateSessionID != "session-123456" || recorded.MediaUnitID != 51 {
		t.Fatalf("event response/record = %d/%s/%+v", recorder.Code, recorder.Body.String(), recorded)
	}
}

func playbackTestRouter(t *testing.T, store *search.PostgresStore, popular PopularProvider, options ...HandlerOption) (*gin.Engine, config.Config) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.Config{SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"player", "player_embed", "iptv", "tvbox", "play", "watch", "404"})
	if err != nil {
		t.Fatal(err)
	}
	crawler := detailCrawlerFunc(func(context.Context, string, string, string) (*search.VodItem, error) { return nil, nil })
	details := NewDetailService(store, store, crawler, nil, 0)
	options = append([]HandlerOption{WithSpeedStore(store)}, options...)
	handler := NewHandler(cfg, store, details, popular, nil, options...)
	router := gin.New()
	router.HTMLRender = renderer
	handler.Register(router)
	return router, cfg
}

func performRequest(router http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	for key, value := range headers {
		if key == "Host" {
			request.Host = value
		} else {
			request.Header.Set(key, value)
		}
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSON(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, recorder.Body.String())
	}
	return payload
}
