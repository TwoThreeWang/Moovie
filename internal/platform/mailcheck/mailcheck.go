// Package mailcheck 判断邮箱域名能不能收信，只用标准库。
//
// 规则：域名必须有 MX 记录。域名不存在、没有 MX、只有一条 "." 的 Null MX（RFC 7505）、
// 单段域名（如 localhost）都算收不了信。DNS 超时或网络出错时放行，不能因为解析器抖动
// 就把正常用户挡在门外。
//
// 它只能发现「域名拼错 / 域名根本不收信」，拦不住「域名真实、邮箱乱写」和临时邮箱。
package mailcheck

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// 缓存和超时参数。
const (
	// CacheTTL 是有结论的查询结果的缓存时间。临时故障不缓存，下次重新查。
	CacheTTL = time.Hour
	// LookupTimeout 是单次 DNS 查询的超时。
	LookupTimeout = 3 * time.Second
	// maxCacheEntries 限制缓存条数，防止有人用随机域名把内存撑大。
	maxCacheEntries = 10000
)

// Resolver 是 MX 查询接口，*net.Resolver 满足它，测试里换成假的。
type Resolver interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

// Checker 带缓存地检查邮箱域名。并发安全。
type Checker struct {
	resolver Resolver
	timeout  time.Duration
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	deliverable bool
	expires     time.Time
}

// New 创建检查器；resolver 为 nil 时使用 net.DefaultResolver。
func New(resolver Resolver) *Checker {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &Checker{resolver: resolver, timeout: LookupTimeout, now: time.Now, cache: make(map[string]cacheEntry)}
}

// SetClock 替换时钟，仅供测试使用。
func (checker *Checker) SetClock(now func() time.Time) { checker.now = now }

// CanReceive 判断邮箱地址的域名能否收信。地址里没有 @ 时返回 false。
func (checker *Checker) CanReceive(ctx context.Context, address string) bool {
	at := strings.LastIndex(address, "@")
	if at < 0 || at == len(address)-1 {
		return false
	}
	return checker.DomainCanReceive(ctx, address[at+1:])
}

// DomainCanReceive 判断域名能否收信。
func (checker *Checker) DomainCanReceive(ctx context.Context, domain string) bool {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if domain == "" || !strings.Contains(domain, ".") {
		return false
	}
	// 非 ASCII 域名（未转 punycode 的国际化域名）标准库解析器处理不了，不做判断、直接放行。
	for index := 0; index < len(domain); index++ {
		if domain[index] >= 0x80 {
			return true
		}
	}
	now := checker.now()
	checker.mu.Lock()
	entry, cached := checker.cache[domain]
	checker.mu.Unlock()
	if cached && now.Before(entry.expires) {
		return entry.deliverable
	}

	lookupContext, cancel := context.WithTimeout(ctx, checker.timeout)
	defer cancel()
	records, err := checker.resolver.LookupMX(lookupContext, domain+".")
	deliverable, definitive := classify(records, err)
	if !definitive {
		slog.Warn("mail domain check inconclusive, allowing", "domain", domain, "error", err)
		return true
	}
	checker.store(domain, deliverable, now)
	return deliverable
}

// classify 把查询结果分成「能收 / 不能收」和「有没有结论」。
func classify(records []*net.MX, err error) (deliverable, definitive bool) {
	if err != nil {
		var dnsError *net.DNSError
		// IsNotFound 覆盖 NXDOMAIN 和「域名存在但没有 MX 记录」两种情况，都是确定的结论。
		if errors.As(err, &dnsError) && dnsError.IsNotFound {
			return false, true
		}
		// 超时、SERVFAIL、网络不可达等都算临时故障。
		return true, false
	}
	usable := 0
	for _, record := range records {
		if host := strings.TrimSuffix(record.Host, "."); host != "" {
			usable++
		}
	}
	// 没有任何可用记录（包括 RFC 7505 的 Null MX：唯一一条记录指向 "."）就是不收信。
	return usable > 0, true
}

// store 写入缓存；表满时先清过期条目，仍然满就清空重来。
func (checker *Checker) store(domain string, deliverable bool, now time.Time) {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if len(checker.cache) >= maxCacheEntries {
		for key, entry := range checker.cache {
			if !now.Before(entry.expires) {
				delete(checker.cache, key)
			}
		}
		if len(checker.cache) >= maxCacheEntries {
			checker.cache = make(map[string]cacheEntry)
		}
	}
	checker.cache[domain] = cacheEntry{deliverable: deliverable, expires: now.Add(CacheTTL)}
}
