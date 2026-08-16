package danmaku

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	externalMaximum = 4000
	localMaximum    = 2000
	maxTextLength   = 50
	sendMaximum     = 10
	sendWindow      = time.Minute
	duplicateWindow = 5 * time.Minute
	upstreamTimeout = 25 * time.Second
)

type Service struct {
	store    Store
	upstream *upstreamClient
	hits     *ttlCache[[]Item]
	misses   *ttlCache[bool]
	group    singleflight.Group
	limiter  *ipLimiter
	now      func() time.Time
}

func NewService(store Store, httpClient *http.Client, apiBase string) *Service {
	service := &Service{
		store: store, hits: newTTLCache[[]Item](80, 12*time.Hour), misses: newTTLCache[bool](500, 20*time.Minute),
		limiter: newIPLimiter(20, time.Minute), now: time.Now,
	}
	if strings.TrimSpace(apiBase) != "" {
		service.upstream = newUpstreamClient(httpClient, apiBase)
	}
	return service
}

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

type SendInput struct {
	Title   string  `json:"title"`
	Episode string  `json:"episode"`
	Text    string  `json:"text"`
	Time    float64 `json:"time"`
	Mode    int     `json:"mode"`
	Color   string  `json:"color"`
}

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

var (
	errParameters = errors.New("invalid parameters")
	errEmptyText  = errors.New("empty danmaku")
	errLongText   = errors.New("danmaku too long")
)

type cacheValue[T any] struct {
	value     T
	expiresAt time.Time
	sequence  uint64
}

type ttlCache[T any] struct {
	mu       sync.Mutex
	values   map[string]cacheValue[T]
	capacity int
	ttl      time.Duration
	sequence uint64
}

func newTTLCache[T any](capacity int, ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{values: make(map[string]cacheValue[T]), capacity: capacity, ttl: ttl}
}

func (cache *ttlCache[T]) Get(key string) (T, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	var zero T
	entry, exists := cache.values[key]
	if !exists || time.Now().After(entry.expiresAt) {
		delete(cache.values, key)
		return zero, false
	}
	cache.sequence++
	entry.sequence = cache.sequence
	cache.values[key] = entry
	return entry.value, true
}

func (cache *ttlCache[T]) Set(key string, value T) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.sequence++
	cache.values[key] = cacheValue[T]{value: value, expiresAt: time.Now().Add(cache.ttl), sequence: cache.sequence}
	if len(cache.values) <= cache.capacity {
		return
	}
	oldestKey := ""
	oldest := ^uint64(0)
	for candidate, entry := range cache.values {
		if entry.sequence < oldest {
			oldestKey, oldest = candidate, entry.sequence
		}
	}
	delete(cache.values, oldestKey)
}

type ipCount struct {
	count int
	reset time.Time
}

type ipLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	counts   map[string]ipCount
	capacity int
}

func newIPLimiter(maximum int, window time.Duration) *ipLimiter {
	return &ipLimiter{max: maximum, window: window, counts: make(map[string]ipCount), capacity: 8192}
}

func (limiter *ipLimiter) Allow(ip string) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	entry, exists := limiter.counts[ip]
	if !exists || now.After(entry.reset) {
		if !exists && len(limiter.counts) >= limiter.capacity {
			for key, candidate := range limiter.counts {
				if now.After(candidate.reset) {
					delete(limiter.counts, key)
				}
			}
			if len(limiter.counts) >= limiter.capacity {
				return false
			}
		}
		limiter.counts[ip] = ipCount{count: 1, reset: now.Add(limiter.window)}
		return true
	}
	if entry.count >= limiter.max {
		return false
	}
	entry.count++
	limiter.counts[ip] = entry
	return true
}
