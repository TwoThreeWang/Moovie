package playback

import (
	"testing"
	"time"
)

func TestPlaybackEventLimiterBoundsPerIPWithoutPersistingIt(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	limiter := newPlaybackEventLimiter(2, time.Minute)
	limiter.now = func() time.Time { return now }
	if !limiter.Allow("192.0.2.1") || !limiter.Allow("192.0.2.1") || limiter.Allow("192.0.2.1") {
		t.Fatal("per-IP event limit was not enforced")
	}
	if !limiter.Allow("192.0.2.2") {
		t.Fatal("one IP exhausted another IP's allowance")
	}
	now = now.Add(time.Minute)
	if !limiter.Allow("192.0.2.1") {
		t.Fatal("event limit did not reset")
	}
}

func TestPlaybackEventLimiterBoundsDistinctClientMemory(t *testing.T) {
	limiter := newPlaybackEventLimiter(1, time.Minute)
	limiter.capacity = 2
	if !limiter.Allow("192.0.2.1") || !limiter.Allow("192.0.2.2") || limiter.Allow("192.0.2.3") || len(limiter.counts) != 2 {
		t.Fatalf("bounded limiter counts = %d", len(limiter.counts))
	}
}
