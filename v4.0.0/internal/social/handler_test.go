package social

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestCommentsLikesAndRepliesPreserveLegacyHTMXBehavior(t *testing.T) {
	router, users, movies, store, publicUser, token := socialTestRouter(t)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.Local)
	if err := movies.Upsert(t.Context(), library.Record{UserID: publicUser.ID, MovieID: "1292052", Status: library.StatusWatched, Rating: 5, Comment: "很好的电影", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	record, _ := movies.GetByUserAndMovie(t.Context(), publicUser.ID, "1292052")
	privateUser, _ := users.Create(t.Context(), identity.User{Email: "private@example.com", Username: "私密用户", Role: "user", Avatar: "🍿", CreatedAt: now})
	_ = movies.Upsert(t.Context(), library.Record{UserID: privateUser.ID, MovieID: "1292052", Status: library.StatusWatched, Comment: "私密短评", CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)})

	comments := performRequest(router, http.MethodGet, "/api/htmx/movie-comments?douban_id=1292052", "", "")
	if comments.Code != http.StatusOK || !strings.Contains(comments.Body.String(), "很好的电影") || !strings.Contains(comments.Body.String(), "私密短评") || strings.Contains(comments.Body.String(), `/user/2`) {
		t.Fatalf("comments = %d/%s", comments.Code, comments.Body.String())
	}

	unauthorized := performRequest(router, http.MethodPost, "/api/comments/"+itoa(record.ID)+"/like", "", "")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized like = %d", unauthorized.Code)
	}
	liked := performRequest(router, http.MethodPost, "/api/comments/"+itoa(record.ID)+"/like", "", token)
	if liked.Code != http.StatusOK || !strings.Contains(liked.Body.String(), "liked") || !strings.Contains(liked.Body.String(), ">1</span>") {
		t.Fatalf("liked = %d/%s", liked.Code, liked.Body.String())
	}
	unliked := performRequest(router, http.MethodPost, "/api/comments/"+itoa(record.ID)+"/like", "", token)
	if unliked.Code != http.StatusOK || strings.Contains(unliked.Body.String(), " liked") || strings.Contains(unliked.Body.String(), ">1</span>") {
		t.Fatalf("unliked = %d/%s", unliked.Code, unliked.Body.String())
	}

	longReply := strings.Repeat("影", 301) + "<script>alert(1)</script>"
	form := url.Values{"content": {"  " + longReply + "  "}}.Encode()
	replied := performRequest(router, http.MethodPost, "/api/comments/"+itoa(record.ID)+"/replies", form, token)
	if replied.Code != http.StatusOK || !strings.Contains(replied.Body.String(), strings.Repeat("影", 300)) || strings.Contains(replied.Body.String(), "script") {
		t.Fatalf("reply = %d/%s", replied.Code, replied.Body.String())
	}
	replies, _ := store.ListReplies(t.Context(), record.ID)
	if len(replies) != 1 || len([]rune(replies[0].Content)) != 300 {
		t.Fatalf("stored replies = %+v", replies)
	}
	empty := performRequest(router, http.MethodPost, "/api/comments/"+itoa(record.ID)+"/replies", "content=+++", token)
	if empty.Code != http.StatusBadRequest || empty.Body.String() != "回复内容不能为空" {
		t.Fatalf("empty reply = %d/%q", empty.Code, empty.Body.String())
	}
}

func TestCinemaBuildsProgramCommentsAndFilmFriendRadar(t *testing.T) {
	router, users, movies, _, publicUser, token := socialTestRouter(t)
	now := time.Now()
	_ = movies.Upsert(t.Context(), library.Record{UserID: publicUser.ID, MovieID: "shared", Title: "共同放映", Poster: "poster-a", Status: library.StatusWatched, Rating: 5, Comment: "第一条公开短评", CreatedAt: now, UpdatedAt: now})
	friend, _ := users.Create(t.Context(), identity.User{Email: "friend@example.com", Username: "同好", Role: "user", Avatar: "📽️", IsPublic: true, CreatedAt: now})
	_ = movies.Upsert(t.Context(), library.Record{UserID: friend.ID, MovieID: "shared", Title: "共同放映", Poster: "poster-b", Status: library.StatusWatched, Rating: 3, Comment: "第二条公开短评", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)})
	privateUser, _ := users.Create(t.Context(), identity.User{Email: "hidden@example.com", Username: "隐藏用户", Role: "user", IsPublic: false, CreatedAt: now})
	_ = movies.Upsert(t.Context(), library.Record{UserID: privateUser.ID, MovieID: "secret", Title: "不应出现", Status: library.StatusWatched, Comment: "私密短评", CreatedAt: now, UpdatedAt: now})

	page := performRequest(router, http.MethodGet, "/cinema", "", "")
	for _, expected := range []string{"片场 - Moovie影牛", "本周放映单", "值得停一下的短评", "片友雷达", "共同放映", "2 位片友看过", "第一条公开短评", "第二条公开短评"} {
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), expected) {
			t.Fatalf("cinema page missing %q = %d/%s", expected, page.Code, page.Body.String())
		}
	}
	if strings.Contains(page.Body.String(), "不应出现") || strings.Contains(page.Body.String(), "/api/htmx/square/") {
		t.Fatalf("cinema leaked private or legacy content: %s", page.Body.String())
	}
	personalized := performRequest(router, http.MethodGet, "/cinema", "", token)
	if personalized.Code != http.StatusOK || !strings.Contains(personalized.Body.String(), "与你共同看过 1 部") {
		t.Fatalf("personalized radar = %d/%s", personalized.Code, personalized.Body.String())
	}
	for _, removed := range []string{"/square", "/api/htmx/square/activity", "/api/htmx/square/leaderboard"} {
		response := performRequest(router, http.MethodGet, removed, "", "")
		if response.Code != http.StatusNotFound {
			t.Fatalf("removed route %s = %d", removed, response.Code)
		}
	}
}

func socialTestRouter(t *testing.T) (*gin.Engine, *identity.MemoryStore, *library.MemoryStore, *MemoryStore, *identity.User, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewMemoryStore()
	publicUser, _ := users.Create(t.Context(), identity.User{Email: "public@example.com", Username: "公开用户", Role: "user", Avatar: "🎬", IsPublic: true, CreatedAt: time.Now()})
	movies := library.NewMemoryStore()
	store := NewMemoryStore(movies, users)
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"cinema"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, store).Register(router)
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: publicUser.ID, Email: publicUser.Email, Role: publicUser.Role, Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, users, movies, store, publicUser, token
}

func performRequest(router http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if token != "" {
		request.AddCookie(&http.Cookie{Name: "token", Value: token})
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func itoa(value int) string { return strconv.Itoa(value) }
