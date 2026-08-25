package admin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/feedback"
	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/gin-gonic/gin"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

type jobRetrierStub struct {
	jobID    int
	taskType string
	limit    int
	retried  int
	err      error
}

func (stub *jobRetrierStub) RetryJob(_ context.Context, jobID int) (int, error) {
	stub.jobID = jobID
	return stub.retried, stub.err
}

func (stub *jobRetrierStub) RetryFailed(_ context.Context, taskType string, limit int) (int, error) {
	stub.taskType, stub.limit = taskType, limit
	return stub.retried, stub.err
}

func jobRetryRouter(t *testing.T, retrier JobRetrier) (*gin.Engine, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewPostgresStore(testdb.Pool(t))
	_, _ = users.Create(t.Context(), identity.User{Email: "admin@example.com", Username: "admin", Role: "admin", CreatedAt: time.Now()})
	_, _ = users.Create(t.Context(), identity.User{Email: "user@example.com", Username: "user", Role: "user", CreatedAt: time.Now()})
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"admin_jobs"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	options := []HandlerOption{WithMetricsReader(adminMetricsStub{})}
	if retrier != nil {
		options = append(options, WithJobRetrier(retrier))
	}
	NewHandler(cfg, users, search.NewPostgresStore(testdb.Pool(t)), catalog.NewPostgresStore(testdb.Pool(t)), feedback.NewPostgresStore(testdb.Pool(t)),
		crawlerStub{}, nil, options...).Register(router)
	now := time.Now()
	adminToken, _ := auth.Sign(auth.Claims{UserID: 1, Role: "admin", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	userToken, _ := auth.Sign(auth.Claims{UserID: 2, Role: "user", Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	return router, adminToken, userToken
}

func TestJobRetryRequiresAdminAndForwardsTheJobID(t *testing.T) {
	retrier := &jobRetrierStub{retried: 1}
	router, adminToken, userToken := jobRetryRouter(t, retrier)

	forbidden := formRequest(router, http.MethodPost, "/admin/jobs/retry", url.Values{"job_id": {"12"}}, userToken)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin retry = %d", forbidden.Code)
	}
	if retrier.jobID != 0 {
		t.Fatalf("non-admin request reached the store: %+v", retrier)
	}
	response := formRequest(router, http.MethodPost, "/admin/jobs/retry", url.Values{"job_id": {"12"}}, adminToken)
	if response.Code != http.StatusOK || retrier.jobID != 12 {
		t.Fatalf("retry = %d/%s (job %d)", response.Code, response.Body.String(), retrier.jobID)
	}
}

func TestJobRetryReportsWhenNothingWasRestored(t *testing.T) {
	// 恢复 0 条不是错误，而是「已经不是失败状态，或同一对象已有任务排队」。
	router, adminToken, _ := jobRetryRouter(t, &jobRetrierStub{retried: 0})
	response := formRequest(router, http.MethodPost, "/admin/jobs/retry", url.Values{"job_id": {"12"}}, adminToken)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "已有任务在队列中") {
		t.Fatalf("empty retry = %d/%s", response.Code, response.Body.String())
	}
}

func TestJobRetryRejectsInvalidInputAndSurfacesStoreErrors(t *testing.T) {
	retrier := &jobRetrierStub{err: errors.New("database is down")}
	router, adminToken, _ := jobRetryRouter(t, retrier)
	for _, jobID := range []string{"", "0", "-1", "abc"} {
		response := formRequest(router, http.MethodPost, "/admin/jobs/retry", url.Values{"job_id": {jobID}}, adminToken)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("job_id %q = %d", jobID, response.Code)
		}
	}
	failure := formRequest(router, http.MethodPost, "/admin/jobs/retry", url.Values{"job_id": {"12"}}, adminToken)
	if failure.Code != http.StatusInternalServerError || strings.Contains(failure.Body.String(), "database is down") {
		t.Fatalf("store error must not leak internals: %d/%s", failure.Code, failure.Body.String())
	}
	invalidType := formRequest(router, http.MethodPost, "/admin/jobs/retry-failed",
		url.Values{"task_type": {"tmdb; DROP TABLE"}}, adminToken)
	if invalidType.Code != http.StatusBadRequest {
		t.Fatalf("invalid task type = %d", invalidType.Code)
	}
}

func TestJobRetryFailedAppliesDefaultLimitAndTaskTypeFilter(t *testing.T) {
	retrier := &jobRetrierStub{retried: 37}
	router, adminToken, _ := jobRetryRouter(t, retrier)

	response := formRequest(router, http.MethodPost, "/admin/jobs/retry-failed", url.Values{"task_type": {"tmdb"}}, adminToken)
	if response.Code != http.StatusOK || retrier.taskType != "tmdb" || retrier.limit != 500 {
		t.Fatalf("bulk retry = %d/%s (type %q limit %d)", response.Code, response.Body.String(), retrier.taskType, retrier.limit)
	}
	if !strings.Contains(response.Body.String(), "37") {
		t.Fatalf("response should report how many jobs were restored: %s", response.Body.String())
	}
	// 空类型表示不限类型，是合法输入。
	all := formRequest(router, http.MethodPost, "/admin/jobs/retry-failed", url.Values{"limit": {"20"}}, adminToken)
	if all.Code != http.StatusOK || retrier.taskType != "" || retrier.limit != 20 {
		t.Fatalf("untyped retry = %d (type %q limit %d)", all.Code, retrier.taskType, retrier.limit)
	}
	if over := formRequest(router, http.MethodPost, "/admin/jobs/retry-failed", url.Values{"limit": {"5000"}}, adminToken); over.Code != http.StatusBadRequest {
		t.Fatalf("limit ceiling not enforced: %d", over.Code)
	}
}

func TestJobRetryIsUnavailableWithoutAQueueStore(t *testing.T) {
	router, adminToken, _ := jobRetryRouter(t, nil)
	response := formRequest(router, http.MethodPost, "/admin/jobs/retry", url.Values{"job_id": {"12"}}, adminToken)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing retrier = %d/%s", response.Code, response.Body.String())
	}
}
