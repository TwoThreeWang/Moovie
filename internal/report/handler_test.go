package report

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

func TestPublicProfileMonthlyAndPrivacyPreserveRoutesAndCanonical(t *testing.T) {
	router, users, libraryStore, reports, publicUser, token := reportTestRouter(t)
	for index := 1; index <= 25; index++ {
		created := time.Date(2026, 7, index, 12, 0, 0, 0, time.Local)
		_ = libraryStore.Upsert(t.Context(), library.Record{UserID: publicUser.ID, MovieID: strconv.Itoa(index), Title: "电影", Poster: "poster", Status: library.StatusWish, CreatedAt: created, UpdatedAt: created})
	}
	shared, _ := reports.Save(t.Context(), MonthlyReport{UserID: publicUser.ID, YearMonth: "2026-07", WatchedCount: 1, PersonaTitle: "推理爱好者", Status: StatusGenerated, GenreStats: "[]", PosterWall: "[]"})
	_ = shared
	created := time.Date(2026, 7, 31, 12, 0, 0, 0, time.Local)
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: publicUser.ID, MovieID: "common", Title: "共同电影", Poster: "poster", Status: library.StatusWatched, Rating: 5, CreatedAt: created, UpdatedAt: created})
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: 2, MovieID: "common", Title: "共同电影", Status: library.StatusWatched, CreatedAt: created, UpdatedAt: created})

	profile := httptest.NewRecorder()
	router.ServeHTTP(profile, httptest.NewRequest(http.MethodGet, "/user/1", nil))
	if profile.Code != http.StatusOK || !strings.Contains(profile.Body.String(), "公开用户 的观影记录") || !strings.Contains(profile.Body.String(), `href="https://moovie.example/user/1"`) || !strings.Contains(profile.Body.String(), "/api/htmx/public/1/wish?page=2") {
		t.Fatalf("profile = %d/%s", profile.Code, profile.Body.String())
	}
	monthlyRequest := httptest.NewRequest(http.MethodGet, "/user/1/monthly/2026-07", nil)
	monthlyRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	monthly := httptest.NewRecorder()
	router.ServeHTTP(monthly, monthlyRequest)
	if monthly.Code != http.StatusOK || !strings.Contains(monthly.Body.String(), "推理爱好者") || !strings.Contains(monthly.Body.String(), "都看过 <strong>1</strong> 部电影") || !strings.Contains(monthly.Body.String(), `href="https://moovie.example/user/1/monthly/2026-07"`) {
		t.Fatalf("monthly = %d/%s", monthly.Code, monthly.Body.String())
	}
	privateUser, _ := users.Create(t.Context(), identity.User{Email: "private@example.com", Username: "private", PasswordHash: "hash", Role: "user", CreatedAt: time.Now()})
	private := httptest.NewRecorder()
	router.ServeHTTP(private, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/user/%d", privateUser.ID), nil))
	if private.Code != http.StatusNotFound || strings.Contains(private.Body.String(), "private 的观影记录") {
		t.Fatalf("private profile leaked = %d/%s", private.Code, private.Body.String())
	}
}

func TestAdminMonthlyGenerationRequiresAdminAndPreservesEnvelope(t *testing.T) {
	router, users, libraryStore, _, _, userToken := reportTestRouter(t)
	created := time.Date(2026, 7, 15, 12, 0, 0, 0, time.Local)
	_ = libraryStore.Upsert(t.Context(), library.Record{UserID: 1, MovieID: "1292052", Title: "电影", Status: library.StatusWatched, Rating: 5, CreatedAt: created, UpdatedAt: created})
	forbiddenRequest := httptest.NewRequest(http.MethodPost, "/admin/monthly-report/generate", strings.NewReader("user_id=1&year_month=2026-07"))
	forbiddenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	forbiddenRequest.AddCookie(&http.Cookie{Name: "token", Value: userToken})
	forbidden := httptest.NewRecorder()
	router.ServeHTTP(forbidden, forbiddenRequest)
	if forbidden.Code != http.StatusForbidden || !strings.Contains(forbidden.Body.String(), "需要管理员权限") {
		t.Fatalf("non-admin generation = %d/%s", forbidden.Code, forbidden.Body.String())
	}
	admin, _ := users.Create(t.Context(), identity.User{Email: "admin@example.com", Username: "admin", PasswordHash: "hash", Role: "admin", CreatedAt: time.Now()})
	now := time.Now()
	adminToken, _ := auth.Sign(auth.Claims{UserID: admin.ID, Email: admin.Email, Role: "admin", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	request := httptest.NewRequest(http.MethodPost, "/admin/monthly-report/generate", strings.NewReader("user_id=1&year_month=2026-07"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "token", Value: adminToken})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"success":true`) || !strings.Contains(recorder.Body.String(), `"url":"/user/1/monthly/2026-07"`) {
		t.Fatalf("admin generation = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func reportTestRouter(t *testing.T) (*gin.Engine, *identity.PostgresStore, *library.PostgresStore, *PostgresStore, *identity.User, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewPostgresStore(testdb.Pool(t))
	publicUser, _ := users.Create(t.Context(), identity.User{Email: "public@example.com", Username: "公开用户", PasswordHash: "hash", Role: "user", Avatar: "🎬", IsPublic: true, CreatedAt: time.Now().Add(-24 * time.Hour)})
	viewer, _ := users.Create(t.Context(), identity.User{Email: "viewer@example.com", Username: "viewer", PasswordHash: "hash", Role: "user", CreatedAt: time.Now()})
	libraryStore := library.NewPostgresStore(testdb.Pool(t))
	reports := NewPostgresStore(testdb.Pool(t))
	service := NewService(reports, libraryStore, catalog.NewPostgresStore(testdb.Pool(t)))
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"share", "share_monthly", "404"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, users, libraryStore, reports, service).Register(router)
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: viewer.ID, Email: viewer.Email, Role: viewer.Role, Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, users, libraryStore, reports, publicUser, token
}
