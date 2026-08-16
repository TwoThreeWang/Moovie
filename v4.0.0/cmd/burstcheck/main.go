package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/compat"
)

type healthCounts struct {
	checks   atomic.Int64
	failures atomic.Int64
}

func main() {
	// burstcheck 只允许对调用方指定的读取路径施压，并同时用独立 Client 高频检查 /health。
	target := flag.String("target", "http://127.0.0.1:5008", "server base URL")
	path := flag.String("path", "/discover/movie", "read-only path to burst")
	requests := flag.Int("requests", 1000, "total burst requests")
	concurrency := flag.Int("concurrency", 100, "concurrent workers")
	requireShedding := flag.Bool("require-shedding", false, "require at least one controlled 503 response")
	maximumP95 := flag.Duration("max-p95", 5*time.Second, "maximum accepted p95 latency")
	flag.Parse()
	if *requests < 1 || *concurrency < 1 || *maximumP95 <= 0 {
		fatalf("invalid burst-check limits")
	}
	base, err := url.Parse(*target)
	if err != nil || base.Scheme == "" || base.Host == "" {
		fatalf("invalid target URL")
	}

	loadClient := &http.Client{Timeout: 35 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	healthClient := &http.Client{Timeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	monitorContext, stopMonitor := context.WithCancel(ctx)
	counts := &healthCounts{}
	monitorDone := make(chan struct{})
	// 健康探针与负载使用不同超时和连接池，避免负载 Client 自身排队掩盖进程失活。
	go monitorHealth(monitorContext, healthClient, base.ResolveReference(&url.URL{Path: "/health"}).String(), counts, monitorDone)

	metrics, measureErr := compat.MeasureEndpoint(ctx, loadClient, *target, *path, *requests, *concurrency)
	stopMonitor()
	<-monitorDone
	finalHealth := probe(healthClient, base.ResolveReference(&url.URL{Path: "/health"}).String())
	finalReady := probe(healthClient, base.ResolveReference(&url.URL{Path: "/ready"}).String())

	result := map[string]any{
		"target": *target, "path": *path, "requests": metrics.Requests, "concurrency": *concurrency,
		"statuses": metrics.Statuses, "transport_errors": metrics.Errors,
		"p50": metrics.Median.String(), "p95": metrics.P95.String(), "max": metrics.Maximum.String(),
		"elapsed": metrics.Elapsed.String(), "requests_per_second": metrics.Throughput,
		"health_checks": counts.checks.Load(), "health_failures": counts.failures.Load(),
		"final_health_status": finalHealth, "final_ready_status": finalReady,
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))

	// 受控 503 属于允许的主动降载；传输错误、探针失败和其他状态码仍会让门禁失败。
	failed := measureErr != nil || metrics.Errors > 0 || metrics.Statuses[http.StatusOK] == 0 || metrics.P95 > *maximumP95 ||
		counts.failures.Load() > 0 || finalHealth != http.StatusOK || finalReady != http.StatusOK
	for status, count := range metrics.Statuses {
		if count > 0 && status != http.StatusOK && status != http.StatusServiceUnavailable {
			failed = true
		}
	}
	if *requireShedding && metrics.Statuses[http.StatusServiceUnavailable] == 0 {
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}

func monitorHealth(ctx context.Context, client *http.Client, endpoint string, counts *healthCounts, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			counts.checks.Add(1)
			if probe(client, endpoint) != http.StatusOK {
				counts.failures.Add(1)
			}
		case <-ctx.Done():
			return
		}
	}
}

func probe(client *http.Client, endpoint string) int {
	response, err := client.Get(endpoint)
	if err != nil {
		return 0
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	_ = response.Body.Close()
	return response.StatusCode
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(2)
}
