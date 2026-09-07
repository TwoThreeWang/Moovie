package social

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
)

func TestNotificationsOpenOlderCommentWithReplies(t *testing.T) {
	router, users, movies, store, owner, token := socialTestRouter(t)
	now := time.Now()
	if err := movies.Upsert(t.Context(), library.Record{UserID: owner.ID, MovieID: "1292052", Title: "影片", Status: library.StatusWatched, Comment: "较早的原短评", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	comment, err := movies.GetByUserAndMovie(t.Context(), owner.ID, "1292052")
	if err != nil || comment == nil {
		t.Fatalf("comment = %+v/%v", comment, err)
	}
	var actor *identity.User
	for i := 0; i < 10; i++ {
		actor, err = users.Create(t.Context(), identity.User{Email: "newer" + itoa(i) + "@example.com", Username: "片友" + itoa(i), Role: "user", CreatedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		if err := movies.Upsert(t.Context(), library.Record{UserID: actor.ID, MovieID: "1292052", Status: library.StatusWatched, Comment: "较新的短评", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	list := performRequest(router, http.MethodGet, "/api/htmx/movie-comments?douban_id=1292052", "", token)
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), "较早的原短评") {
		t.Fatal("fixture must put original comment outside latest ten")
	}
	if _, _, err := store.ToggleLike(t.Context(), comment.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateReply(t.Context(), comment.ID, actor.ID, "这条回复应该直接可见"); err != nil {
		t.Fatal(err)
	}
	notifications, err := store.ListNotifications(t.Context(), owner.ID, 50)
	if err != nil || len(notifications) != 2 {
		t.Fatalf("notifications = %+v/%v", notifications, err)
	}
	for _, notification := range notifications {
		read := performRequest(router, http.MethodPost, "/notifications/"+itoa(notification.ID)+"/read", "", token)
		if read.Code != http.StatusOK || read.Header().Get("HX-Redirect") != "/review/"+itoa(comment.ID) {
			t.Fatalf("%s redirect = %d/%v", notification.Type, read.Code, read.Header())
		}
		page := performRequest(router, http.MethodGet, read.Header().Get("HX-Redirect"), "", token)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "较早的原短评") || !strings.Contains(page.Body.String(), "这条回复应该直接可见") {
			t.Fatalf("%s review = %d/%s", notification.Type, page.Code, page.Body.String())
		}
	}
}

func TestUnavailableNotificationTargetsStayInMessageList(t *testing.T) {
	router, users, movies, store, owner, token := socialTestRouter(t)
	actor, err := users.Create(t.Context(), identity.User{Email: "actor@example.com", Username: "片友", Role: "user", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := movies.Upsert(t.Context(), library.Record{UserID: owner.ID, MovieID: "1292052", Title: "影片", Status: library.StatusWatched, Comment: "将被清空的短评"}); err != nil {
		t.Fatal(err)
	}
	comment, err := movies.GetByUserAndMovie(t.Context(), owner.ID, "1292052")
	if err != nil || comment == nil {
		t.Fatalf("comment = %+v/%v", comment, err)
	}
	if _, _, err := store.ToggleLike(t.Context(), comment.ID, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateReply(t.Context(), comment.ID, actor.ID, "回复内容"); err != nil {
		t.Fatal(err)
	}
	notifications, err := store.ListNotifications(t.Context(), owner.ID, 50)
	if err != nil || len(notifications) != 2 {
		t.Fatalf("notifications = %+v/%v", notifications, err)
	}
	if err := movies.UpdateRatingComment(t.Context(), owner.ID, comment.ID, 0, "   "); err != nil {
		t.Fatal(err)
	}
	for _, notification := range notifications {
		read := performRequest(router, http.MethodPost, "/notifications/"+itoa(notification.ID)+"/read", "", token)
		if read.Code != http.StatusOK || read.Header().Get("HX-Redirect") != "" || read.Header().Get("HX-Retarget") != "#notification-list" || read.Header().Get("HX-Reswap") != "innerHTML" || read.Header().Get("HX-Trigger") != "notificationsChanged" || !strings.Contains(read.Body.String(), "原短评已清空或不可用") {
			t.Fatalf("%s cleared comment = %d/%v/%s", notification.Type, read.Code, read.Header(), read.Body.String())
		}
	}
	if count, err := store.CountUnreadNotifications(t.Context(), owner.ID); err != nil || count != 0 {
		t.Fatalf("unread after unavailable reads = %d/%v", count, err)
	}
	// 删除原记录会级联删除两类通知，模拟消息列表打开后目标失效。
	if err := movies.Remove(t.Context(), owner.ID, "1292052"); err != nil {
		t.Fatal(err)
	}
	for _, notification := range notifications {
		read := performRequest(router, http.MethodPost, "/notifications/"+itoa(notification.ID)+"/read", "", token)
		if read.Code != http.StatusOK || read.Header().Get("HX-Redirect") != "" || read.Header().Get("HX-Retarget") != "#notification-list" || read.Header().Get("HX-Reswap") != "innerHTML" || read.Header().Get("HX-Trigger") != "notificationsChanged" || !strings.Contains(read.Body.String(), "这条消息已失效或不存在") || !strings.Contains(read.Body.String(), "还没有互动消息") || strings.Contains(read.Body.String(), `class="notification-open"`) {
			t.Fatalf("%s stale notification = %d/%v/%s", notification.Type, read.Code, read.Header(), read.Body.String())
		}
	}
}
