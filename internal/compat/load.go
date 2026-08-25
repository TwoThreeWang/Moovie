package compat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"
)

// LoadMetrics 是压测结果：请求数、错误数、各状态码分布和耗时分位数。
type LoadMetrics struct {
	Requests   int           `json:"requests"`
	Errors     int           `json:"errors"`
	Statuses   map[int]int   `json:"statuses"`
	Minimum    time.Duration `json:"minimum"`
	Median     time.Duration `json:"median"`
	P95        time.Duration `json:"p95"`
	Maximum    time.Duration `json:"maximum"`
	Elapsed    time.Duration `json:"elapsed"`
	Throughput float64       `json:"throughput"`
}

// MeasureEndpoint 用固定并发压测一个接口，响应体读完即丢只统计耗时。
func MeasureEndpoint(ctx context.Context, client *http.Client, baseURL, path string, requests, concurrency int) (LoadMetrics, error) {
	if requests < 1 || concurrency < 1 {
		return LoadMetrics{}, fmt.Errorf("requests and concurrency must be positive")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return LoadMetrics{}, err
	}
	relative, err := url.Parse(path)
	if err != nil {
		return LoadMetrics{}, err
	}
	target := base.ResolveReference(relative).String()
	if concurrency > requests {
		concurrency = requests
	}
	type result struct {
		duration time.Duration
		status   int
		err      error
	}
	jobs := make(chan struct{})
	results := make(chan result, requests)
	var workers sync.WaitGroup
	for index := 0; index < concurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range jobs {
				started := time.Now()
				request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
				if requestErr != nil {
					results <- result{duration: time.Since(started), err: requestErr}
					continue
				}
				response, requestErr := client.Do(request)
				if requestErr != nil {
					results <- result{duration: time.Since(started), err: requestErr}
					continue
				}
				readBytes, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes+1))
				closeErr := response.Body.Close()
				if readErr == nil {
					readErr = closeErr
				}
				if readBytes > maxResponseBytes {
					readErr = fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
				}
				results <- result{duration: time.Since(started), status: response.StatusCode, err: readErr}
			}
		}()
	}
	started := time.Now()
	go func() {
		defer close(jobs)
		for index := 0; index < requests; index++ {
			select {
			case jobs <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	metrics := LoadMetrics{Requests: requests, Statuses: make(map[int]int)}
	durations := make([]time.Duration, 0, requests)
	completed := 0
	for result := range results {
		completed++
		durations = append(durations, result.duration)
		if result.err != nil {
			metrics.Errors++
		} else {
			metrics.Statuses[result.status]++
		}
	}
	metrics.Elapsed = time.Since(started)
	if completed != requests {
		metrics.Errors += requests - completed
	}
	if len(durations) > 0 {
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		metrics.Minimum = durations[0]
		metrics.Median = durations[percentileIndex(len(durations), 50)]
		metrics.P95 = durations[percentileIndex(len(durations), 95)]
		metrics.Maximum = durations[len(durations)-1]
	}
	if metrics.Elapsed > 0 {
		metrics.Throughput = float64(completed) / metrics.Elapsed.Seconds()
	}
	return metrics, nil
}

// percentileIndex 计算分位数在有序切片中的下标。
func percentileIndex(length, percentile int) int {
	if length <= 1 {
		return 0
	}
	index := (length*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > length {
		index = length
	}
	return index - 1
}
