package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/gin-gonic/gin"
)

func TestLoadUserRestoresPublicPageUserContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "user@example.com", Username: "viewer", Role: "user"})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	router := gin.New()
	router.Use(auth.Optional("secret"), LoadUser(store, "secret", false))
	router.GET("/", func(c *gin.Context) {
		loaded, exists := c.Get(UserInfoContextKey)
		if !exists || loaded.(*User).Username != "viewer" {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestLoadUserLeavesInvalidAndDeletedTokensAnonymous(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(auth.Optional("secret"), LoadUser(NewMemoryStore(), "secret", false))
	router.GET("/", func(c *gin.Context) {
		if _, exists := c.Get(UserInfoContextKey); exists {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: "invalid"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestLoadUserMakesCurrentDatabaseRoleAuthoritative(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "admin@example.com", Username: "admin", Role: "admin"})
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: "admin", Issued: now.Add(-40 * time.Minute).Unix(), Expiry: now.Add(20 * time.Minute).Unix()}, "secret")
	_ = store.UpdateRole(t.Context(), user.ID, "user")

	router := gin.New()
	router.Use(auth.Optional("secret"), LoadUser(store, "secret", false))
	router.GET("/admin", auth.Require("secret", false), auth.Optional("secret"), func(c *gin.Context) {
		role, _ := c.Get("role")
		if role != "admin" {
			c.Status(http.StatusForbidden)
			return
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("demoted admin status = %d, want %d", response.Code, http.StatusForbidden)
	}
	refreshed := response.Result().Cookies()
	if len(refreshed) == 0 {
		t.Fatal("sliding renewal cookie missing")
	}
	claims, err := auth.Parse(refreshed[0].Value, "secret", time.Now())
	if err != nil || claims.Role != "user" || claims.Email != user.Email {
		t.Fatalf("refreshed claims/error = %+v/%v", claims, err)
	}
}

// browsingRouter 复现生产装配：全局 Optional + LoadUser，路由级只有 Optional。
// 首页、搜索、详情、播放这些页面都走这条链路，它们才是绝大多数用户的日常路径。
func browsingRouter(store UserReader) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(auth.Optional("secret"), LoadUser(store, "secret", false))
	handler := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	router.GET("/movie/:id", auth.Optional("secret"), handler)
	router.GET("/static/app.css", handler)
	router.GET("/api/proxy/image/:url", handler)
	router.GET("/dashboard", auth.Require("secret", false), handler)
	return router
}

func browse(router *gin.Engine, path, token string) *http.Response {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Result()
}

func signAged(t *testing.T, user *User, age, lifetime time.Duration) string {
	t.Helper()
	now := time.Now()
	token, err := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role,
		Issued: now.Add(-age).Unix(), Expiry: now.Add(lifetime - age).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestLoadUserRenewsSessionOnOptionalOnlyBrowsingPaths(t *testing.T) {
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "viewer@example.com", Username: "viewer", Role: "user"})
	// 72 小时寿命、已过 40 小时：超过半程，应当续期。
	token := signAged(t, user, 40*time.Hour, 72*time.Hour)

	cookies := browse(browsingRouter(store), "/movie/1", token).Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1 sliding renewal cookie", len(cookies))
	}
	claims, err := auth.Parse(cookies[0].Value, "secret", time.Now())
	if err != nil {
		t.Fatalf("parse refreshed token: %v", err)
	}
	if lifetime := claims.Expiry - claims.Issued; lifetime != int64((72 * time.Hour).Seconds()) {
		t.Fatalf("renewed lifetime = %ds, want %ds", lifetime, int64((72 * time.Hour).Seconds()))
	}
	if remaining := time.Until(time.Unix(claims.Expiry, 0)); remaining < 71*time.Hour {
		t.Fatalf("remaining = %v, want a full window", remaining)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie attributes = %+v", cookies[0])
	}
}

func TestLoadUserDoesNotRenewBeforeHalfLife(t *testing.T) {
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "fresh@example.com", Username: "fresh", Role: "user"})
	token := signAged(t, user, time.Hour, 72*time.Hour)

	if cookies := browse(browsingRouter(store), "/movie/1", token).Cookies(); len(cookies) != 0 {
		t.Fatalf("cookies = %d, want no renewal before half life", len(cookies))
	}
}

func TestLoadUserSkipsRenewalForStaticAndImageProxyPaths(t *testing.T) {
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "viewer@example.com", Username: "viewer", Role: "user"})
	token := signAged(t, user, 40*time.Hour, 72*time.Hour)
	router := browsingRouter(store)

	// 这两类路径由 CDN 和浏览器缓存，附带 Set-Cookie 会让缓存整体失效。
	for _, path := range []string{"/static/app.css", "/api/proxy/image/abc"} {
		if cookies := browse(router, path, token).Cookies(); len(cookies) != 0 {
			t.Fatalf("%s set %d cookies, want 0", path, len(cookies))
		}
	}
}

func TestLoadUserRenewsAtMostOncePerRequest(t *testing.T) {
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "member@example.com", Username: "member", Role: "user"})
	token := signAged(t, user, 40*time.Hour, 72*time.Hour)

	// /dashboard 上 LoadUser 和 auth.Require 都会尝试续期，只能下发一次。
	if cookies := browse(browsingRouter(store), "/dashboard", token).Cookies(); len(cookies) != 1 {
		t.Fatalf("cookies = %d, want exactly 1", len(cookies))
	}
}

func TestLoadUserDoesNotRenewDeletedUserSession(t *testing.T) {
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "gone@example.com", Username: "gone", Role: "user"})
	token := signAged(t, user, 40*time.Hour, 72*time.Hour)
	_ = store.Delete(t.Context(), user.ID)

	if cookies := browse(browsingRouter(store), "/movie/1", token).Cookies(); len(cookies) != 0 {
		t.Fatalf("cookies = %d, want no renewal for a deleted user", len(cookies))
	}
}

func TestLoadUserPreventsDeletedUserTokenFromBeingRestored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	user, _ := store.Create(t.Context(), User{Email: "gone@example.com", Username: "gone", Role: "admin"})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	_ = store.Delete(t.Context(), user.ID)

	router := gin.New()
	router.Use(auth.Optional("secret"), LoadUser(store, "secret", false))
	router.GET("/secure", auth.Optional("secret"), auth.Require("secret", false), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/secure", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
