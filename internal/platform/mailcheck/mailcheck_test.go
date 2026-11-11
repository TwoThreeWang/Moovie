package mailcheck

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// fakeResolver 按域名返回预设结果，并记录查询次数。
type fakeResolver struct {
	answers map[string]fakeAnswer
	calls   map[string]int
}

type fakeAnswer struct {
	records []*net.MX
	err     error
	wait    bool // 一直等到 ctx 超时，模拟解析器无响应
}

func (resolver *fakeResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	resolver.calls[name]++
	answer, ok := resolver.answers[name]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	}
	if answer.wait {
		<-ctx.Done()
		return nil, &net.DNSError{Err: "i/o timeout", Name: name, IsTimeout: true}
	}
	return answer.records, answer.err
}

func newFake() *fakeResolver {
	return &fakeResolver{calls: map[string]int{}, answers: map[string]fakeAnswer{
		"example.com.":      {records: []*net.MX{{Host: "mx1.example.com.", Pref: 10}}},
		"nullmx.example.":   {records: []*net.MX{{Host: ".", Pref: 0}}},
		"nomx.example.":     {err: &net.DNSError{Err: "no such host", Name: "nomx.example.", IsNotFound: true}},
		"servfail.example.": {err: &net.DNSError{Err: "server misbehaving", Name: "servfail.example.", IsTemporary: true}},
		"slow.example.":     {wait: true},
		"broken.example.":   {err: errors.New("network is unreachable")},
		"empty.example.":    {records: nil},
	}}
}

func TestDomainCanReceive(t *testing.T) {
	checker := New(newFake())
	checker.timeout = 20 * time.Millisecond
	for _, testCase := range []struct {
		address string
		want    bool
	}{
		{"user@example.com", true},
		{"user@EXAMPLE.com.", true},
		{"user@missing.example", false}, // NXDOMAIN
		{"user@nomx.example", false},    // 域名存在但没有 MX
		{"user@nullmx.example", false},  // RFC 7505 Null MX
		{"user@empty.example", false},   // 查询成功但没有可用记录
		{"user@localhost", false},       // 单段域名
		{"user@", false},
		{"no-at-sign", false},
		{"user@servfail.example", true}, // 临时故障放行
		{"user@slow.example", true},     // 超时放行
		{"user@broken.example", true},   // 网络错误放行
		{"user@例子.中国", true},            // 未转换的国际化域名不做判断
	} {
		if got := checker.CanReceive(context.Background(), testCase.address); got != testCase.want {
			t.Fatalf("CanReceive(%q) = %v, want %v", testCase.address, got, testCase.want)
		}
	}
}

func TestDefinitiveResultsAreCachedForAnHourButFailuresAreNot(t *testing.T) {
	resolver := newFake()
	checker := New(resolver)
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	checker.SetClock(func() time.Time { return now })

	for range 3 {
		checker.DomainCanReceive(context.Background(), "example.com")
		checker.DomainCanReceive(context.Background(), "missing.example")
		checker.DomainCanReceive(context.Background(), "servfail.example")
	}
	if resolver.calls["example.com."] != 1 || resolver.calls["missing.example."] != 1 {
		t.Fatalf("definitive results not cached: %v", resolver.calls)
	}
	if resolver.calls["servfail.example."] != 3 {
		t.Fatalf("temporary failures must not be cached: %v", resolver.calls)
	}

	now = now.Add(CacheTTL + time.Second)
	checker.DomainCanReceive(context.Background(), "example.com")
	if resolver.calls["example.com."] != 2 {
		t.Fatalf("cache did not expire: %v", resolver.calls)
	}
}

func TestCacheIsBounded(t *testing.T) {
	checker := New(newFake())
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for index := 0; index < maxCacheEntries; index++ {
		checker.cache[string(rune('a'+index%26))+time.Duration(index).String()] = cacheEntry{expires: now.Add(time.Hour)}
	}
	checker.store("example.com", true, now)
	if len(checker.cache) > maxCacheEntries {
		t.Fatalf("cache grew past limit: %d", len(checker.cache))
	}
}
