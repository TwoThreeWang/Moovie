package danmaku

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/cache"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/ratelimit"
	"golang.org/x/sync/singleflight"
)

// 弹幕相关的各种上限：上游最多保留 4000 条、站内最多读 2000 条、单条最多 50 字、
// 每人每分钟最多发 10 条、5 分钟内不能发重复内容、上游请求超时 25 秒。
const (
	externalMaximum = 4000
	localMaximum    = 2000
	maxTextLength   = 50
	sendMaximum     = 10
	sendWindow      = time.Minute
	duplicateWindow = 5 * time.Minute
	upstreamTimeout = 25 * time.Second
)

// Service 组合上游弹幕和站内弹幕。
// hits 缓存命中的弹幕（12 小时），misses 缓存查不到的键（20 分钟）避免反复问上游。
type Service struct {
	store    Store
	upstream *upstreamClient
	hits     *cache.TTL[[]Item]
	misses   *cache.TTL[bool]
	group    singleflight.Group
	limiter  *ratelimit.PerIP
	now      func() time.Time
}

// NewService 创建弹幕服务，没配上游地址时只用站内弹幕。
func NewService(store Store, httpClient *http.Client, apiBase string) *Service {
	service := &Service{
		store: store, hits: cache.New[[]Item](80, 12*time.Hour), misses: cache.New[bool](500, 20*time.Minute),
		limiter: ratelimit.NewPerIP(20, time.Minute), now: time.Now,
	}
	if strings.TrimSpace(apiBase) != "" {
		service.upstream = newUpstreamClient(httpClient, apiBase)
	}
	return service
}

// List 返回某一集的全部弹幕。
func (service *Service) List(ctx context.Context, rawTitle, rawEpisode, clientIP string) []Item {
	rawTitle = strings.TrimSpace(rawTitle)
	if rawTitle == "" || len([]rune(rawTitle)) > 100 {
		return []Item{}
	}
	season, title := splitSeason(rawTitle)
	episode := parseEpisode(rawEpisode)
	vodKey := buildVodKey(title, season, episode)
	external := service.external(vodKey, title, season, episode, clientIP)
	local := make([]Item, 0)
	if records, err := service.store.ListByVodKey(ctx, vodKey, localMaximum); err == nil {
		for _, record := range records {
			local = append(local, Item{Text: record.Text, Time: record.Time, Mode: record.Mode, Color: record.Color})
		}
	}
	merged := make([]Item, 0, len(external)+len(local))
	merged = append(merged, external...)
	merged = append(merged, local...)
	return merged
}

// external 取上游弹幕：先查两级缓存，再按 IP 限流，最后 singleflight 去重回源。
// 任何一步失败都返回 nil，弹幕拉不到不能影响播放。
func (service *Service) external(vodKey, title string, season, episode int, clientIP string) []Item {
	if service.upstream == nil {
		return nil
	}
	key := "dm:" + vodKey
	if items, exists := service.hits.Get(key); exists {
		return items
	}
	if _, exists := service.misses.Get(key); exists || !service.limiter.Allow(clientIP) {
		return nil
	}
	value, err, _ := service.group.Do(key, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), upstreamTimeout)
		defer cancel()
		items, fetchErr := service.upstream.fetch(ctx, title, season, episode)
		if fetchErr != nil {
			return nil, fetchErr
		}
		items = sample(items, externalMaximum)
		if len(items) == 0 {
			service.misses.Set(key, true)
			return []Item{}, nil
		}
		service.hits.Set(key, items)
		return items, nil
	})
	if err != nil {
		return nil
	}
	items, _ := value.([]Item)
	return items
}

// SendInput 是发送弹幕的请求体。
type SendInput struct {
	Title   string  `json:"title"`
	Episode string  `json:"episode"`
	Text    string  `json:"text"`
	Time    float64 `json:"time"`
	Mode    int     `json:"mode"`
	Color   string  `json:"color"`
}

// Send 校验并发送弹幕，频率和重复检查在数据库事务里做（见 CreateGuarded）。
func (service *Service) Send(ctx context.Context, userID int, input SendInput) error {
	rawTitle := strings.TrimSpace(input.Title)
	if rawTitle == "" || len([]rune(rawTitle)) > 100 {
		return errParameters
	}
	text := sanitizeText(input.Text)
	if text == "" {
		return errEmptyText
	}
	if len([]rune(text)) > maxTextLength {
		return errLongText
	}
	mode := input.Mode
	if mode != 0 && mode != 1 && mode != 2 {
		mode = 0
	}
	color := strings.ToUpper(strings.TrimSpace(input.Color))
	if !hexColorPattern.MatchString(color) {
		color = "#FFFFFF"
	}
	seconds := input.Time
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		seconds = 0
	}
	season, title := splitSeason(rawTitle)
	vodKey := buildVodKey(title, season, parseEpisode(input.Episode))
	now := service.now()
	_, err := service.store.CreateGuarded(ctx, Record{
		VodKey: vodKey, Time: seconds, Text: text, Mode: mode, Color: color, UserID: userID, CreatedAt: now,
	}, now.Add(-sendWindow), now.Add(-duplicateWindow), sendMaximum)
	return err
}

// 参数校验失败的几种内部错误。
var (
	errParameters = errors.New("invalid parameters")
	errEmptyText  = errors.New("empty danmaku")
	errLongText   = errors.New("danmaku too long")
)
