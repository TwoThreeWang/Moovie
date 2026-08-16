package contract

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestImplementationRegistersExactlyTheFinalRoutes(t *testing.T) {
	root := filepath.Join("..")
	pattern := regexp.MustCompile(`\brouter\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"`)
	implemented := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range pattern.FindAllStringSubmatch(string(contents), -1) {
			key := match[1] + " " + match[2]
			if previous, duplicate := implemented[key]; duplicate {
				t.Errorf("route %s is registered in both %s and %s", key, previous, path)
			}
			implemented[key] = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	missing := make([]string, 0)
	for _, route := range Routes {
		key := route.Method + " " + route.Path
		if _, exists := implemented[key]; !exists {
			missing = append(missing, key)
		}
		delete(implemented, key)
	}
	extras := make([]string, 0, len(implemented))
	for key := range implemented {
		extras = append(extras, key)
	}
	sort.Strings(missing)
	sort.Strings(extras)
	if len(missing) > 0 || len(extras) > 0 {
		t.Fatalf("implementation route drift: missing=%v extras=%v", missing, extras)
	}
}
