package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/compat"
)

func main() {
	// compatcheck 按 manifest 请求新旧系统，并只放行清单中有明确原因的差异。
	oldBase := flag.String("old", "http://127.0.0.1:5007", "legacy server base URL")
	newBase := flag.String("new", "http://127.0.0.1:5008", "refactored server base URL")
	manifestPath := flag.String("manifest", "./compat/seo_cases.json", "compatibility manifest path")
	requireAll := flag.Bool("require-all", false, "fail when fixture environment variables are missing")
	flag.Parse()

	manifestFile, err := os.Open(*manifestPath)
	if err != nil {
		fatalf("open manifest: %v", err)
	}
	defer manifestFile.Close()

	manifest, err := compat.LoadManifest(manifestFile)
	if err != nil {
		fatalf("load manifest: %v", err)
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		// 不自动跟随重定向，因为 3xx 状态和 Location 本身就是兼容契约的一部分。
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	failed := 0
	skipped := 0
	explainedCount := 0
	for _, testCase := range manifest.Cases {
		missing := missingEnvironment(testCase.RequiredEnv)
		if len(missing) > 0 {
			fmt.Printf("SKIP %s: missing %s\n", testCase.Name, strings.Join(missing, ", "))
			skipped++
			if *requireAll {
				failed++
			}
			continue
		}
		testCase.Path = os.ExpandEnv(testCase.Path)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		oldSnapshot, oldErr := compat.Fetch(ctx, client, *oldBase, testCase)
		newSnapshot, newErr := compat.Fetch(ctx, client, *newBase, testCase)
		cancel()
		if oldErr != nil || newErr != nil {
			fmt.Printf("FAIL %s: old_error=%v new_error=%v\n", testCase.Name, oldErr, newErr)
			failed++
			continue
		}

		differences := compat.Compare(oldSnapshot, newSnapshot)
		if len(differences) == 0 {
			fmt.Printf("PASS %s %s\n", testCase.Name, testCase.Path)
			continue
		}
		unexpected, explained := compat.FilterExpectedDifferences(differences, testCase.ExpectedDifferences)
		if len(explained) > 0 {
			explainedCount += len(explained)
			fmt.Printf("EXPLAINED %s: %s\n", testCase.Name, testCase.DifferenceReason)
			for _, difference := range explained {
				fmt.Printf("  - %s\n", difference)
			}
		}
		if len(unexpected) == 0 {
			fmt.Printf("PASS %s %s\n", testCase.Name, testCase.Path)
			continue
		}

		fmt.Printf("FAIL %s %s\n", testCase.Name, testCase.Path)
		for _, difference := range unexpected {
			fmt.Printf("  - %s\n", difference)
		}
		failed++
	}

	fmt.Printf("summary: total=%d failed=%d skipped=%d explained=%d\n", len(manifest.Cases), failed, skipped, explainedCount)
	if failed > 0 {
		os.Exit(1)
	}
}

func missingEnvironment(keys []string) []string {
	missing := make([]string, 0)
	for _, key := range keys {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			missing = append(missing, key)
		}
	}
	return missing
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
