package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/compat"
)

func main() {
	// 对新旧系统发送相同的只读请求，并比较状态码集合、传输错误和 P95 比值。
	oldBase := flag.String("old", "http://127.0.0.1:5007", "legacy server base URL")
	newBase := flag.String("new", "http://127.0.0.1:5008", "refactored server base URL")
	pathsFlag := flag.String("paths", "/,/search?kw=肖申克的救赎,/trends,/sitemap.xml", "comma-separated read-only paths")
	requests := flag.Int("requests", 100, "requests per server and path")
	concurrency := flag.Int("concurrency", 10, "concurrent requests")
	maxRegression := flag.Float64("max-p95-regression", 1.20, "maximum accepted new/old p95 ratio")
	flag.Parse()
	if *requests < 1 || *concurrency < 1 || *maxRegression < 1 {
		fatalf("invalid load-check limits")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	failed := false
	for _, path := range strings.Split(*pathsFlag, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		oldMetrics, oldErr := compat.MeasureEndpoint(ctx, client, *oldBase, path, *requests, *concurrency)
		newMetrics, newErr := compat.MeasureEndpoint(ctx, client, *newBase, path, *requests, *concurrency)
		cancel()
		if oldErr != nil || newErr != nil {
			fmt.Printf("FAIL %s old_error=%v new_error=%v\n", path, oldErr, newErr)
			failed = true
			continue
		}
		// 旧系统 P95 为零时不计算比值，仍通过状态码和错误数判断一致性。
		ratio := 0.0
		if oldMetrics.P95 > 0 {
			ratio = float64(newMetrics.P95) / float64(oldMetrics.P95)
		}
		fmt.Printf("%s old_p50=%s old_p95=%s new_p50=%s new_p95=%s ratio=%.2f old_rps=%.1f new_rps=%.1f\n",
			path, oldMetrics.Median, oldMetrics.P95, newMetrics.Median, newMetrics.P95, ratio, oldMetrics.Throughput, newMetrics.Throughput)
		if oldMetrics.Errors > 0 || newMetrics.Errors > 0 || !reflect.DeepEqual(oldMetrics.Statuses, newMetrics.Statuses) || ratio > *maxRegression {
			fmt.Printf("FAIL %s statuses old=%v new=%v errors old=%d new=%d\n", path, oldMetrics.Statuses, newMetrics.Statuses, oldMetrics.Errors, newMetrics.Errors)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(2)
}
