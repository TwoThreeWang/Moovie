package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/pow"
	"golang.org/x/crypto/bcrypt"
)

// fixedClockIssuer 返回时钟由测试手动拨动的签发器，用来测「提交过快」。
func fixedClockIssuer(t *testing.T) (*pow.Issuer, *time.Time) {
	t.Helper()
	issuer, err := pow.NewIssuer(4)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	issuer.SetClock(func() time.Time { return now })
	return issuer, &now
}

// fakeDomains 把指定域名当成收不了信。
type fakeDomains map[string]bool

func (domains fakeDomains) CanReceive(_ context.Context, address string) bool {
	return !domains[address[strings.LastIndex(address, "@")+1:]]
}

// countingStore 记录 FindByEmail 调用次数，验证人机校验发生在查库之前。
type countingStore struct {
	Store
	lookups atomic.Int32
}

func (store *countingStore) FindByEmail(ctx context.Context, email string) (*User, error) {
	store.lookups.Add(1)
	return store.Store.FindByEmail(ctx, email)
}

func getPage(router http.Handler, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func TestAuthPagesAlwaysIssueFreshUncachedChallenges(t *testing.T) {
	router, _, _ := identityTestRouter(t, "test")
	for _, path := range []string{"/auth/login", "/auth/register"} {
		first, second := getPage(router, path), getPage(router, path)
		if challengeFrom(t, first.Body.String()) == challengeFrom(t, second.Body.String()) {
			t.Fatalf("%s reused a challenge", path)
		}
		if first.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s cache-control = %q", path, first.Header().Get("Cache-Control"))
		}
		body := first.Body.String()
		for _, snippet := range []string{`name="pow_nonce"`, `name="mv_extra_note" value="" autocomplete="off" tabindex="-1"`, `aria-hidden="true"`, `/static/js/pow.js`} {
			if !strings.Contains(body, snippet) {
				t.Fatalf("%s missing %q", path, snippet)
			}
		}
	}
	// 出错重新渲染也必须带新挑战，并保留已填的邮箱。
	failed := postSolvedForm(t, router, "/auth/login", url.Values{"email": {"nobody@example.com"}, "password": {"x"}})
	if challengeFrom(t, failed.Body.String()) == "" || !strings.Contains(failed.Body.String(), `value="nobody@example.com"`) {
		t.Fatalf("login error page = %s", failed.Body.String())
	}
}

