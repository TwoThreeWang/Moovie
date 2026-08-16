package search

import (
	"testing"
	"time"
)

func TestCacheExpiresAndEvictsLeastRecentlyUsed(t *testing.T) {
	now := time.Date(2026, time.July, 29, 0, 0, 0, 0, time.UTC)
	cache := NewCache[string](2, time.Hour)
	cache.clock = func() time.Time { return now }
	cache.Set("a", "A")
	cache.Set("b", "B")
	if _, found := cache.Get("a"); !found {
		t.Fatal("expected a cache hit")
	}
	cache.Set("c", "C")
	if _, found := cache.Get("b"); found {
		t.Fatal("least recently used entry b was not evicted")
	}
	now = now.Add(2 * time.Hour)
	if _, found := cache.Get("a"); found {
		t.Fatal("expired entry a was returned")
	}
}
