package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthPagesPreserveLegacyTemplatesAndRedirectField(t *testing.T) {
	router, _, _ := identityTestRouter(t, "test")
	request := httptest.NewRequest(http.MethodGet, "/auth/login?redirect=%2Fplay%2Fsource%2F42", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "欢迎回来") || !strings.Contains(recorder.Body.String(), `value="/play/source/42"`) {
		t.Fatalf("login status/body = %d/%s", recorder.Code, recorder.Body.String())
	}
	register := httptest.NewRecorder()
	router.ServeHTTP(register, httptest.NewRequest(http.MethodGet, "/auth/register", nil))
	if register.Code != http.StatusOK || !strings.Contains(register.Body.String(), "创建账号") {
		t.Fatalf("register status/body = %d/%s", register.Code, register.Body.String())
	}
}

func TestRegisterValidatesAndCreatesLegacyBcryptUser(t *testing.T) {
	router, store, now := identityTestRouter(t, "test")
	invalid := postForm(router, "/auth/register", url.Values{"email": {"bad"}, "password": {"123456"}, "confirm_password": {"123456"}})
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), "请输入有效的邮箱地址") {
		t.Fatalf("invalid register = %d/%s", invalid.Code, invalid.Body.String())
	}

	registered := postForm(router, "/auth/register", url.Values{"email": {"person@example.com"}, "password": {"secret1"}, "confirm_password": {"secret1"}})
	if registered.Code != http.StatusFound || registered.Header().Get("Location") != "/" {
		t.Fatalf("register status/location = %d/%q", registered.Code, registered.Header().Get("Location"))
	}
	user, _ := store.FindByEmail(t.Context(), "person@example.com")
	if user == nil || user.Username != "person" || user.Role != "user" || user.Avatar != "🎬" || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte("secret1")) != nil {
		t.Fatalf("created user = %+v", user)
	}
	cookie := responseCookie(t, registered, "token")
	claims, err := auth.Parse(cookie.Value, "secret", now)
	if err != nil || claims.UserID != user.ID || claims.Email != user.Email {
		t.Fatalf("claims/error = %+v/%v", claims, err)
	}
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Secure {
		t.Fatalf("test cookie = %+v", cookie)
	}
}

func TestLoginUsesExistingHashAndRejectsOpenRedirect(t *testing.T) {
	router, store, now := identityTestRouter(t, "production")
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret1"), bcrypt.DefaultCost)
	_, _ = store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: string(hash), Role: "user", CreatedAt: now})

	bad := postForm(router, "/auth/login", url.Values{"email": {"person@example.com"}, "password": {"wrong"}})
	if bad.Code != http.StatusOK || !strings.Contains(bad.Body.String(), "邮箱或密码错误") {
		t.Fatalf("bad login = %d/%s", bad.Code, bad.Body.String())
	}
	loggedIn := postForm(router, "/auth/login", url.Values{"email": {"person@example.com"}, "password": {"secret1"}, "redirect": {"//evil.example"}})
	if loggedIn.Code != http.StatusFound || loggedIn.Header().Get("Location") != "/" {
		t.Fatalf("login status/location = %d/%q", loggedIn.Code, loggedIn.Header().Get("Location"))
	}
	if cookie := responseCookie(t, loggedIn, "token"); !cookie.Secure || !cookie.HttpOnly || cookie.MaxAge != int((72*time.Hour).Seconds()) {
		t.Fatalf("production cookie = %+v", cookie)
	}
}

func TestLogoutExpiresToken(t *testing.T) {
	router, _, _ := identityTestRouter(t, "test")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/auth/logout", nil))
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != "/" {
		t.Fatalf("logout status/location = %d/%q", recorder.Code, recorder.Header().Get("Location"))
	}
	if cookie := responseCookie(t, recorder, "token"); cookie.MaxAge != -1 || cookie.Value != "" {
		t.Fatalf("logout cookie = %+v", cookie)
	}
}