func TestHumanCheckRejectsBotSubmissionsBeforeTouchingTheDatabase(t *testing.T) {
	issuer, now := fixedClockIssuer(t)
	_, postgres, _ := identityTestRouter(t, "test")
	// 用计数 store 装路由，确保失败的提交一次库都没查。
	counting := &countingStore{Store: postgres}
	router := buildIdentityRouter(t, "test", counting, WithHumanCheck(issuer))

	hash, _ := bcrypt.GenerateFromPassword([]byte("secret1"), bcrypt.MinCost)
	_, _ = postgres.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: string(hash), Role: "user", CreatedAt: *now})
	credentials := url.Values{"email": {"person@example.com"}, "password": {"secret1"}}

	loginToken := challengeFrom(t, getPage(router, "/auth/login").Body.String())
	registerToken := challengeFrom(t, getPage(router, "/auth/register").Body.String())
	loginNonce := pow.Solve(loginToken)
	with := func(extra map[string]string) url.Values {
		values := url.Values{}
		for key, list := range credentials {
			values[key] = list
		}
		for key, value := range extra {
			values.Set(key, value)
		}
		return values
	}
	assertRejected := func(name string, response *httptest.ResponseRecorder) {
		t.Helper()
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), messageHumanCheckFailed) {
			t.Fatalf("%s = %d/%s", name, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `value="person@example.com"`) {
			t.Fatalf("%s did not keep the email", name)
		}
		challengeFrom(t, response.Body.String())
		if lookups := counting.lookups.Load(); lookups != 0 {
			t.Fatalf("%s queried the database %d times", name, lookups)
		}
	}

	// 页面打开不足 2 秒就提交，视为脚本。
	*now = now.Add(time.Second)
	assertRejected("too fast", postForm(router, "/auth/login", with(map[string]string{pow.TokenField: loginToken, pow.NonceField: loginNonce})))
	*now = now.Add(5 * time.Second)
	assertRejected("missing nonce", postForm(router, "/auth/login", with(map[string]string{pow.TokenField: loginToken})))
	assertRejected("missing challenge", postForm(router, "/auth/login", with(nil)))
	assertRejected("wrong nonce", postForm(router, "/auth/login", with(map[string]string{pow.TokenField: loginToken, pow.NonceField: unsolvedNonce(loginToken)})))
	assertRejected("register challenge on login", postForm(router, "/auth/login", with(map[string]string{pow.TokenField: registerToken, pow.NonceField: pow.Solve(registerToken)})))
	assertRejected("trap filled", postForm(router, "/auth/login", with(map[string]string{pow.TokenField: loginToken, pow.NonceField: loginNonce, pow.TrapField: "https://spam.example"})))

	// 以上失败都没有消耗挑战，正确提交可以登录；同一挑战再交一次就是重放。
	accepted := postForm(router, "/auth/login", with(map[string]string{pow.TokenField: loginToken, pow.NonceField: loginNonce}))
	if accepted.Code != http.StatusFound {
		t.Fatalf("valid login = %d/%s", accepted.Code, accepted.Body.String())
	}
	counting.lookups.Store(0)
	assertRejected("replay", postForm(router, "/auth/login", with(map[string]string{pow.TokenField: loginToken, pow.NonceField: loginNonce})))
}

func TestRegisterChecksMailDomainAndHidesExistingAccounts(t *testing.T) {
	router, store, now := identityTestRouterWithOptions(t, "test", WithEmailDomainChecker(fakeDomains{"gmial.con": true}))
	_, _ = store.Create(t.Context(), User{Email: "taken@example.com", Username: "taken", PasswordHash: "x", Role: "user", CreatedAt: now})
	form := func(email string) url.Values {
		return url.Values{"email": {email}, "password": {"secret1"}, "confirm_password": {"secret1"}}
	}

	typo := postSolvedForm(t, router, "/auth/register", form("person@gmial.con"))
	if !strings.Contains(typo.Body.String(), messageDomainCannotMail) || !strings.Contains(typo.Body.String(), `value="person@gmial.con"`) {
		t.Fatalf("typo domain = %d/%s", typo.Code, typo.Body.String())
	}
	if user, _ := store.FindByEmail(t.Context(), "person@gmial.con"); user != nil {
		t.Fatal("account created for undeliverable domain")
	}
	// 取消勾选「公开主页」后出错，重新渲染时保持不勾选。
	unchecked := postSolvedForm(t, router, "/auth/register", form("person@gmial.con"))
	if strings.Contains(unchecked.Body.String(), `name="is_public" checked`) {
		t.Fatal("unchecked is_public came back checked")
	}

	taken := postSolvedForm(t, router, "/auth/register", form("taken@example.com"))
	body := taken.Body.String()
	if !strings.Contains(body, messageRegisterUnavailable) || strings.Contains(body, "已被注册") {
		t.Fatalf("taken email = %d/%s", taken.Code, body)
	}
}

func TestLoginComparesAgainstDummyHashForUnknownAccounts(t *testing.T) {
	cost, err := bcrypt.Cost(dummyPasswordHash())
	if err != nil || cost != bcrypt.DefaultCost {
		t.Fatalf("dummy hash cost = %d/%v, want %d", cost, err, bcrypt.DefaultCost)
	}
	router, _, _ := identityTestRouter(t, "test")
	unknown := postSolvedForm(t, router, "/auth/login", url.Values{"email": {"ghost@example.com"}, "password": {"moovie-timing-equalizer"}})
	if unknown.Code != http.StatusOK || !strings.Contains(unknown.Body.String(), "邮箱或密码错误") {
		t.Fatalf("unknown account with dummy password = %d/%s", unknown.Code, unknown.Body.String())
	}
}

