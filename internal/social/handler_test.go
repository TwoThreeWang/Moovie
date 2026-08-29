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
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
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
	privateUser, privateErr := users.Create(t.Context(), identity.User{Email: "private@example.com", Username: "私密用户", Role: "user", Avatar: "🍿", CreatedAt: now})
	if privateErr != nil {
		t.Fatalf("create private user: %v", privateErr)
	}
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

func TestInteractionNotificationsAggregateLikesAndTrackReadState(t *testing.T) {
	router, users, movies, store, owner, ownerToken := socialTestRouter(t)
	now := time.Now()
	if err := movies.Upsert(t.Context(), library.Record{UserID: owner.ID, MovieID: "1292052", Title: "肖申克的救赎", Status: library.StatusWatched, Comment: "值得重看", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	comment, _ := movies.GetByUserAndMovie(t.Context(), owner.ID, "1292052")

	actor, _ := users.Create(t.Context(), identity.User{Email: "actor@example.com", Username: "片友甲", Role: "user", Avatar: "🍿", IsPublic: true, CreatedAt: now})
	secondActor, _ := users.Create(t.Context(), identity.User{Email: "actor2@example.com", Username: "片友乙", Role: "user", Avatar: "📽️", IsPublic: true, CreatedAt: now})
	actorToken := signedToken(t, actor)
	secondActorToken := signedToken(t, secondActor)

	performRequest(router, http.MethodPost, "/api/comments/"+itoa(comment.ID)+"/like", "", actorToken)
	performRequest(router, http.MethodPost, "/api/comments/"+itoa(comment.ID)+"/like", "", secondActorToken)
	count, _ := store.CountUnreadNotifications(t.Context(), owner.ID)
	if count != 1 {
		t.Fatalf("aggregated unread likes = %d, want 1", count)
	}
	page := performRequest(router, http.MethodGet, "/notifications", "", ownerToken)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "等 2 人") || !strings.Contains(page.Body.String(), "肖申克的救赎") || !strings.Contains(page.Body.String(), `hx-delete="/notifications/`) {
		t.Fatalf("notifications page = %d/%s", page.Code, page.Body.String())
	}

	performRequest(router, http.MethodPost, "/api/comments/"+itoa(comment.ID)+"/like", "", actorToken)
	notifications, _ := store.ListNotifications(t.Context(), owner.ID, 50)
	if len(notifications) != 1 || notifications[0].ActorCount != 1 {
		t.Fatalf("notifications after unlike = %+v", notifications)
	}

	replied := performRequest(router, http.MethodPost, "/api/comments/"+itoa(comment.ID)+"/replies", url.Values{"content": {"我也这么觉得"}}.Encode(), actorToken)
	if replied.Code != http.StatusOK {
		t.Fatalf("reply = %d/%s", replied.Code, replied.Body.String())
	}
	notifications, _ = store.ListNotifications(t.Context(), owner.ID, 50)
	if len(notifications) != 2 || notifications[0].Type != "comment_reply" || notifications[0].Content != "我也这么觉得" {
		t.Fatalf("notifications after reply = %+v", notifications)
	}
	replyNotificationID := notifications[0].ID
	likeNotificationID := notifications[1].ID
	forbiddenDelete := performRequest(router, http.MethodDelete, "/notifications/"+itoa(replyNotificationID), "", actorToken)
	if forbiddenDelete.Code != http.StatusNotFound {
		t.Fatalf("delete another user's notification = %d, want 404", forbiddenDelete.Code)
	}
	read := performRequest(router, http.MethodPost, "/notifications/"+itoa(notifications[0].ID)+"/read", "", ownerToken)
	if read.Code != http.StatusOK || read.Header().Get("HX-Redirect") != "/movie/1292052?comment="+itoa(comment.ID)+"#comment-"+itoa(comment.ID) {
		t.Fatalf("read redirect = %d/%q", read.Code, read.Header().Get("HX-Redirect"))
	}
	count, _ = store.CountUnreadNotifications(t.Context(), owner.ID)
	if count != 1 {
		t.Fatalf("unread after reading reply = %d, want 1", count)
	}
	readAll := performRequest(router, http.MethodPost, "/notifications/read-all", "", ownerToken)
	count, _ = store.CountUnreadNotifications(t.Context(), owner.ID)
	if readAll.Code != http.StatusOK || count != 0 || readAll.Header().Get("HX-Trigger") != "notificationsChanged" {
		t.Fatalf("read all = %d/count=%d/trigger=%q", readAll.Code, count, readAll.Header().Get("HX-Trigger"))
	}
	deletedReply := performRequest(router, http.MethodDelete, "/notifications/"+itoa(replyNotificationID), "", ownerToken)
	notifications, _ = store.ListNotifications(t.Context(), owner.ID, 50)
	if deletedReply.Code != http.StatusOK || deletedReply.Header().Get("HX-Trigger") != "notificationsChanged" || len(notifications) != 1 || notifications[0].Type != "comment_like" {
		t.Fatalf("delete reply notification = %d/%q/%+v", deletedReply.Code, deletedReply.Header().Get("HX-Trigger"), notifications)
	}
	deletedLikes := performRequest(router, http.MethodDelete, "/notifications/"+itoa(likeNotificationID), "", ownerToken)
	notifications, _ = store.ListNotifications(t.Context(), owner.ID, 50)
	likeCounts, _ := store.CountLikes(t.Context(), []int{comment.ID})
	replies, _ := store.ListReplies(t.Context(), comment.ID)
	if deletedLikes.Code != http.StatusOK || len(notifications) != 0 || likeCounts[comment.ID] != 1 || len(replies) != 1 || !strings.Contains(deletedLikes.Body.String(), "还没有互动消息") {
		t.Fatalf("hard delete notifications = %d/notifications=%+v/likes=%d/replies=%d", deletedLikes.Code, notifications, likeCounts[comment.ID], len(replies))
	}

	performRequest(router, http.MethodPost, "/api/comments/"+itoa(comment.ID)+"/like", "", ownerToken)
	performRequest(router, http.MethodPost, "/api/comments/"+itoa(comment.ID)+"/replies", "content=self", ownerToken)
	count, _ = store.CountUnreadNotifications(t.Context(), owner.ID)
	if count != 0 {
		t.Fatalf("self interactions created notifications: %d", count)
	}
}

func socialTestRouter(t *testing.T) (*gin.Engine, *identity.PostgresStore, *library.PostgresStore, *PostgresStore, *identity.User, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := identity.NewPostgresStore(testdb.Pool(t))
	publicUser, _ := users.Create(t.Context(), identity.User{Email: "public@example.com", Username: "公开用户", Role: "user", Avatar: "🎬", IsPublic: true, CreatedAt: time.Now()})
	movies := library.NewPostgresStore(testdb.Pool(t))
	store := NewPostgresStore(testdb.Pool(t))
	cfg := config.Config{Env: "test", SiteName: "Moovie影牛", SiteURL: "https://moovie.example", AppSecret: "secret"}
	renderer, err := platformweb.LoadRenderer(filepath.Join("..", "..", "web", "templates"), []string{"cinema", "notifications"})
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

func signedToken(t *testing.T, user *identity.User) string {
	t.Helper()
	now := time.Now()
	token, err := auth.Sign(auth.Claims{UserID: user.ID, Email: user.Email, Role: user.Role, Issued: now.Unix(), Expiry: now.Add(time.Hour).Unix()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	return token
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
