package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/user/moovie/internal/middleware"
	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/utils"
	"golang.org/x/sync/singleflight"
)

// 弹幕代理：把 danmu_api（弹弹play 兼容接口）的数据转成 Artplayer 弹幕格式。
//
// 之所以要在服务端中转而不是浏览器直连：
//  1. 绕开 CORS，并且不把弹幕服务地址暴露给前端（否则会被白嫖打爆）；
//  2. 上游是实时抓取各视频平台，单次耗时 5~10 秒，必须有一层强缓存；
//  3. 上游默认对 /comment 接口按 IP 限流，而代理后所有用户在它眼里是同一个 IP，
//     真正的限流只能放在这里按访客 IP 做。
//
// 注意：上游返回的 episodeId 是它内存缓存里的临时下标，每次搜索都会变，
// 因此绝对不能把 episodeId 持久化，match → comment 必须在一次请求里连着做完，
// 缓存的是最终弹幕结果（key 为 片名+季+集）。
const (
	danmakuMaxItems        = 4000             // 单集最多返回多少条，超出等间隔采样
	danmakuHitTTL          = 12 * time.Hour   // 命中缓存有效期
	danmakuMissTTL         = 20 * time.Minute // 未匹配到时的负缓存，避免反复打上游
	danmakuUpstreamTimeout = 25 * time.Second // 上游总超时（match + comment）
	danmakuMaxBodyBytes    = 24 << 20         // 上游响应体上限 24MB，防御性限制
	danmakuIPLimit         = 20               // 单 IP 每分钟最多触发多少次回源
	danmakuIPWindow        = time.Minute

	// 站内弹幕
	danmakuOwnMaxItems   = 2000            // 单集最多返回多少条站内弹幕
	danmakuMaxTextLen    = 50              // 单条弹幕最大字数
	danmakuSendPerWindow = 10              // 每个用户每窗口最多发多少条
	danmakuSendWindow    = time.Minute     // 频率限制窗口
	danmakuDupWindow     = 5 * time.Minute // 同一集内重复内容的判定窗口
)

// DanmakuItem Artplayer 弹幕插件要求的数据结构
type DanmakuItem struct {
	Text  string  `json:"text"`
	Time  float64 `json:"time"`
	Mode  int     `json:"mode"`  // 0 滚动 / 1 顶部 / 2 底部
	Color string  `json:"color"` // #RRGGBB
}

// ---------- 上游（弹弹play 兼容）响应结构 ----------

type danmuMatchResp struct {
	Success   bool `json:"success"`
	IsMatched bool `json:"isMatched"`
	Matches   []struct {
		EpisodeID    int64  `json:"episodeId"`
		AnimeTitle   string `json:"animeTitle"`
		EpisodeTitle string `json:"episodeTitle"`
	} `json:"matches"`
}

type danmuComment struct {
	P string `json:"p"` // "时间,模式,颜色,来源"
	M string `json:"m"` // 弹幕文本
}

type danmuCommentResp struct {
	Comments []danmuComment `json:"comments"`
}

var (
	danmakuHitCache  = utils.NewSearchCache[[]DanmakuItem](80, danmakuHitTTL)
	danmakuMissCache = utils.NewSearchCache[bool](500, danmakuMissTTL)
	danmakuGroup     singleflight.Group
	danmakuLimiter   = newIPLimiter(danmakuIPLimit, danmakuIPWindow)
)

// buildVodKey 把片名+季+集归一化成一个标识，外部源缓存和站内弹幕归属都用它。
// 刻意不带 source_key：同一集常常同时存在于多个采集源，
// 按片名维度归属才能让弹幕在不同线路之间共享。
func buildVodKey(title string, season, episode int) string {
	return fmt.Sprintf("%s|S%02d|E%03d", strings.ToLower(title), season, episode)
}

// Danmaku GET /api/danmaku?title=庆余年第二季&episode=第3集
//
// 返回「外部聚合源 + 站内用户弹幕」的合集。
// 无论成功与否都返回 JSON 数组，失败时是空数组，
// 这样前端永远不会因为弹幕挂掉而影响正片播放。
func (h *Handler) Danmaku(c *gin.Context) {
	empty := []DanmakuItem{}

	rawTitle := strings.TrimSpace(c.Query("title"))
	if rawTitle == "" || len([]rune(rawTitle)) > 100 {
		c.JSON(http.StatusOK, empty)
		return
	}

	season, title := splitSeason(rawTitle)
	episode := parseEpisodeNumber(c.Query("episode"))
	vodKey := buildVodKey(title, season, episode)

	external := h.externalDanmaku(c, vodKey, title, season, episode)
	// 站内弹幕每次实时查库，不能跟着外部源一起进 12 小时缓存，
	// 否则用户发完刷新看不到自己刚发的那条
	own := h.ownDanmaku(vodKey)

	if len(external) == 0 && len(own) == 0 {
		c.JSON(http.StatusOK, empty)
		return
	}

	// 必须复制到新切片再合并：external 可能直接来自缓存，
	// 缓存里的切片 cap 往往大于 len，直接 append 会写进缓存的底层数组，
	// 并发请求之间会互相踩数据
	merged := make([]DanmakuItem, 0, len(external)+len(own))
	merged = append(merged, external...)
	merged = append(merged, own...)

	// 不需要按时间排序：插件是按时间窗口从队列里筛选的，与顺序无关
	c.JSON(http.StatusOK, merged)
}