func TestDashboardRequiresAuthAndSettingsUpdateUser(t *testing.T) {
	router, store, now := identityTestRouter(t, "test")
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret1"), bcrypt.DefaultCost)
	user, _ := store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: string(hash), Role: "user", Avatar: "🎬", CreatedAt: now})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(72 * time.Hour).Unix()}, "secret")

	unauthorizedRequest := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	unauthorizedRequest.Header.Set("Accept", "text/html")
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, unauthorizedRequest)
	if unauthorized.Code != http.StatusFound || unauthorized.Header().Get("Location") != "/auth/login?redirect=/dashboard" {
		t.Fatalf("unauthorized status/location = %d/%q", unauthorized.Code, unauthorized.Header().Get("Location"))
	}

	dashboardRequest := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	dashboardRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	dashboard := httptest.NewRecorder()
	router.ServeHTTP(dashboard, dashboardRequest)
	if dashboard.Code != http.StatusOK || !strings.Contains(dashboard.Body.String(), "person") || !strings.Contains(dashboard.Body.String(), "部在看") {
		t.Fatalf("dashboard status/body = %d/%s", dashboard.Code, dashboard.Body.String())
	}

	updateRequest := httptest.NewRequest(http.MethodPost, "/dashboard/settings/username", strings.NewReader(url.Values{"username": {"new-name"}}.Encode()))
	updateRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	updateRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	updated := httptest.NewRecorder()
	router.ServeHTTP(updated, updateRequest)
	if updated.Code != http.StatusFound || updated.Header().Get("Location") != "/dashboard/settings?success=username" {
		t.Fatalf("update status/location = %d/%q", updated.Code, updated.Header().Get("Location"))
	}
	stored, _ := store.FindByID(t.Context(), user.ID)
	if stored.Username != "new-name" {
		t.Fatalf("stored user = %+v", stored)
	}
}

func TestDashboardUsesLibraryCounts(t *testing.T) {
	router, store, now := identityTestRouterWithOptions(t, "test", WithLibraryCounter(identityLibraryCounter{"wish": 3, "watched": 5}))
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret1"), bcrypt.DefaultCost)
	user, _ := store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: string(hash), Role: "user", Avatar: "🎬", CreatedAt: now})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(72 * time.Hour).Unix()}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, `<span class="stat-value">3</span>`) || !strings.Contains(body, `<span class="stat-value">5</span>`) {
		t.Fatalf("dashboard counts = %d/%s", recorder.Code, recorder.Body.String())
	}
}

func TestDashboardShowsLatestMonthlyReport(t *testing.T) {
	reader := identityMonthlyReader{report: struct {
		YearMonth     string
		WatchedCount  int
		AvgRating     float64
		TopMovieTitle string
	}{YearMonth: "2026-07", WatchedCount: 8, AvgRating: 4.5, TopMovieTitle: "最佳电影"}}
	router, store, now := identityTestRouterWithOptions(t, "test", WithMonthlyReportReader(reader))
	user, _ := store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: "hash", Role: "user", Avatar: "🎬", CreatedAt: now})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "2026-07") || !strings.Contains(recorder.Body.String(), "最佳电影") {
		t.Fatalf("dashboard monthly report = %d/%s", recorder.Code, recorder.Body.String())
	}
}

type identityMonthlyReader struct{ report any }

func (reader identityMonthlyReader) LatestForDashboard(context.Context, int) (any, error) {
	return reader.report, nil
}

type identityLibraryCounter map[string]int

func (counter identityLibraryCounter) CountByUser(_ context.Context, _ int, stringStatus string) (int, error) {
	return counter[stringStatus], nil
}

func TestSettingsPreservePasswordAndAvatarValidation(t *testing.T) {
	router, store, now := identityTestRouter(t, "test")
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret1"), bcrypt.DefaultCost)
	user, _ := store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: string(hash), Role: "user", Avatar: "🎬", CreatedAt: now})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(72 * time.Hour).Unix()}, "secret")

	badPassword := authenticatedForm(router, token, "/dashboard/settings/password", url.Values{"current_password": {"wrong"}, "new_password": {"newpass"}, "confirm_password": {"newpass"}})
	if badPassword.Code != http.StatusOK || !strings.Contains(badPassword.Body.String(), "当前密码错误") {
		t.Fatalf("bad password = %d/%s", badPassword.Code, badPassword.Body.String())
	}
	badAvatar := authenticatedForm(router, token, "/dashboard/settings/avatar", url.Values{"avatar": {"😀😁😂🤣😃"}})
	if badAvatar.Code != http.StatusOK || !strings.Contains(badAvatar.Body.String(), "头像最多支持 4 个 emoji 字符") {
		t.Fatalf("bad avatar = %d/%s", badAvatar.Code, badAvatar.Body.String())
	}
}

func identityTestRouter(t *testing.T, environment string) (*gin.Engine, *MemoryStore, time.Time) {
	return identityTestRouterWithOptions(t, environment)
}

func identityTestRouterWithOptions(t *testing.T, environment string, options ...HandlerOption) (*gin.Engine, *MemoryStore, time.Time) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	cfg := config.Config{Env: environment, SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret", JWTExpiry: 72 * time.Hour}
	store := NewMemoryStore()
	handler := NewHandler(cfg, store, options...)
	handler.now = func() time.Time { return now }
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"login", "register", "dashboard", "settings"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	handler.Register(router)
	return router, store, now
}

func authenticatedForm(router http.Handler, token, path string, values url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func postForm(router http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func responseCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	response := recorder.Result()
	defer response.Body.Close()
	for _, cookie := range response.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("cookie %q missing: %v", name, recorder.Header().Values("Set-Cookie"))
	return nil
}
