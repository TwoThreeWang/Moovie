package social

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/gin-gonic/gin"
)

// 片场各板块的展示数量。
const (
	weeklyFilmLimit      = 6
	featuredCommentLimit = 6
	filmFriendLimit      = 5
	feedPageSize         = 20
)

// Handler 提供片场页面和短评互动接口。
type Handler struct {
	config config.Config
	store  Store
	now    func() time.Time
}

// NewHandler 创建片场处理器。
func NewHandler(cfg config.Config, store Store) *Handler {
	return &Handler{config: cfg, store: store, now: time.Now}
}

// Register 注册片场路由，全部用 Optional 鉴权（游客可看不可互动）。
func (handler *Handler) Register(router *gin.Engine) {
	optional := auth.Optional(handler.config.AppSecret)
	router.GET("/cinema", optional, handler.cinema)
	router.GET("/api/htmx/movie-comments", optional, handler.movieComments)
	router.POST("/api/comments/:id/like", optional, handler.toggleLike)
	router.GET("/api/comments/:id/replies", optional, handler.replies)
	router.POST("/api/comments/:id/replies", optional, handler.createReply)
	router.GET("/review/:id", optional, handler.review)
	require := auth.Require(handler.config.AppSecret, handler.config.Env == "production")
	router.GET("/feed", require, handler.feed)
	router.GET("/following", require, handler.following)
	router.POST("/api/users/:user_id/follow", require, handler.toggleFollow)
	router.DELETE("/api/users/:user_id/follow", require, handler.unfollow)
	router.GET("/notifications", require, handler.notifications)
	router.GET("/api/notifications/unread-count", require, handler.unreadNotificationCount)
	router.POST("/notifications/read-all", require, handler.readAllNotifications)
	router.POST("/notifications/:id/read", require, handler.readNotification)
	router.DELETE("/notifications/:id", require, handler.deleteNotification)
}

// cinema 渲染片场首页：本周热门 + 精选短评 + 片友推荐。
// 三个查询任一失败就整页报错，因为少一块内容页面就不成立了。
func (handler *Handler) cinema(c *gin.Context) {
	now := handler.now()
	weekStart := startOfWeek(now)
	weeklyFilms, err := handler.store.ListWeeklyFilms(c.Request.Context(), weekStart, weeklyFilmLimit)
	if err != nil {
		c.String(http.StatusInternalServerError, "片场暂时无法开场")
		return
	}
	comments, err := handler.store.ListFeaturedComments(c.Request.Context(), featuredCommentLimit)
	if err != nil {
		c.String(http.StatusInternalServerError, "片场暂时无法开场")
		return
	}
	currentUserID := auth.UserID(c)
	friends, err := handler.store.ListFilmFriends(c.Request.Context(), currentUserID, filmFriendLimit)
	if err != nil {
		c.String(http.StatusInternalServerError, "片场暂时无法开场")
		return
	}
	commentIDs := make([]int, 0, len(comments))
	for _, comment := range comments {
		commentIDs = append(commentIDs, comment.ID)
	}
	likeCounts, _ := handler.store.CountLikes(c.Request.Context(), commentIDs)
	replyCounts, _ := handler.store.CountReplies(c.Request.Context(), commentIDs)
	liked, _ := handler.store.LikedByUser(c.Request.Context(), commentIDs, currentUserID)
	friendIDs := make([]int, 0, len(friends))
	for _, friend := range friends {
		friendIDs = append(friendIDs, friend.UserID)
	}
	following, _ := handler.store.FollowingSet(c.Request.Context(), currentUserID, friendIDs)

	c.HTML(http.StatusOK, "cinema.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       "片场 - " + handler.config.SiteName,
		Description: "从片友的放映单与短评里，遇见下一部电影。",
		Canonical:   platformweb.CanonicalURL(handler.config.SiteURL, "/cinema"),
	}, gin.H{
		"WeeklyFilms": weeklyFilms, "FeaturedComments": comments, "FilmFriends": friends,
		"LikeCounts": likeCounts, "ReplyCounts": replyCounts, "Liked": liked, "Following": following,
		"CurrentUserID": currentUserID, "WeekStart": weekStart, "WeekEnd": weekStart.AddDate(0, 0, 6),
	}))
}

