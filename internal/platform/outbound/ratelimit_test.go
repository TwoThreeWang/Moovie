package outbound

import (
	"context"
	"testing"
	"time"
)

func TestLimiterSpacesOutRequestsAndHonorsCooldown(t *testing.T) {
	limiter := NewLimiter(20 * time.Millisecond)
	start := time.Now()
	for index := 0; index < 3; index++ {
		if err := limiter.Wait(context.Background()); err != nil {
			t.Fatalf("wait %d: %v", index, err)
		}
	}
	// 第一次立即放行，后两次各等一个间隔。
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Fatalf("elapsed = %s, want at least 40ms", elapsed)
	}
	limiter.Pause(80 * time.Millisecond)
	paused := time.Now()
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(paused); elapsed < 70*time.Millisecond {
		t.Fatalf("cooldown = %s, want at least 70ms", elapsed)
	}
}

func TestLimiterStopsWaitingWhenContextIsCancelled(t *testing.T) {
	limiter := NewLimiter(time.Hour)
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := limiter.Wait(ctx); err == nil {
		t.Fatal("expected the cancelled context to abort the wait")
	}
}

func TestNilLimiterIsANoop(t *testing.T) {
	var limiter *Limiter
	if err := limiter.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	limiter.Pause(time.Second)
}
