package douban

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func TestBindStatusAndUnbindPreserveLegacyRoutes(t *testing.T) {
	router, users, jobs, validator, coordinator, token := doubanTestRouter(t)
	bound := doubanForm(router, token, "/dashboard/settings/douban/bind", url.Values{"douban_user_id": {"https://www.douban.com/people/198878447/"}})
	if bound.Code != http.StatusFound || bound.Header().Get("Location") != "/dashboard/settings?success=douban_bind" || validator.userID != "198878447" || coordinator.created != 1 {
		t.Fatalf("bind = %d/%q validator=%q created=%d", bound.Code, bound.Header().Get("Location"), validator.userID, coordinator.created)
	}
	user, _ := users.FindByID(t.Context(), 1)
	if user.DoubanUserID != "198878447" {
		t.Fatalf("bound user = %+v", user)
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/htmx/douban-sync-status", nil)
	statusRequest.AddCookie(&http.Cookie{Name: "token", Value: token})
	status := httptest.NewRecorder()
	router.ServeHTTP(status, statusRequest)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), "同步任务排队中") {
		t.Fatalf("status = %d/%s", status.Code, status.Body.String())
	}
	unbound := doubanForm(router, token, "/dashboard/settings/douban/unbind", nil)
	if unbound.Code != http.StatusFound || unbound.Header().Get("Location") != "/dashboard/settings?success=douban_unbind" {
		t.Fatalf("unbind = %d/%q", unbound.Code, unbound.Header().Get("Location"))
	}
	user, _ = users.FindByID(t.Context(), 1)
	if user.DoubanUserID != "" {
		t.Fatalf("unbound user = %+v", user)
	}
	if latest, _ := jobs.LatestByUser(t.Context(), 1); latest == nil {
		t.Fatal("binding did not persist a sync job")
	}
}

func TestBindRejectsInvalidIDWithoutCallingProvider(t *testing.T) {
	router, _, _, validator, _, token := doubanTestRouter(t)
	response := doubanForm(router, token, "/dashboard/settings/douban/bind", url.Values{"douban_user_id": {"https://example.com/not-douban"}})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "请输入有效的豆瓣用户 ID 或主页链接") || validator.userID != "" {
		t.Fatalf("invalid bind = %d/%s validator=%q", response.Code, response.Body.String(), validator.userID)
	}
}

func doubanTestRouter(t *testing.T) (*gin.Engine, *identity.PostgresStore, *QueueJobStore, *recordingValidator, *recordingCoordinator, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewPostgresStore(testdb.Pool(t))
	user, _ := users.Create(t.Context(), identity.User{Email: "person@example.com", Username: "person", PasswordHash: "hash", Role: "user", Avatar: "🎬", CreatedAt: time.Now()})
	jobs := NewQueueJobStore(workqueue.NewPostgresStore(testdb.Pool(t)))
	validator := &recordingValidator{}
	coordinator := &recordingCoordinator{jobs: jobs}
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"settings"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(cfg, users, jobs, validator, coordinator).Register(router)
	now := time.Now()
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, users, jobs, validator, coordinator, token
}

type recordingValidator struct{ userID string }

func (validator *recordingValidator) ValidateUser(_ context.Context, userID string) error {
	validator.userID = userID
	return nil
}

type recordingCoordinator struct {
	jobs    *QueueJobStore
	created int
}

func (coordinator *recordingCoordinator) CreateFull(ctx context.Context, userID int) (int, error) {
	coordinator.created++
	job, err := coordinator.jobs.Create(ctx, userID, TypeFull)
	return job.ID, err
}

func doubanForm(router http.Handler, token, path string, values url.Values) *httptest.ResponseRecorder {
	encoded := ""
	if values != nil {
		encoded = values.Encode()
	}
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(encoded))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}