// externalDanmaku 取外部聚合源的弹幕，带缓存、负缓存、限流和 singleflight。
// 任何失败都返回空切片，不往上抛错。
func (h *Handler) externalDanmaku(c *gin.Context, vodKey, title string, season, episode int) []DanmakuItem {
	if h.Config.DanmakuAPIBase == "" {
		return nil
	}

	key := "dm:" + vodKey

	// 1. 命中缓存
	if items, ok := danmakuHitCache.Get(key); ok {
		return items
	}
	// 2. 负缓存：上次没匹配到，短时间内不再回源
	if _, ok := danmakuMissCache.Get(key); ok {
		return nil
	}
	// 3. 需要回源，此时才计入限流
	if !danmakuLimiter.Allow(c.ClientIP()) {
		return nil
	}

	// singleflight：同一集被多人同时打开时只回源一次
	// 用 Background 而不是请求上下文，避免首个请求断开导致所有等待者一起失败
	v, err, _ := danmakuGroup.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), danmakuUpstreamTimeout)
		defer cancel()

		items, err := h.fetchDanmaku(ctx, title, season, episode)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			danmakuMissCache.Set(key, true)
			return []DanmakuItem{}, nil
		}
		danmakuHitCache.Set(key, items)
		return items, nil
	})

	if err != nil {
		log.Printf("[Danmaku] 外部源获取失败 title=%q season=%d ep=%d err=%v", title, season, episode, err)
		return nil
	}

	items, _ := v.([]DanmakuItem)
	return items
}

// ownDanmaku 取站内用户发的弹幕
func (h *Handler) ownDanmaku(vodKey string) []DanmakuItem {
	if h.Repos == nil || h.Repos.Danmaku == nil {
		return nil
	}
	rows, err := h.Repos.Danmaku.ListByVodKey(vodKey, danmakuOwnMaxItems)
	if err != nil {
		log.Printf("[Danmaku] 查询站内弹幕失败 key=%q err=%v", vodKey, err)
		return nil
	}
	items := make([]DanmakuItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, DanmakuItem{
			Text:  r.Text,
			Time:  r.Time,
			Mode:  r.Mode,
			Color: r.Color,
		})
	}
	return items
}

// PostDanmaku POST /api/danmaku 发送一条站内弹幕（需要登录）
func (h *Handler) PostDanmaku(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后再发送弹幕"})
		return
	}

	var req struct {
		Title   string  `json:"title"`
		Episode string  `json:"episode"`
		Text    string  `json:"text"`
		Time    float64 `json:"time"`
		Mode    int     `json:"mode"`
		Color   string  `json:"color"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	rawTitle := strings.TrimSpace(req.Title)
	if rawTitle == "" || len([]rune(rawTitle)) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}

	text := sanitizeDanmakuText(req.Text)
	n := len([]rune(text))
	if n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "弹幕内容不能为空"})
		return
	}
	if n > danmakuMaxTextLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("弹幕最多 %d 个字", danmakuMaxTextLen)})
		return
	}

	// 模式和颜色都做白名单，不信任前端传什么
	mode := req.Mode
	if mode != 0 && mode != 1 && mode != 2 {
		mode = 0
	}
	color := strings.ToUpper(strings.TrimSpace(req.Color))
	if !reHexColor.MatchString(color) {
		color = "#FFFFFF"
	}
	t := req.Time
	if t < 0 || math.IsNaN(t) || math.IsInf(t, 0) {
		t = 0
	}

	season, title := splitSeason(rawTitle)
	episode := parseEpisodeNumber(req.Episode)
	vodKey := buildVodKey(title, season, episode)

	// 频率限制
	since := time.Now().Add(-danmakuSendWindow)
	if cnt, err := h.Repos.Danmaku.CountByUserSince(userID, since); err == nil && cnt >= danmakuSendPerWindow {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "发送太频繁了，歇一会儿再发"})
		return
	}
	// 短时间内同一集重复内容，直接当成手抖/刷屏拦掉
	if dup, err := h.Repos.Danmaku.ExistsDuplicate(userID, vodKey, text, time.Now().Add(-danmakuDupWindow)); err == nil && dup {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "刚发过一样的内容了"})
		return
	}

	d := &model.Danmaku{
		VodKey: vodKey,
		Time:   t,
		Text:   text,
		Mode:   mode,
		Color:  color,
		UserID: userID,
	}
	if err := h.Repos.Danmaku.Create(d); err != nil {
		log.Printf("[Danmaku] 保存失败 user=%d key=%q err=%v", userID, vodKey, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "发送失败，请稍后重试"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

var (
	reHexColor       = regexp.MustCompile(`^#[0-9A-F]{6}$`)
	reDanmakuControl = regexp.MustCompile(`[\x00-\x1F\x7F]`) // 控制字符，含换行回车
	reDanmakuFormat  = regexp.MustCompile(`\p{Cf}`)          // 零宽字符、方向控制符等不可见字符
)