// movieComments 返回详情页的短评列表片段。
func (handler *Handler) movieComments(c *gin.Context) {
	movieID := c.Query("douban_id")
	if movieID == "" {
		c.String(http.StatusOK, "")
		return
	}
	comments, err := handler.store.ListCommentsByMovie(c.Request.Context(), movieID, 10)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	ids := make([]int, 0, len(comments))
	for _, comment := range comments {
		ids = append(ids, comment.ID)
	}
	likeCounts, _ := handler.store.CountLikes(c.Request.Context(), ids)
	replyCounts, _ := handler.store.CountReplies(c.Request.Context(), ids)
	liked, _ := handler.store.LikedByUser(c.Request.Context(), ids, auth.UserID(c))
	c.HTML(http.StatusOK, "partials/movie_user_comments.html", gin.H{
		"Comments": comments, "LikeCounts": likeCounts, "ReplyCounts": replyCounts,
		"Liked": liked, "CurrentUserID": auth.UserID(c),
	})
}

// toggleLike 点赞或取消点赞。
func (handler *Handler) toggleLike(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	count, liked, err := handler.store.ToggleLike(c.Request.Context(), userMovieID, userID)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	c.HTML(http.StatusOK, "partials/comment_like_button.html", gin.H{"UserMovieID": userMovieID, "LikeCount": count, "Liked": liked})
}

// replies 返回某条短评的回复列表。
func (handler *Handler) replies(c *gin.Context) {
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	handler.renderReplies(c, userMovieID, auth.UserID(c))
}

// createReply 发表回复。
func (handler *Handler) createReply(c *gin.Context) {
	userID := auth.UserID(c)
	if userID == 0 {
		c.String(http.StatusUnauthorized, "")
		return
	}
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	content := strings.TrimSpace(c.PostForm("content"))
	if content == "" {
		c.String(http.StatusBadRequest, "回复内容不能为空")
		return
	}
	characters := []rune(content)
	if len(characters) > 300 {
		content = string(characters[:300])
	}
	if _, err := handler.store.CreateReply(c.Request.Context(), userMovieID, userID, content); err != nil {
		c.String(http.StatusInternalServerError, "回复失败")
		return
	}
	handler.renderReplies(c, userMovieID, userID)
}

// renderReplies 渲染回复列表片段。
func (handler *Handler) renderReplies(c *gin.Context, userMovieID, userID int) {
	replies, err := handler.store.ListReplies(c.Request.Context(), userMovieID)
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	c.HTML(http.StatusOK, "partials/comment_replies.html", gin.H{"UserMovieID": userMovieID, "Replies": replies, "CurrentUserID": userID})
}

// toggleFollow 关注或取消关注某个用户，返回刷新后的按钮片段。
func (handler *Handler) toggleFollow(c *gin.Context) {
	followerID := auth.UserID(c)
	followeeID, err := positiveID(c.Param("user_id"))
	if err != nil || followeeID == followerID {
		c.String(http.StatusBadRequest, "")
		return
	}
	following, err := handler.store.ToggleFollow(c.Request.Context(), followerID, followeeID)
	if errors.Is(err, ErrFollowUnavailable) {
		c.String(http.StatusNotFound, "用户不存在或未公开主页")
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "")
		return
	}
	followers, _, _ := handler.store.CountFollow(c.Request.Context(), followeeID)
	c.HTML(http.StatusOK, "partials/follow_button.html", gin.H{
		"UserID": followeeID, "Following": following, "Followers": followers, "CurrentUserID": followerID,
	})
}

func (handler *Handler) unfollow(c *gin.Context) {
	id, err := positiveID(c.Param("user_id"))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := handler.store.Unfollow(c.Request.Context(), auth.UserID(c), id); err != nil {
		c.String(http.StatusInternalServerError, "取消关注失败，请重试")
		return
	}
	c.Header("HX-Refresh", "true")
	c.Status(http.StatusOK)
}

func (handler *Handler) following(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 || page > 100000 {
		page = 1
	}
	users, err := handler.store.ListFollowing(c.Request.Context(), auth.UserID(c), feedPageSize+1, (page-1)*feedPageSize)
	if err != nil {
		c.String(http.StatusInternalServerError, "关注列表暂时无法加载")
		return
	}
	hasMore := len(users) > feedPageSize
	if hasMore {
		users = users[:feedPageSize]
	}
	c.HTML(http.StatusOK, "following.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: "我关注的人 - " + handler.config.SiteName, Robots: "noindex, nofollow",
	}, gin.H{"FollowingUsers": users, "Page": page, "NextPage": page + 1, "HasMore": hasMore}))
}

