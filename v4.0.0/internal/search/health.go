package search

import (
	"context"
	"sync"
	"time"
)

const (
	breakerFailureThreshold = 3
	breakerCooldown         = 5 * time.Minute
)

type breakerState struct {
	consecutiveFailures int
	openUntil           time.Time
	probing             bool
}

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

func NewHealthWithStore(enabled bool, store HealthStatStore) *Health {
	health := NewHealth(enabled)
	health.store = store
	return health
}

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