// sanitizeDanmakuText 清洗弹幕文本。
//
// 两类字符处理方式不同，不能合成一个正则：
//   - 控制字符（换行、回车等）替换成空格，否则 "换行\n注入" 会被粘成 "换行注入"；
//   - 零宽字符直接删除，它本身就不该占位。
//
// 零宽字符尤其要清：插在敏感词中间能绕过关键词过滤，
// 单独发一串还能伪造出"空白弹幕"，而 U+202E 能让整条弹幕反向渲染。
func sanitizeDanmakuText(s string) string {
	s = reDanmakuControl.ReplaceAllString(s, " ")
	s = reDanmakuFormat.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}

// fetchDanmaku 两步回源：match 拿 episodeId → comment 取弹幕
func (h *Handler) fetchDanmaku(ctx context.Context, title string, season, episode int) ([]DanmakuItem, error) {
	base := h.Config.DanmakuAPIBase

	// 上游按「片名 S01E01」这种规范文件名做匹配；没有集数时按电影处理，只传片名
	fileName := title
	if episode > 0 {
		fileName = fmt.Sprintf("%s S%02dE%02d", title, season, episode)
	}

	episodeID, err := h.danmakuMatch(ctx, base, fileName)
	if err != nil || episodeID == 0 {
		return nil, err
	}

	comments, err := h.danmakuComments(ctx, base, episodeID)
	if err != nil {
		return nil, err
	}

	items := toArtplayerDanmaku(comments)
	items = sampleDanmaku(items, danmakuMaxItems)
	log.Printf("[Danmaku] %s -> episodeId=%d 原始 %d 条，返回 %d 条", fileName, episodeID, len(comments), len(items))
	return items, nil
}

func (h *Handler) danmakuMatch(ctx context.Context, base, fileName string) (int64, error) {
	payload, _ := json.Marshal(map[string]string{"fileName": fileName})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v2/match", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := utils.GlobalHttpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("match 返回 %d", resp.StatusCode)
	}

	var mr danmuMatchResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&mr); err != nil {
		return 0, err
	}
	if !mr.IsMatched || len(mr.Matches) == 0 {
		return 0, nil // 没匹配到不算错误，走负缓存
	}
	return mr.Matches[0].EpisodeID, nil
}

func (h *Handler) danmakuComments(ctx context.Context, base string, episodeID int64) ([]danmuComment, error) {
	url := fmt.Sprintf("%s/api/v2/comment/%d?format=json", base, episodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := utils.GlobalHttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 404 通常是上游内存里的 episodeId 映射丢了（Serverless 冷启动会这样），
	// 当成"没有弹幕"处理，不要往上抛错刷日志
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("comment 返回 %d", resp.StatusCode)
	}

	var cr danmuCommentResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, danmakuMaxBodyBytes)).Decode(&cr); err != nil {
		return nil, err
	}
	return cr.Comments, nil
}

// ---------- 格式转换 ----------

// toArtplayerDanmaku 弹弹play 的 p 字段是 "时间,模式,颜色,来源"，
// 其中模式编号（1 滚动 / 4 底部 / 5 顶部）和 Artplayer（0/2/1）完全不同，必须映射。
func toArtplayerDanmaku(comments []danmuComment) []DanmakuItem {
	out := make([]DanmakuItem, 0, len(comments))
	for _, cm := range comments {
		text := strings.TrimSpace(cm.M)
		if text == "" {
			continue
		}
		parts := strings.Split(cm.P, ",")
		if len(parts) < 3 {
			continue
		}

		t, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil || t < 0 {
			continue
		}

		mode := 0
		switch strings.TrimSpace(parts[1]) {
		case "4":
			mode = 2 // 底部
		case "5":
			mode = 1 // 顶部
		}

		color := "#FFFFFF"
		if n, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil && n >= 0 && n <= 0xFFFFFF {
			color = fmt.Sprintf("#%06X", n)
		}

		out = append(out, DanmakuItem{Text: text, Time: t, Mode: mode, Color: color})
	}
	return out
}

