package library

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestLibraryUnauthorizedResponsesPreserveLegacyHTMXSemantics(t *testing.T) {
	router, _ := libraryTestRouter(t)

	if response := request(router, http.MethodPost, "/api/user-movies/1292052/wish", nil, ""); response.Code != http.StatusUnauthorized || response.Body.Len() != 0 {
		t.Fatalf("unauthorized write = %d/%q", response.Code, response.Body.String())
	}
	if response := request(router, http.MethodGet, "/api/htmx/user-movie/mark-watched?douban_id=1292052", nil, ""); response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("unauthorized form = %d/%q", response.Code, response.Body.String())
	}
	if response := request(router, http.MethodGet, "/api/htmx/dashboard/wish", nil, ""); response.Code != http.StatusOK || response.Body.String() != "未登录" {
		t.Fatalf("unauthorized dashboard = %d/%q", response.Code, response.Body.String())
	}
	buttons := request(router, http.MethodGet, "/api/htmx/user-movie/buttons?variant=play&douban_id=1292052&title=霸王别姬&redirect=%2Fplay%2Fa%2Fb", nil, "")
	if buttons.Code != http.StatusOK || !strings.Contains(buttons.Body.String(), `href="/auth/login?redirect=%2Fplay%2Fa%2Fb"`) || !strings.Contains(buttons.Body.String(), `id="play-actions-1292052"`) {
		t.Fatalf("guest play buttons = %d/%s", buttons.Code, buttons.Body.String())
	}
}

func TestLibraryWishWatchedUpdateAndRemoveFlow(t *testing.T) {
	router, store := libraryTestRouter(t)
	token := libraryToken(t, 7)

	wish := request(router, http.MethodPost, "/api/user-movies/1292052/wish?title=%E9%9C%B8%E7%8E%8B%E5%88%AB%E5%A7%AC&poster=https%3A%2F%2Fimg.example%2Fa%3Fx%3D1%26y%3D2&year=1993", nil, token)
	if wish.Code != http.StatusOK || !strings.Contains(wish.Body.String(), "已想看") || strings.Contains(wish.Body.String(), "已看过") {
		t.Fatalf("wish response = %d/%s", wish.Code, wish.Body.String())
	}
	record, _ := store.GetByUserAndMovie(t.Context(), 7, "1292052")
	if record == nil || record.Status != StatusWish || record.Title != "霸王别姬" {
		t.Fatalf("wish record = %+v", record)
	}

	form := url.Values{"title": {"霸王别姬"}, "poster": {"poster"}, "year": {"1993"}, "rating": {"4"}, "comment": {"值得重看"}, "variant": {"play"}}
	watched := request(router, http.MethodPost, "/api/user-movies/1292052/watched", form, token)
	if watched.Code != http.StatusOK || !strings.Contains(watched.Body.String(), "已看过") || !strings.Contains(watched.Body.String(), `id="play-actions-1292052"`) {
		t.Fatalf("watched response = %d/%s", watched.Code, watched.Body.String())
	}
	record, _ = store.GetByUserAndMovie(t.Context(), 7, "1292052")
	if record == nil || record.Status != StatusWatched || record.Rating != 4 || record.Comment != "值得重看" {
		t.Fatalf("watched record = %+v", record)
	}

	edit := request(router, http.MethodGet, "/api/htmx/user-movie/edit?id="+strconvItoa(record.ID), nil, token)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "修改评分与短评") || !strings.Contains(edit.Body.String(), "值得重看") {
		t.Fatalf("edit form = %d/%s", edit.Code, edit.Body.String())
	}
	updated := request(router, http.MethodPatch, "/api/user-movies/"+strconvItoa(record.ID), url.Values{"rating": {"9"}, "comment": {"五星"}}, token)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), "★★★★★") || !strings.Contains(updated.Body.String(), "五星") {
		t.Fatalf("update response = %d/%s", updated.Code, updated.Body.String())
	}
	record, _ = store.GetByID(t.Context(), 7, record.ID)
	if record.Rating != 5 || record.Comment != "五星" {
		t.Fatalf("updated record = %+v", record)
	}

	removed := request(router, http.MethodDelete, "/api/user-movies/1292052?source=dashboard", nil, token)
	if removed.Code != http.StatusOK || removed.Body.Len() != 0 {
		t.Fatalf("dashboard remove = %d/%q", removed.Code, removed.Body.String())
	}
	record, _ = store.GetByUserAndMovie(t.Context(), 7, "1292052")
	if record != nil {
		t.Fatalf("record remains after remove: %+v", record)
	}
}

