package service

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
)

// 熔断参数
const (
	// breakerThreshold 连续失败多少次后熔断（只统计 timeout / error，empty 不算）
	breakerThreshold = 3
	// breakerCooldown 熔断持续时长，期间该站点不参与搜索扇出
	breakerCooldown = 5 * time.Minute
	// statFlushInterval 内存计数落库的间隔
	statFlushInterval = time.Minute
)

// siteCounter 单个站点在当前 flush 周期内的累计值
type siteCounter struct {
	ok      int
	empty   int
	timeout int
	err     int
	totalMs int64
}

// breakerState 单个站点的熔断状态
type breakerState struct {
	consecutiveFails int
	openUntil        time.Time
	// probing 表示半开状态下已经放出探针，避免冷却结束瞬间涌入多个请求
	probing bool
}

// SiteHealth 采集站点健康度统计 + 熔断器。
//
// 设计要点：
//  1. 请求路径上只做内存累加，绝不写库；由后台 goroutine 每分钟 flush 一次。
//  2. 熔断只用于搜索扇出（多站点并发，跳过一个不影响可用性），
//     绝不用于 GetDetail —— 那是用户点击某个具体站点链接后的定向请求，
//     跳过会直接导致播放页打不开。
//  3. 任何情况下都保证候选站点不为空，宁可慢也不能全站搜不到。
type SiteHealth struct {
	repo    *repository.SiteStatRepository
	enabled bool // 熔断开关；关闭时只统计不熔断

	mu       sync.Mutex
	counters map[string]*siteCounter
	breakers map[string]*breakerState

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewSiteHealth 创建健康度服务。breakerEnabled 为 false 时只统计、不熔断。
func NewSiteHealth(repo *repository.SiteStatRepository, breakerEnabled bool) *SiteHealth {
	return &SiteHealth{
		repo:     repo,
		enabled:  breakerEnabled,
		counters: make(map[string]*siteCounter),
		breakers: make(map[string]*breakerState),
	}
}

// Record 记录一次采集调用的结果
func (h *SiteHealth) Record(siteKey string, outcome model.SiteCallOutcome, elapsed time.Duration) {
	if h == nil || siteKey == "" {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	c := h.counters[siteKey]
	if c == nil {
		c = &siteCounter{}
		h.counters[siteKey] = c
	}
	c.totalMs += elapsed.Milliseconds()

	b := h.breakers[siteKey]
	if b == nil {
		b = &breakerState{}
		h.breakers[siteKey] = b
	}

	switch outcome {
	case model.SiteCallOK:
		c.ok++
		// 任何一次成功都立刻解除熔断并清零
		b.consecutiveFails = 0
		b.openUntil = time.Time{}
		b.probing = false
	case model.SiteCallEmpty:
		c.empty++
		// 空返回不计入熔断：可能只是没有这部片，不代表站点坏了。
		// 但也不清零，避免"坏站持续返回空"把失败计数一直重置。
		b.probing = false
	case model.SiteCallTimeout, model.SiteCallError:
		if outcome == model.SiteCallTimeout {
			c.timeout++
		} else {
			c.err++
		}
		b.consecutiveFails++
		b.probing = false
		if b.consecutiveFails >= breakerThreshold {
			b.openUntil = time.Now().Add(breakerCooldown)
		}
	}
}

// FilterAvailable 从候选站点中剔除处于熔断中的站点。
//
// 返回值 skipped 是被跳过的站点 key，仅用于日志。
// 兜底：如果过滤后一个站点都不剩，则原样返回全部站点 —— 服务器自身网络抖动
// 会让所有站点同时失败，没有这层兜底会出现"搜什么都没有"的雪崩。
func (h *SiteHealth) FilterAvailable(sites []*model.Site) (available []*model.Site, skipped []string) {
	if h == nil || !h.enabled || len(sites) == 0 {
		return sites, nil
	}

	now := time.Now()

	h.mu.Lock()
	for _, site := range sites {
		b := h.breakers[site.Key]
		if b == nil || now.After(b.openUntil) {
			available = append(available, site)
			continue
		}
		// 冷却期内放一个探针，让恢复的站点能尽快回到候选池
		if !b.probing {
			b.probing = true
			available = append(available, site)
			continue
		}
		skipped = append(skipped, site.Key)
	}
	h.mu.Unlock()

	if len(available) == 0 {
		log.Printf("[SiteHealth] 全部 %d 个站点均处于熔断中，兜底放行全部站点", len(sites))
		return sites, nil
	}
	return available, skipped
}

// TrippedUntil 返回站点的熔断截止时间；未熔断时返回零值
func (h *SiteHealth) TrippedUntil(siteKey string) time.Time {
	if h == nil {
		return time.Time{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	b := h.breakers[siteKey]
	if b == nil || time.Now().After(b.openUntil) {
		return time.Time{}
	}
	return b.openUntil
}

// Start 启动后台 flush 循环
func (h *SiteHealth) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		ticker := time.NewTicker(statFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				h.flush()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止后台循环，并把内存里剩余的计数落库
func (h *SiteHealth) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	h.wg.Wait()
	h.flush()
}

// flush 把内存计数写入数据库，并清空内存
func (h *SiteHealth) flush() {
	h.mu.Lock()
	if len(h.counters) == 0 {
		h.mu.Unlock()
		return
	}
	snapshot := h.counters
	h.counters = make(map[string]*siteCounter)
	h.mu.Unlock()

	bucket := time.Now().Truncate(time.Hour)
	stats := make([]*model.SiteStat, 0, len(snapshot))
	for key, c := range snapshot {
		stats = append(stats, &model.SiteStat{
			SiteKey:      key,
			Bucket:       bucket,
			OKCount:      c.ok,
			EmptyCount:   c.empty,
			TimeoutCount: c.timeout,
			ErrorCount:   c.err,
			TotalMs:      c.totalMs,
		})
	}

	if err := h.repo.AddBatch(stats); err != nil {
		log.Printf("[SiteHealth] 统计落库失败（本轮 %d 条计数丢弃）: %v", len(stats), err)
	}
}

// ClassifyError 把一次采集调用的返回值归类为四态之一
func ClassifyError(ctx context.Context, err error, itemCount int) model.SiteCallOutcome {
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return model.SiteCallTimeout
		}
		return model.SiteCallError
	}
	if itemCount == 0 {
		return model.SiteCallEmpty
	}
	return model.SiteCallOK
}