// feed 展示关注的人的最新动态。没有关注任何人时退回片友推荐，
// 空页面比没有内容更劝退，至少要给一条继续走下去的路。
func (handler *Handler) feed(c *gin.Context) {
	userID := auth.UserID(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	offset := (page - 1) * feedPageSize
	activities, err := handler.store.ListFeed(c.Request.Context(), userID, feedPageSize, offset)
	if err != nil {
		c.String(http.StatusInternalServerError, "动态暂时无法加载")
		return
	}
	commentIDs := make([]int, 0, len(activities))
	for _, activity := range activities {
		commentIDs = append(commentIDs, activity.ID)
	}
	likeCounts, _ := handler.store.CountLikes(c.Request.Context(), commentIDs)
	replyCounts, _ := handler.store.CountReplies(c.Request.Context(), commentIDs)
	liked, _ := handler.store.LikedByUser(c.Request.Context(), commentIDs, userID)
	_, followingCount, _ := handler.store.CountFollow(c.Request.Context(), userID)

	data := gin.H{
		"Activities": activities, "LikeCounts": likeCounts, "ReplyCounts": replyCounts, "Liked": liked,
		"CurrentUserID": userID, "FollowingCount": followingCount,
		"HasMore": len(activities) == feedPageSize, "NextPage": page + 1, "Page": page,
	}
	if followingCount == 0 {
		friends, _ := handler.store.ListFilmFriends(c.Request.Context(), userID, filmFriendLimit)
		data["FilmFriends"] = friends
		data["Following"] = map[int]bool{}
	}
	c.HTML(http.StatusOK, "feed.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: "关注动态 - " + handler.config.SiteName, Robots: "noindex, nofollow",
	}, data))
}

// review 是单条短评的永久链接页。短评现在是 user_movies 上的一个字段，
// 没有独立 URL 就无法被分享、收录或引用；这一页让它成为可以被链接的内容。
func (handler *Handler) review(c *gin.Context) {
	userMovieID, err := positiveID(c.Param("id"))
	if err != nil {
		handler.reviewNotFound(c)
		return
	}
	activity, err := handler.store.GetComment(c.Request.Context(), userMovieID)
	if err != nil {
		c.String(http.StatusInternalServerError, "短评暂时无法加载")
		return
	}
	if activity == nil {
		handler.reviewNotFound(c)
		return
	}
	currentUserID := auth.UserID(c)
	ids := []int{activity.ID}
	likeCounts, _ := handler.store.CountLikes(c.Request.Context(), ids)
	replyCounts, _ := handler.store.CountReplies(c.Request.Context(), ids)
	liked, _ := handler.store.LikedByUser(c.Request.Context(), ids, currentUserID)
	replies, _ := handler.store.ListReplies(c.Request.Context(), activity.ID)
	following, _ := handler.store.FollowingSet(c.Request.Context(), currentUserID, []int{activity.UserID})
	followers, _, _ := handler.store.CountFollow(c.Request.Context(), activity.UserID)

	canonical := platformweb.CanonicalURL(handler.config.SiteURL, "/review/"+strconv.Itoa(activity.ID))
	c.HTML(http.StatusOK, "review.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title:       activity.User.Username + "评《" + activity.Title + "》 - " + handler.config.SiteName,
		Description: summarize(activity.Comment, 120),
		Canonical:   canonical,
	}, gin.H{
		"Activity": activity, "Replies": replies, "CurrentUserID": currentUserID, "Canonical": canonical,
		"LikeCount": likeCounts[activity.ID], "ReplyCount": replyCounts[activity.ID], "Liked": liked[activity.ID],
		"Following": following[activity.UserID], "Followers": followers,
	}))
}

// reviewNotFound 渲染短评不存在时的 404。
func (handler *Handler) reviewNotFound(c *gin.Context) {
	c.HTML(http.StatusNotFound, "404.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: "短评未找到 - " + handler.config.SiteName, Robots: "noindex, follow",
	}, gin.H{"Path": c.Request.URL.Path}))
}

// summarize 截断文本用于页面描述，按 rune 计数避免把中文截半。
func summarize(text string, limit int) string {
	text = strings.TrimSpace(text)
	characters := []rune(text)
	if len(characters) <= limit {
		return text
	}
	return string(characters[:limit]) + "…"
}

