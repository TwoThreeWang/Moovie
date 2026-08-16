package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/compat"
)

func main() {
	// 正式发布要求 URL 集合和元数据严格兼容；balanced drift 只用于本地写入未冻结时解释边界漂移。
	oldBase := flag.String("old", "http://127.0.0.1:5007", "legacy server base URL")
	newBase := flag.String("new", "http://127.0.0.1:5008", "refactored server base URL")
	strict := flag.Bool("strict", false, "also fail when the new sitemap contains additional URLs")
	allowBalancedDrift := flag.Int("allow-balanced-drift", 0, "local-only allowance for equal missing/extra boundary rows; release checks must keep 0")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second}
	oldEntries, err := compat.FetchSitemapEntries(ctx, client, *oldBase)
	if err != nil {
		fatalf("legacy sitemap: %v", err)
	}
	newEntries, err := compat.FetchSitemapEntries(ctx, client, *newBase)
	if err != nil {
		fatalf("refactored sitemap: %v", err)
	}
	oldURLs, newURLs := make(map[string]struct{}, len(oldEntries)), make(map[string]struct{}, len(newEntries))
	for location := range oldEntries {
		oldURLs[location] = struct{}{}
	}
	for location := range newEntries {
		newURLs[location] = struct{}{}
	}
	missing, extra := compat.CompareSitemapURLs(oldURLs, newURLs)
	// URL 集合与 lastmod 等元数据分别比较，数量相同不能证明 sitemap 内容相同。
	metadata := compat.CompareSitemapMetadata(oldEntries, newEntries)
	for _, location := range missing {
		fmt.Printf("MISSING %s\n", location)
	}
	for _, location := range extra {
		fmt.Printf("EXTRA %s\n", location)
	}
	for _, difference := range metadata {
		fmt.Printf("META %s\n", difference)
	}
	fmt.Printf("summary: old=%d new=%d missing=%d extra=%d metadata=%d\n", len(oldURLs), len(newURLs), len(missing), len(extra), len(metadata))
	if acceptableBalancedDrift(len(oldURLs), len(newURLs), len(missing), len(extra), len(metadata), *allowBalancedDrift) {
		fmt.Printf("EXPLAINED balanced sitemap boundary drift within local allowance=%d; repeat with frozen writes and allowance=0 before release\n", *allowBalancedDrift)
		return
	}
	if len(missing) > 0 || len(metadata) > 0 || (*strict && len(extra) > 0) {
		os.Exit(1)
	}
}

func acceptableBalancedDrift(oldTotal, newTotal, missing, extra, metadata, allowance int) bool {
	return allowance > 0 && oldTotal == newTotal && missing > 0 && missing == extra && missing <= allowance && metadata == 0
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(2)
}
