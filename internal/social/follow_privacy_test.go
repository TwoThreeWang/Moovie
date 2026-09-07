package social

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"github.com/TwoThreeWang/Moovie/new/internal/report"
)

func TestFollowNotificationRespectsCurrentProfilePrivacy(t *testing.T) {
	router, users, movies, store, owner, token := socialTestRouter(t)
	report.NewHandler(config.Config{Env: "test", AppSecret: "secret"}, users, movies,
		report.NewPostgresStore(testdb.Pool(t)), nil, store).Register(router)
	actor, err := users.Create(t.Context(), identity.User{Email: "actor@example.com", Username: "关注者", Role: "user", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if response := performRequest(router, http.MethodPost, "/api/users/"+itoa(owner.ID)+"/follow", "", signedToken(t, actor)); response.Code != http.StatusOK {
		t.Fatalf("private actor follow = %d", response.Code)
	}
	notifications, err := store.ListNotifications(t.Context(), owner.ID, 20)
	if err != nil || len(notifications) != 1 {
		t.Fatalf("notifications = %+v/%v", notifications, err)
	}
	endpoint := "/notifications/" + itoa(notifications[0].ID) + "/read"
	if response := performRequest(router, http.MethodPost, endpoint, "", signedToken(t, actor)); response.Code != http.StatusOK || response.Header().Get("HX-Redirect") != "" || !strings.Contains(response.Body.String(), "这条消息已失效或不存在") {
		t.Fatalf("read another user's notification = %d", response.Code)
	}
	if count, err := store.CountUnreadNotifications(t.Context(), owner.ID); err != nil || count != 1 {
		t.Fatalf("unread before owner reads = %d/%v", count, err)
	}
	// 读取时检查最新状态：从未公开、公开、打开列表后关闭主页都要正确处理。
	for _, public := range []bool{false, true, false} {
		if err := users.UpdateIsPublic(t.Context(), actor.ID, public); err != nil {
			t.Fatal(err)
		}
		page := performRequest(router, http.MethodGet, "/notifications", "", token)
		if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "对方主页未公开") == public {
			t.Fatalf("notification privacy hint public=%v: %d/%s", public, page.Code, page.Body.String())
		}
		read := performRequest(router, http.MethodPost, endpoint, "", token)
		if read.Code != http.StatusOK {
			t.Fatalf("read = %d/%s", read.Code, read.Body.String())
		}
		profile := performRequest(router, http.MethodGet, "/user/"+itoa(actor.ID), "", token)
		if public {
			if read.Header().Get("HX-Redirect") != "/user/"+itoa(actor.ID) || profile.Code != http.StatusOK {
				t.Fatalf("public profile redirect = %q/status=%d", read.Header().Get("HX-Redirect"), profile.Code)
			}
		} else if read.Header().Get("HX-Redirect") != "" || read.Header().Get("HX-Retarget") != "#notification-list" ||
			read.Header().Get("HX-Reswap") != "innerHTML" || read.Header().Get("HX-Trigger") != "notificationsChanged" ||
			!strings.Contains(read.Body.String(), "对方主页未公开") || strings.Contains(read.Body.String(), "is-unread") || profile.Code != http.StatusNotFound {
			t.Fatalf("private notification = headers=%v/body=%s/profile=%d", read.Header(), read.Body.String(), profile.Code)
		}
		if count, err := store.CountUnreadNotifications(t.Context(), owner.ID); err != nil || count != 0 {
			t.Fatalf("unread after reading = %d/%v", count, err)
		}
	}
}

func TestFollowingRespectsPrivacyAndAllowsPrivateUnfollow(t *testing.T) {
	router, users, movies, store, viewer, token := socialTestRouter(t)
	target, err := users.Create(t.Context(), identity.User{Email: "target@example.com", Username: "待关注片友", Role: "user", IsPublic: true, Avatar: "🎬", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := movies.Upsert(t.Context(), library.Record{UserID: target.ID, MovieID: "1292052", Title: "私密观影记录", Status: library.StatusWish}); err != nil {
		t.Fatal(err)
	}
	endpoint := "/api/users/" + itoa(target.ID) + "/follow"
	if response := performRequest(router, http.MethodPost, endpoint, "", token); response.Code != http.StatusOK {
		t.Fatalf("follow = %d", response.Code)
	}
	if response := performRequest(router, http.MethodGet, "/feed", "", token); !strings.Contains(response.Body.String(), "私密观影记录") {
		t.Fatal("public activity missing")
	}
	if err := users.UpdateIsPublic(t.Context(), target.ID, false); err != nil {
		t.Fatal(err)
	}
	if response := performRequest(router, http.MethodGet, "/feed", "", token); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "私密观影记录") {
		t.Fatal("private activity leaked")
	}
	managed := performRequest(router, http.MethodGet, "/following", "", token)
	if managed.Code != http.StatusOK || !strings.Contains(managed.Body.String(), "待关注片友") || !strings.Contains(managed.Body.String(), "主页未公开") || strings.Contains(managed.Body.String(), `href="/user/`+itoa(target.ID)+`"`) {
		t.Fatalf("private following management = %d/%s", managed.Code, managed.Body.String())
	}
	if other := performRequest(router, http.MethodGet, "/following", "", signedToken(t, target)); strings.Contains(other.Body.String(), "待关注片友") {
		t.Fatal("another user's following leaked")
	}
	// 无登录不能读取关系，也不能取消；只能修改自己的关系。
	if anonymous := performRequest(router, http.MethodGet, "/following", "", ""); anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d", anonymous.Code)
	}
	if anonymous := performRequest(router, http.MethodDelete, endpoint, "", ""); anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous delete = %d", anonymous.Code)
	}
	if response := performRequest(router, http.MethodDelete, endpoint, "", signedToken(t, target)); response.Code != http.StatusOK {
		t.Fatalf("other delete = %d", response.Code)
	}
	if set, _ := store.FollowingSet(t.Context(), viewer.ID, []int{target.ID}); !set[target.ID] {
		t.Fatal("other user deleted viewer's follow")
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := performRequest(router, http.MethodDelete, endpoint, "", token)
		if response.Code != http.StatusOK || response.Header().Get("HX-Refresh") != "true" {
			t.Fatalf("unfollow = %d", response.Code)
		}
	}
	if set, _ := store.FollowingSet(t.Context(), viewer.ID, []int{target.ID}); set[target.ID] {
		t.Fatal("idempotent unfollow recreated relation")
	}
	if notifications, err := store.ListNotifications(t.Context(), target.ID, 20); err != nil || len(notifications) != 0 {
		t.Fatalf("stale follow notification: %+v/%v", notifications, err)
	}
	if response := performRequest(router, http.MethodPost, endpoint, "", token); response.Code != http.StatusNotFound {
		t.Fatalf("private follow = %d", response.Code)
	}
	// 旧按钮同样可以取消私密用户的已有关系。
	if err := users.UpdateIsPublic(t.Context(), target.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ToggleFollow(t.Context(), viewer.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := users.UpdateIsPublic(t.Context(), target.ID, false); err != nil {
		t.Fatal(err)
	}
	if following, err := store.ToggleFollow(t.Context(), viewer.ID, target.ID); err != nil || following {
		t.Fatalf("private toggle unfollow = %v/%v", following, err)
	}
}

func TestFollowingPaginatesWithoutRequiringActivity(t *testing.T) {
	router, users, _, store, viewer, token := socialTestRouter(t)
	for i := 0; i < feedPageSize+1; i++ {
		user, err := users.Create(t.Context(), identity.User{Email: "follow" + itoa(i) + "@example.com", Username: "片友" + itoa(i), Role: "user", IsPublic: true, CreatedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ToggleFollow(t.Context(), viewer.ID, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	first := performRequest(router, http.MethodGet, "/following", "", token).Body.String()
	second := performRequest(router, http.MethodGet, "/following?page=2", "", token).Body.String()
	if strings.Count(first, `class="following-person"`) != feedPageSize || !strings.Contains(first, `href="/following?page=2"`) {
		t.Fatal("first following page invalid")
	}
	if strings.Count(second, `class="following-person"`) != 1 || !strings.Contains(second, `href="/following?page=1"`) || strings.Contains(second, "下一页") {
		t.Fatal("second following page invalid")
	}
}