// notifications 展示当前用户收到的短评互动。
func (handler *Handler) notifications(c *gin.Context) {
	notifications, err := handler.store.ListNotifications(c.Request.Context(), auth.UserID(c), 50)
	if err != nil {
		c.String(http.StatusInternalServerError, "消息暂时无法加载")
		return
	}
	c.HTML(http.StatusOK, "notifications.html", platformweb.NewData(c, handler.config, platformweb.Metadata{
		Title: "消息 - " + handler.config.SiteName, Robots: "noindex, nofollow",
	}, gin.H{"Notifications": notifications}))
}

// unreadNotificationCount 返回全局导航使用的聚合未读数。
func (handler *Handler) unreadNotificationCount(c *gin.Context) {
	count, err := handler.store.CountUnreadNotifications(c.Request.Context(), auth.UserID(c))
	if err != nil {
		c.String(http.StatusOK, "")
		return
	}
	c.HTML(http.StatusOK, "partials/notification_badge.html", gin.H{"Count": count})
}

// readNotification 标记一项已读，再跳到原短评或公开主页；私密主页留在消息列表。
func (handler *Handler) readNotification(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	target, err := handler.store.ReadNotification(c.Request.Context(), id, auth.UserID(c))
	if errors.Is(err, ErrNotificationUnavailable) {
		handler.renderNotificationList(c, auth.UserID(c), "这条消息已失效或不存在，消息列表已更新。")
		return
	}
	if err != nil {
		c.String(http.StatusInternalServerError, "消息暂时无法打开，请稍后重试")
		return
	}
	if target.UserMovieID > 0 && !target.CommentAvailable {
		handler.renderNotificationList(c, auth.UserID(c), "原短评已清空或不可用，消息已标记为已读。")
		return
	}
	if target.UserMovieID == 0 && !target.ActorIsPublic {
		handler.renderNotificationList(c, auth.UserID(c), "对方主页未公开，消息已标记为已读。")
		return
	}
	destination := fmt.Sprintf("/user/%d", target.ActorUserID)
	if target.UserMovieID > 0 {
		destination = fmt.Sprintf("/review/%d", target.UserMovieID)
	}
	c.Header("HX-Redirect", destination)
	c.Status(http.StatusOK)
}

// readAllNotifications 标记全部已读并重绘列表。
func (handler *Handler) readAllNotifications(c *gin.Context) {
	userID := auth.UserID(c)
	if err := handler.store.ReadAllNotifications(c.Request.Context(), userID); err != nil {
		c.String(http.StatusInternalServerError, "标记已读失败")
		return
	}
	handler.renderNotificationList(c, userID, "")
}

// deleteNotification 物理删除当前用户的一条消息并重绘列表。
func (handler *Handler) deleteNotification(c *gin.Context) {
	id, err := positiveID(c.Param("id"))
	if err != nil {
		c.String(http.StatusBadRequest, "")
		return
	}
	userID := auth.UserID(c)
	if err := handler.store.DeleteNotification(c.Request.Context(), id, userID); err != nil {
		c.String(http.StatusNotFound, "消息不存在")
		return
	}
	handler.renderNotificationList(c, userID, "")
}

func (handler *Handler) renderNotificationList(c *gin.Context, userID int, notice string) {
	notifications, err := handler.store.ListNotifications(c.Request.Context(), userID, 50)
	if err != nil {
		c.String(http.StatusInternalServerError, "消息暂时无法加载")
		return
	}
	c.Header("HX-Trigger", "notificationsChanged")
	c.Header("HX-Retarget", "#notification-list")
	c.Header("HX-Reswap", "innerHTML")
	c.HTML(http.StatusOK, "partials/notification_list.html", gin.H{"Notifications": notifications, "Notice": notice})
}

// startOfWeek 取本周一零点，作为「本周」的起点。
func startOfWeek(value time.Time) time.Time {
	daysSinceMonday := (int(value.Weekday()) + 6) % 7
	year, month, day := value.Date()
	return time.Date(year, month, day-daysSinceMonday, 0, 0, 0, 0, value.Location())
}

// positiveID 解析并校验正整数 ID。
func positiveID(value string) (int, error) {
	id, err := strconv.Atoi(value)
	if err != nil || id <= 0 {
		return 0, strconv.ErrSyntax
	}
	return id, nil
}