func TestUpdateEmailRequiresPasswordValidDomainAndHidesOwner(t *testing.T) {
	router, store, now := identityTestRouterWithOptions(t, "test", WithEmailDomainChecker(fakeDomains{"gmial.con": true}))
	hash, _ := bcrypt.GenerateFromPassword([]byte("secret1"), bcrypt.MinCost)
	user, _ := store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: string(hash), Role: "user", Avatar: "🎬", CreatedAt: now})
	_, _ = store.Create(t.Context(), User{Email: "other@example.com", Username: "other", PasswordHash: "x", Role: "user", CreatedAt: now})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")

	for name, testCase := range map[string]struct {
		values url.Values
		want   string
	}{
		"no password":    {url.Values{"email": {"new@example.com"}}, "当前密码错误"},
		"wrong password": {url.Values{"email": {"new@example.com"}, "current_password": {"wrong"}}, "当前密码错误"},
		"weak format":    {url.Values{"email": {"a@b@c"}, "current_password": {"secret1"}}, "请输入有效的邮箱地址"},
		"typo domain":    {url.Values{"email": {"new@gmial.con"}, "current_password": {"secret1"}}, messageDomainCannotMail},
		"owned by other": {url.Values{"email": {"other@example.com"}, "current_password": {"secret1"}}, messageEmailUnavailable},
	} {
		response := authenticatedForm(router, token, "/dashboard/settings/email", testCase.values)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), testCase.want) {
			t.Fatalf("%s = %d/%s", name, response.Code, response.Body.String())
		}
	}
	if stored, _ := store.FindByID(t.Context(), user.ID); stored.Email != "person@example.com" {
		t.Fatalf("email changed by rejected request: %s", stored.Email)
	}

	updated := authenticatedForm(router, token, "/dashboard/settings/email", url.Values{"email": {"new@example.com"}, "current_password": {"secret1"}})
	if updated.Code != http.StatusFound || updated.Header().Get("Location") != "/dashboard/settings?success=email" {
		t.Fatalf("valid update = %d/%q", updated.Code, updated.Header().Get("Location"))
	}
	if stored, _ := store.FindByID(t.Context(), user.ID); stored.Email != "new@example.com" {
		t.Fatalf("email = %s", stored.Email)
	}
}

func TestSettingsEmailFormAsksForCurrentPassword(t *testing.T) {
	router, store, now := identityTestRouter(t, "test")
	user, _ := store.Create(t.Context(), User{Email: "person@example.com", Username: "person", PasswordHash: "x", Role: "user", Avatar: "🎬", CreatedAt: now})
	token, _ := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: time.Now().Unix(), Expiry: time.Now().Add(time.Hour).Unix()}, "secret")
	request := httptest.NewRequest(http.MethodGet, "/dashboard/settings", nil)
	request.AddCookie(&http.Cookie{Name: "token", Value: token})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	body := recorder.Body.String()
	start := strings.Index(body, `action="/dashboard/settings/email"`)
	end := strings.Index(body[start:], "</form>")
	if start < 0 || end < 0 || !strings.Contains(body[start:start+end], `name="current_password"`) {
		t.Fatalf("email form lacks current_password field: %s", body)
	}
}

// unsolvedNonce 返回一个不满足难度的 nonce。
func unsolvedNonce(token string) string {
	salt := strings.Split(token, ".")[3]
	for nonce := 0; ; nonce++ {
		candidate := "9" + strings.Repeat("0", nonce%5) + string(rune('0'+nonce%10))
		if !pow.Satisfies(salt, candidate, 4) {
			return candidate
		}
	}
}
