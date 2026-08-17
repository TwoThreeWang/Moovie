package catalog

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
)

func TestTMDBProviderResolvesIMDbTVAndPersistsLegacyFields(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		switch request.URL.Path {
		case "/movie/api":
			// TMDB 任务不再查任何映射源；映射由 imdb_backfill 提前补好。
			t.Fatalf("TMDB sync must not call the IMDb mapping service: %s", request.URL.String())
			return nil, nil
		case "/3/find/tt0111161":
			assertTMDBBearer(t, request)
			return testJSONResponse(request, http.StatusOK, `{"movie_results":[],"tv_results":[{"id":42}]}`), nil
		case "/3/tv/42/images":
			assertTMDBBearer(t, request)
			if request.URL.Query().Get("include_image_language") != "zh,en,null" {
				t.Fatalf("image language = %q", request.URL.RawQuery)
			}
			return testJSONResponse(request, http.StatusOK, `{"backdrops":[{"file_path":"/a.jpg"},{"file_path":"/b.jpg"}],"posters":[{"file_path":"/poster.jpg"}]}`), nil
		case "/3/tv/42":
			assertTMDBBearer(t, request)
			return testJSONResponse(request, http.StatusOK, `{"original_title":"Original","overview":"简介","first_air_date":"2025-02-03","episode_run_time":[45],"genres":[{"name":"剧情"},{"name":"悬疑"}]}`), nil
		default:
			return testJSONResponse(request, http.StatusNotFound, `{}`), nil
		}
	})}

	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "标题", Poster: "old-poster", IMDbID: "tt0111161"})
	provider := NewTMDBProvider(client, store, "tmdb-token", WithTMDBBase("https://tmdb.test"))
	if err := provider.SyncBackdrops(t.Context(), "1292052"); err != nil {
		t.Fatal(err)
	}
	movie, _ := store.FindByDoubanID(t.Context(), "1292052")
	if calls.Load() != 3 || movie.IMDbID != "tt0111161" || movie.OriginalTitle != "Original" || movie.Summary != "简介" || movie.Year != "2025" || movie.Duration != "45分钟" || movie.Genres != "剧情/悬疑" {
		t.Fatalf("calls/movie = %d/%+v", calls.Load(), movie)
	}
	if movie.Poster != "old-poster" || movie.Backdrops != "https://image.tmdb.org/t/p/w1280/a.jpg,https://image.tmdb.org/t/p/w1280/b.jpg" {
		t.Fatalf("TMDB images = poster %q backdrops %q", movie.Poster, movie.Backdrops)
	}
}

func TestApplyTMDBDataUsesPosterAsFallbackWhenLegacyPosterIsEmpty(t *testing.T) {
	movie := &Movie{}
	applyTMDBData(movie, &tmdbImagesResponse{Posters: []struct {
		FilePath string `json:"file_path"`
	}{{FilePath: "/poster.jpg"}}}, nil)
	if movie.Poster != "https://image.tmdb.org/t/p/w500/poster.jpg" {
		t.Fatalf("fallback poster = %q", movie.Poster)
	}
}

func TestTMDBProviderOnlyWritesTheSeasonNamedByDoubanMedia(t *testing.T) {
	var paths []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/3/find/tt14688458":
			return testJSONResponse(request, http.StatusOK, `{"movie_results":[],"tv_results":[{"id":42}]}`), nil
		case "/3/tv/42/images":
			return testJSONResponse(request, http.StatusOK, `{"backdrops":[],"posters":[]}`), nil
		case "/3/tv/42":
			return testJSONResponse(request, http.StatusOK, `{
				"status":"Returning Series",
				"last_episode_to_air":{"season_number":1,"episode_number":10,"name":"S1 finale","air_date":"2024-01-01"},
				"next_episode_to_air":{"season_number":2,"episode_number":1,"name":"S2 premiere","air_date":"2025-01-01"},
				"seasons":[{"season_number":1},{"season_number":2}]
			}`), nil
		case "/3/tv/42/season/1":
			return testJSONResponse(request, http.StatusOK, `{"season_number":1,"episodes":[{"episode_number":10,"name":"S1 finale","air_date":"2024-01-01","runtime":45}]}`), nil
		default:
			return testJSONResponse(request, http.StatusNotFound, `{}`), nil
		}
	})}
	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "35468745", Title: "末日地堡 第一季", OriginalTitle: "Silo Season 1", IMDbID: "tt14688458",
	})
	canonical := &canonicalWriterStub{}
	units := &mediaUnitWriterStub{}
	provider := NewTMDBProvider(client, store, "tmdb-token",
		WithTMDBBase("https://tmdb.test"),
		WithTMDBCanonicalWriter(canonical), WithTMDBMediaUnitWriter(units))
	if err := provider.SyncBackdrops(t.Context(), "35468745"); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if path == "/3/tv/42/season/2" {
			t.Fatalf("second season was fetched for first-season media: %v", paths)
		}
	}
	if !slices.Contains(paths, "/3/tv/42/season/1") || len(units.units) == 0 {
		t.Fatalf("first season was not synchronized: paths=%v units=%+v", paths, units.units)
	}
	for _, unit := range units.units {
		if unit.SeasonNumber != 1 {
			t.Fatalf("wrong-season unit written: %+v", unit)
		}
	}
	if len(canonical.externalIDs) != 3 || canonical.externalIDs[1].ExternalType != "tv_season_1" || canonical.externalIDs[2].ExternalType != "tv_season_1" {
		t.Fatalf("external IDs = %+v", canonical.externalIDs)
	}
}