func TestMarkWatchedPreservesLegacyQueryFallback(t *testing.T) {
	router, store := libraryTestRouter(t)
	token := libraryToken(t, 7)
	target := "/api/user-movies/1292052/watched?title=%E9%9C%B8%E7%8E%8B%E5%88%AB%E5%A7%AC&poster=poster&year=1993&rating=3&comment=%E7%BB%8F%E5%85%B8&variant=play"
	response := request(router, http.MethodPost, target, nil, token)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="play-actions-1292052"`) {
		t.Fatalf("query fallback response = %d/%s", response.Code, response.Body.String())
	}
	record, _ := store.GetByUserAndMovie(t.Context(), 7, "1292052")
	if record == nil || record.Title != "霸王别姬" || record.Poster != "poster" || record.Year != "1993" || record.Rating != 3 || record.Comment != "经典" {
		t.Fatalf("query fallback record = %+v", record)
	}
}

func TestDashboardLibraryPaginationUsesLegacyPageSize(t *testing.T) {
	router, store := libraryTestRouter(t)
	for index := 1; index <= 25; index++ {
		if err := store.Upsert(t.Context(), Record{UserID: 7, MovieID: strconvItoa(index), Title: "影片" + strconvItoa(index), Poster: "https://img.example/p.jpg", Year: "2026", Status: StatusWish}); err != nil {
			t.Fatal(err)
		}
	}
	token := libraryToken(t, 7)
	first := request(router, http.MethodGet, "/api/htmx/dashboard/wish?page=1", nil, token)
	if first.Code != http.StatusOK || strings.Count(first.Body.String(), `class="movie-card-wrapper"`) != 24 || !strings.Contains(first.Body.String(), "page=2") {
		t.Fatalf("first page status/cards = %d/%d", first.Code, strings.Count(first.Body.String(), `class="movie-card-wrapper"`))
	}
	if !strings.Contains(first.Body.String(), "/api/proxy/image/r76RqSIVvUryzx") {
		t.Fatalf("first page did not preserve proxy image URL: %s", first.Body.String())
	}
	second := request(router, http.MethodGet, "/api/htmx/dashboard/wish?page=2", nil, token)
	if second.Code != http.StatusOK || strings.Count(second.Body.String(), `class="movie-card-wrapper"`) != 1 || strings.Contains(second.Body.String(), `id="wish-grid"`) {
		t.Fatalf("second page status/cards/body = %d/%d/%s", second.Code, strings.Count(second.Body.String(), `class="movie-card-wrapper"`), second.Body.String())
	}
}

func libraryTestRouter(t *testing.T) (*gin.Engine, *PostgresStore) {
	testdb.User(t, testdb.Pool(t), 7)
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := NewPostgresStore(testdb.Pool(t))
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), nil)
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(store, "secret").Register(router)
	return router, store
}

func libraryToken(t *testing.T, userID int) string {
	t.Helper()
	now := time.Now()
	token, err := auth.Sign(auth.Claims{UserID: userID, Email: "person@example.com", Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func request(router http.Handler, method, target string, values url.Values, token string) *httptest.ResponseRecorder {
	var body *strings.Reader
	if values == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(values.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if values != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func strconvItoa(value int) string {
	return strconv.Itoa(value)
}
