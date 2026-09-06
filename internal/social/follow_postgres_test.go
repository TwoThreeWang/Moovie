package social

import (
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
)

// TestFollowAndFeedRunAgainstPostgres 让关注和动态流的 SQL 真的在库上跑一遍。
// 这几条语句里有 CTE、ANY($n) 和多表 JOIN，靠字符串断言证明不了它们能执行。
func TestFollowAndFeedRunAgainstPostgres(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.User(t, pool, 1, 2, 3)
	store := NewPostgresStore(pool)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `UPDATE users SET is_public = TRUE WHERE id IN (2, 3)`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO user_movies (id, user_id, movie_id, title, status, rating, comment)
VALUES (11, 2, '1292052', '肖申克的救赎', 'watched', 5, '希望是好东西'),
       (12, 3, '1291546', '霸王别姬', 'wish', 0, '')`); err != nil {
		t.Fatalf("插入短评: %v", err)
	}

	// 关注。
	following, err := store.ToggleFollow(ctx, 1, 2)
	if err != nil || !following {
		t.Fatalf("首次关注 = %v/%v", following, err)
	}
	if _, err := store.ToggleFollow(ctx, 1, 3); err != nil {
		t.Fatalf("关注第二人: %v", err)
	}

	// 再点一次是取消关注，不能变成重复插入。
	following, err = store.ToggleFollow(ctx, 1, 3)
	if err != nil || following {
		t.Fatalf("取消关注 = %v/%v", following, err)
	}

	// 不能关注自己。
	if _, err := store.ToggleFollow(ctx, 1, 1); err == nil {
		t.Fatal("关注自己应当被拒绝")
	}

	set, err := store.FollowingSet(ctx, 1, []int{2, 3})
	if err != nil || !set[2] || set[3] {
		t.Fatalf("FollowingSet = %#v/%v", set, err)
	}

	followers, followingCount, err := store.CountFollow(ctx, 2)
	if err != nil || followers != 1 || followingCount != 0 {
		t.Fatalf("CountFollow(2) = %d/%d/%v", followers, followingCount, err)
	}

	// 动态流只包含已关注的人：用户 3 已被取消关注，不能出现。
	feed, err := store.ListFeed(ctx, 1, 20, 0)
	if err != nil {
		t.Fatalf("ListFeed: %v", err)
	}
	if len(feed) != 1 || feed[0].UserID != 2 || feed[0].Title != "肖申克的救赎" {
		t.Fatalf("feed = %#v", feed)
	}

	// 关注会生成通知，取关会收回；重复关注不能刷屏。
	notifications, err := store.ListNotifications(ctx, 2, 20)
	if err != nil || len(notifications) != 1 {
		t.Fatalf("被关注方通知 = %#v/%v", notifications, err)
	}
	if got := notifications[0]; got.Type != "follow" || got.ActorUserID != 1 || got.UserMovieID != 0 || !got.Unread {
		t.Fatalf("关注通知内容 = %+v", got)
	}
	// 用户 3 被关注后又被取关，通知必须一起撤掉，不能留个「关注了你」的幽灵。
	if stale, _ := store.ListNotifications(ctx, 3, 20); len(stale) != 0 {
		t.Fatalf("取关后仍有通知 = %#v", stale)
	}

	// 红点必须把关注也算进去。这条断言不能只查「读完变 0」——
	// 未读计数漏掉某个类型时，读之前本来就是 0，那种断言是空过的。
	if unread, _ := store.CountUnreadNotifications(ctx, 2); unread != 1 {
		t.Fatalf("关注应计入未读，实际 = %d", unread)
	}

	// 点开关注通知没有短评可跳，应当给出关注者的主页。
	target, err := store.ReadNotification(ctx, notifications[0].ID, 2)
	if err != nil || target.ActorUserID != 1 || target.UserMovieID != 0 {
		t.Fatalf("关注通知跳转目标 = %+v/%v", target, err)
	}
	if unread, _ := store.CountUnreadNotifications(ctx, 2); unread != 0 {
		t.Fatalf("已读后未读数 = %d", unread)
	}

	// 取关再关注：走 ON CONFLICT 更新同一行，不是插第二条。
	if _, err := store.ToggleFollow(ctx, 1, 2); err != nil {
		t.Fatalf("取关: %v", err)
	}
	if _, err := store.ToggleFollow(ctx, 1, 2); err != nil {
		t.Fatalf("重新关注: %v", err)
	}
	again, _ := store.ListNotifications(ctx, 2, 20)
	if len(again) != 1 || !again[0].Unread {
		t.Fatalf("重新关注后通知 = %#v", again)
	}

	// 短评永久链接：有内容的能取到，空短评按不存在处理。
	activity, err := store.GetComment(ctx, 11)
	if err != nil || activity == nil || activity.Comment != "希望是好东西" {
		t.Fatalf("GetComment(11) = %#v/%v", activity, err)
	}
	empty, err := store.GetComment(ctx, 12)
	if err != nil || empty != nil {
		t.Fatalf("空短评应当不可访问 = %#v/%v", empty, err)
	}
}