// sampleDanmaku 等间隔采样。热门剧单集能有四万条，全量下发对带宽和前端渲染都是负担。
func sampleDanmaku(items []DanmakuItem, max int) []DanmakuItem {
	if max <= 0 || len(items) <= max {
		return items
	}
	out := make([]DanmakuItem, 0, max)
	step := float64(len(items)) / float64(max)
	for i := 0; i < max; i++ {
		idx := int(float64(i) * step)
		if idx >= len(items) {
			break
		}
		out = append(out, items[idx])
	}
	return out
}

// ---------- 片名 / 集数解析 ----------

var (
	reSeasonCJK = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十]+)\s*[季部]`)
	reSeasonEN  = regexp.MustCompile(`(?i)\bs(?:eason)?\s*(\d{1,2})\b`)
	reTitleNoise = regexp.MustCompile(`(?i)[（(\[【][^）)\]】]*[）)\]】]|4k|2160p|1080p|720p|web-?dl|蓝光|国语|粤语|中字|高清|抢先版|未删减`)

	reEpPure = regexp.MustCompile(`^\s*(\d{1,4})\s*$`)
	reEpCJK  = regexp.MustCompile(`第\s*([0-9一二三四五六七八九十百零两]+)\s*[集话話期]`)
	reEpEN   = regexp.MustCompile(`(?i)^\s*e(?:p|pisode)?\.?\s*(\d{1,4})\s*$`)
)

// splitSeason 从片名里剥离季数。"庆余年 第二季" → (2, "庆余年")
// 上游要的是「干净片名 + SxxExx」，把季数混在片名里会拉低匹配率。
func splitSeason(title string) (int, string) {
	season := 1
	clean := title

	if m := reSeasonCJK.FindStringSubmatch(title); len(m) == 2 {
		if n := cnNumToInt(m[1]); n > 0 {
			season = n
		}
		clean = strings.Replace(clean, m[0], " ", 1)
	} else if m := reSeasonEN.FindStringSubmatch(title); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			season = n
		}
		clean = strings.Replace(clean, m[0], " ", 1)
	}

	clean = reTitleNoise.ReplaceAllString(clean, " ")
	clean = strings.Join(strings.Fields(clean), " ")
	if clean == "" {
		clean = strings.TrimSpace(title)
	}
	return season, clean
}

// parseEpisodeNumber 从剧集标题里抠出集数。
// 采集站的集名五花八门："第3集" "03" "EP3" "正片" "HD国语"...
// 认不出来就返回 0，按电影处理（只用片名匹配），这比瞎猜一个集数安全。
func parseEpisodeNumber(raw string) int {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0
	}
	if m := reEpPure.FindStringSubmatch(s); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if m := reEpCJK.FindStringSubmatch(s); len(m) == 2 {
		return cnNumToInt(m[1])
	}
	if m := reEpEN.FindStringSubmatch(s); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// cnNumToInt 中文数字转阿拉伯数字，支持到 "一百零五"。集名里 "第十二集" 很常见。
func cnNumToInt(s string) int {
	s = strings.TrimSpace(s)
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	digits := map[rune]int{'零': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	section := 0
	for _, r := range s {
		switch {
		case r == '十':
			if section == 0 {
				section = 1
			}
			section *= 10
		case r == '百':
			if section == 0 {
				section = 1
			}
			section *= 100
		default:
			d, ok := digits[r]
			if !ok {
				return 0
			}
			if section > 0 && section%10 == 0 {
				section += d // 十二 → 12，一百零五 → 105
			} else {
				section = section*10 + d
			}
		}
	}
	return section
}

// ---------- 按访客 IP 限流 ----------

type ipCount struct {
	n     int
	reset time.Time
}

type ipLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	counts map[string]*ipCount
	lastGC time.Time
}

func newIPLimiter(max int, window time.Duration) *ipLimiter {
	return &ipLimiter{
		max:    max,
		window: window,
		counts: make(map[string]*ipCount),
		lastGC: time.Now(),
	}
}

func (l *ipLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	// 顺手清理过期条目，避免 map 无限增长
	if now.Sub(l.lastGC) > 5*l.window {
		for k, v := range l.counts {
			if now.After(v.reset) {
				delete(l.counts, k)
			}
		}
		l.lastGC = now
	}

	c, ok := l.counts[ip]
	if !ok || now.After(c.reset) {
		l.counts[ip] = &ipCount{n: 1, reset: now.Add(l.window)}
		return true
	}
	if c.n >= l.max {
		return false
	}
	c.n++
	return true
}
