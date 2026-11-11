package feedback

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/pow"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

func TestFeedbackPageIssuesChallengeAndRejectsBots(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewPostgresStore(testdb.Pool(t))
	issuer, err := pow.NewIssuer(4)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	issuer.SetClock(func() time.Time { return now })
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"feedback"})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.HTMLRender = renderer
	NewHandler(config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}, store, WithHumanCheck(issuer)).Register(router)

	page := request(router, http.MethodGet, "/feedback", "")
	body := page.Body.String()
	for _, snippet := range []string{`name="pow_nonce"`, `name="mv_extra_note"`, `/static/js/pow.js`, `<option value="bug" selected>`} {
		if !strings.Contains(body, snippet) {
			t.Fatalf("feedback page missing %q", snippet)
		}
	}
	if page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", page.Header().Get("Cache-Control"))
	}
	token := challengeFrom(t, body)
	nonce := pow.Solve(token)
	foreign, _ := issuer.Issue("login")
	submit := func(extra map[string]string) string {
		values := url.Values{"type": {"bug"}, "content": {"广告内容"}}
		for key, value := range extra {
			values.Set(key, value)
		}
		return formRequest(router, http.MethodPost, "/api/feedback", values, "").Body.String()
	}
	rejected := func(name, response string) {
		t.Helper()
		if !strings.Contains(response, "安全校验失败") || !strings.Contains(response, ">广告内容</textarea>") || challengeFrom(t, response) == token {
			t.Fatalf("%s not rejected with a fresh form: %s", name, response)
		}
	}

	now = now.Add(time.Second)
	rejected("too fast", submit(map[string]string{pow.TokenField: token, pow.NonceField: nonce}))
	now = now.Add(5 * time.Second)
	rejected("missing nonce", submit(map[string]string{pow.TokenField: token}))
	rejected("bad nonce", submit(map[string]string{pow.TokenField: token, pow.NonceField: "x1"}))
	rejected("login challenge", submit(map[string]string{pow.TokenField: foreign, pow.NonceField: pow.Solve(foreign)}))
	rejected("trap", submit(map[string]string{pow.TokenField: token, pow.NonceField: nonce, pow.TrapField: "1"}))
	if count, _ := store.CountPublic(t.Context(), ""); count != 0 {
		t.Fatalf("rejected submissions were stored: %d", count)
	}

	if accepted := submit(map[string]string{pow.TokenField: token, pow.NonceField: nonce}); !strings.Contains(accepted, "感谢您的反馈") {
		t.Fatalf("valid submit = %s", accepted)
	}
	rejected("replay", submit(map[string]string{pow.TokenField: token, pow.NonceField: nonce}))
	if count, _ := store.CountPublic(t.Context(), ""); count != 1 {
		t.Fatalf("stored feedback = %d, want 1", count)
	}
}