func TestTMDBProviderFallsBackToSeriesTitleForSeasonIMDbID(t *testing.T) {
	var paths []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		switch request.URL.Path {
		case "/3/find/tt35047389":
			return testJSONResponse(request, http.StatusOK, `{"movie_results":[],"tv_results":[]}`), nil
		case "/3/search/tv":
			if request.URL.Query().Get("query") != "silo" {
				t.Fatalf("search query = %q", request.URL.Query().Get("query"))
			}
			return testJSONResponse(request, http.StatusOK, `{"results":[{"id":125988,"name":"末日地堡","original_name":"Silo"}]}`), nil
		case "/3/tv/125988/images":
			return testJSONResponse(request, http.StatusOK, `{"backdrops":[],"posters":[]}`), nil
		case "/3/tv/125988":
			return testJSONResponse(request, http.StatusOK, `{"status":"Returning Series","seasons":[{"season_number":1},{"season_number":3}]}`), nil
		case "/3/tv/125988/season/3":
			return testJSONResponse(request, http.StatusOK, `{"season_number":3,"episodes":[{"episode_number":1,"name":"S3 premiere","air_date":"2026-08-20","runtime":45}]}`), nil
		default:
			return testJSONResponse(request, http.StatusNotFound, `{}`), nil
		}
	})}
	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{
		DoubanID: "36857449", Title: "末日地堡 第三季", OriginalTitle: "Silo Season 3", IMDbID: "tt35047389",
	})
	canonical := &canonicalWriterStub{}
	units := &mediaUnitWriterStub{}
	provider := NewTMDBProvider(client, store, "tmdb-token",
		WithTMDBBase("https://tmdb.test"),
		WithTMDBCanonicalWriter(canonical), WithTMDBMediaUnitWriter(units))
	if err := provider.SyncBackdrops(t.Context(), "36857449"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, "/3/search/tv") || !slices.Contains(paths, "/3/tv/125988/season/3") {
		t.Fatalf("season title fallback was not used: %v", paths)
	}
	if slices.Contains(paths, "/3/tv/125988/season/1") || len(units.units) != 1 || units.units[0].SeasonNumber != 3 {
		t.Fatalf("wrong seasons synchronized: paths=%v units=%+v", paths, units.units)
	}
	if len(canonical.externalIDs) != 3 || canonical.externalIDs[1].ExternalType != "tv_season_3" || canonical.externalIDs[2].ExternalID != "125988" {
		t.Fatalf("external IDs = %+v", canonical.externalIDs)
	}
}

type mediaUnitWriterStub struct{ units []mediaidentity.MediaUnit }

func (writer *mediaUnitWriterStub) EnsureMediaUnit(_ context.Context, unit mediaidentity.MediaUnit) (mediaidentity.MediaUnit, error) {
	writer.units = append(writer.units, unit)
	return unit, nil
}

func testJSONResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    request,
	}
}

func TestTMDBProviderRequiresTokenBeforeNetwork(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "标题"})
	provider := NewTMDBProvider(http.DefaultClient, store, "")
	err := provider.SyncBackdrops(t.Context(), "1292052")
	if err == nil || !strings.Contains(err.Error(), "TMDB_API_TOKEN") {
		t.Fatalf("error = %v", err)
	}
}

func assertTMDBBearer(t *testing.T, request *http.Request) {
	t.Helper()
	if request.Header.Get("Authorization") != "Bearer tmdb-token" {
		t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
	}
}
