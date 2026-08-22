package search

import (
	"context"
	"sync"
	"time"
)

// 熔断参数：连续失败 3 次熔断，冷却 5 分钟。
const (
	breakerFailureThreshold = 3
	breakerCooldown         = 5 * time.Minute
)

// breakerState 是单个资源站的熔断状态：连续失败达到阈值就熔断一段时间，
// 熔断期内每轮只放一个探测请求过去（probing）。
type breakerState struct {
	consecutiveFailures int
	openUntil           time.Time
	probing             bool
}

// Health 同时做两件事：资源站熔断判断，以及按小时聚合健康统计并定期落库 site_stats。
type Health struct {
	enabled       bool
	now           func() time.Time
	store         HealthStatStore
	flushInterval time.Duration

	mu       sync.Mutex
	breakers map[string]*breakerState
	outcomes map[string]map[Outcome]int
	counters map[string]*HealthStat
	cancel   context.CancelFunc
	wait     sync.WaitGroup
}

// NewHealth 创建只在内存里做熔断的健康监控（不落库）。
func NewHealth(enabled bool) *Health {
	return &Health{
		enabled:       enabled,
		now:           time.Now,
		breakers:      make(map[string]*breakerState),
		outcomes:      make(map[string]map[Outcome]int),
		counters:      make(map[string]*HealthStat),
		flushInterval: time.Minute,
	}
}

// NewHealthWithStore 创建带持久化的健康监控，统计每分钟批量写一次。
func NewHealthWithStore(enabled bool, store HealthStatStore) *Health {
	health := NewHealth(enabled)
	health.store = store
	return health
}

// Record 记录一次抓取结果：累加计数，并更新熔断状态。
func (health *Health) Record(siteKey string, outcome Outcome, elapsedMilliseconds int64) {
	if health == nil || siteKey == "" {
		return
	}
	health.mu.Lock()
	defer health.mu.Unlock()
	if health.outcomes[siteKey] == nil {
		health.outcomes[siteKey] = make(map[Outcome]int)
	}
	health.outcomes[siteKey][outcome]++
	counter := health.counters[siteKey]
	if counter == nil {
		counter = &HealthStat{SiteKey: siteKey}
		health.counters[siteKey] = counter
	}
	counter.TotalMs += elapsedMilliseconds
	switch outcome {
	case OutcomeOK:
		counter.OKCount++
	case OutcomeEmpty:
		counter.EmptyCount++
	case OutcomeTimeout:
		counter.TimeoutCount++
	case OutcomeError:
		counter.ErrorCount++
	}

	state := health.breakers[siteKey]
	if state == nil {
		state = &breakerState{}
		health.breakers[siteKey] = state
	}
	switch outcome {
	case OutcomeOK:
		state.consecutiveFailures = 0
		state.openUntil = time.Time{}
		state.probing = false
	case OutcomeEmpty:
		state.probing = false
	case OutcomeTimeout, OutcomeError:
		state.consecutiveFailures++
		state.probing = false
		if state.consecutiveFailures >= breakerFailureThreshold {
			state.openUntil = health.now().Add(breakerCooldown)
		}
	}
}

// Start 启动后台定时落库。
func (health *Health) Start() {
	if health == nil || health.store == nil {
		return
	}
	health.mu.Lock()
	if health.cancel != nil {
		health.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	health.cancel = cancel
	health.wait.Add(1)
	health.mu.Unlock()
	go func() {
		defer health.wait.Done()
		ticker := time.NewTicker(health.flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = health.flush(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止定时器并把内存里剩余的统计刷进数据库。
func (health *Health) Stop(ctx context.Context) error {
	if health == nil || health.store == nil {
		return nil
	}
	health.mu.Lock()
	cancel := health.cancel
	health.cancel = nil
	health.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		health.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return health.flush(ctx)
}

// flush 把内存计数按小时桶写入 site_stats，写完清零。
func (health *Health) flush(ctx context.Context) error {
	health.mu.Lock()
	if len(health.counters) == 0 {
		health.mu.Unlock()
		return nil
	}
	bucket := health.now().Truncate(time.Hour)
	stats := make([]HealthStat, 0, len(health.counters))
	for _, counter := range health.counters {
		copy := *counter
		copy.Bucket = bucket
		stats = append(stats, copy)
	}
	health.counters = make(map[string]*HealthStat)
	health.mu.Unlock()
	return health.store.AddHealthStats(ctx, stats)
}

// FilterAvailable 过滤掉处于熔断中的资源站。
// 兜底：如果全部被熔断，则退回原始列表，宁可慢也不能一条结果都搜不到。
func (health *Health) FilterAvailable(sites []Site) (available []Site, skipped []string) {
	if health == nil || !health.enabled || len(sites) == 0 {
		return sites, nil
	}
	now := health.now()
	health.mu.Lock()
	for _, site := range sites {
		state := health.breakers[site.Key]
		if state == nil || now.After(state.openUntil) {
			available = append(available, site)
			continue
		}
		if !state.probing {
			state.probing = true
			available = append(available, site)
			continue
		}
		skipped = append(skipped, site.Key)
	}
	health.mu.Unlock()
	if len(available) == 0 {
		return sites, nil
	}
	return available, skipped
}

// TrippedUntil 返回熔断到期时间，供后台页面展示。
func (health *Health) TrippedUntil(siteKey string) time.Time {
	if health == nil || !health.enabled {
		return time.Time{}
	}
	health.mu.Lock()
	defer health.mu.Unlock()
	state := health.breakers[siteKey]
	if state == nil || !health.now().Before(state.openUntil) {
		return time.Time{}
	}
	return state.openUntil
}

// classifyOutcome 把抓取结果分成 ok/empty/timeout/error 四类。
func classifyOutcome(ctx context.Context, err error, itemCount int) Outcome {
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return OutcomeTimeout
		}
		return OutcomeError
	}
	if itemCount == 0 {
		return OutcomeEmpty
	}
	return OutcomeOK
}
