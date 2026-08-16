package releasecontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightIncludesReadOnlyRefactoredArchitectureAudit(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "scripts", "release", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	for _, expected := range []string{
		"./scripts/release/source-audit.sh --local", "LOCAL_PREFLIGHT", "LOCAL_SITEMAP_BALANCED_DRIFT", "-allow-balanced-drift",
		"go run ./cmd/releaseaudit", "-dsn=$MIGRATION_TARGET_DSN", "-target-env=./.env", "REQUIRE_POPULARITY_SNAPSHOTS",
		"-required-popularity-sources", "-max-popularity-age", "[7/7] refactored burst stability", "go run ./cmd/burstcheck",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("preflight missing %q", expected)
		}
	}
	if strings.Contains(script, "go run ./cmd/releaseaudit -apply") {
		t.Fatal("preflight must not expose an apply mode")
	}
	// 旧系统下线后预检仍必须可执行：OLD_BASE_URL 只能是可选项。
	if strings.Contains(script, "require_env OLD_BASE_URL") {
		t.Fatal("preflight must not require OLD_BASE_URL after the legacy system is retired")
	}
	// SEO 路径契约仍需保留检查入口，只是允许在旧站不可达时跳过。
	if !strings.Contains(script, "go run ./cmd/compatcheck") || !strings.Contains(script, "compat/seo_cases.json") {
		t.Fatal("preflight must keep the SEO route compatibility step")
	}
}

func TestReleaseChecklistPreservesManualBoundaries(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join("..", "..", "docs", "P9_RELEASE_ACCEPTANCE_CHECKLIST.md"))
	if err != nil {
		t.Fatal(err)
	}
	checklist := string(contents)
	for _, expected := range []string{
		"releaseaudit", "至少观察 30 天", "未过期的 `batch_id`",
		"source-audit.sh", "静态检查、Go 测试和 HTTP 200 不能替代本节",
		"cmd/burstcheck", "X-Moovie-Overload", "liveness 使用 `/health`", "JOBS_IN_WEB=false",
	} {
		if !strings.Contains(checklist, expected) {
			t.Fatalf("release checklist missing %q", expected)
		}
	}
}

func TestMigrationRequiresWriteFreezeWithoutBackupGate(t *testing.T) {
	for _, path := range [][]string{{"scripts", "migrate.sh"}, {"cmd", "datamigrate", "main.go"}} {
		contents := readReleaseFile(t, path...)
		if strings.Contains(contents, "backup-restore-verified") {
			t.Fatalf("%s must not require backup restore verification", filepath.Join(path...))
		}
		if !strings.Contains(contents, "write-freeze-confirmed") {
			t.Fatalf("%s must keep the write-freeze gate", filepath.Join(path...))
		}
	}
}

func TestReleaseSourceAuditAndContainerPackagingGuardrails(t *testing.T) {
	sourceAudit := readReleaseFile(t, "scripts", "release", "source-audit.sh")
	for _, expected := range []string{"git -C \"$repository_root\" ls-files --error-unmatch", "release source is not tracked by Git", "SOURCE_INCLUSION_CONFIRMED", "--local", "Git inclusion remains a mandatory release gate"} {
		if !strings.Contains(sourceAudit, expected) {
			t.Fatalf("source audit missing %q", expected)
		}
	}
	dockerfile := readReleaseFile(t, "Dockerfile")
	for _, expected := range []string{"go build -trimpath", "./cmd/web", "./cmd/worker", "USER moovie", "WEB_ROOT=/app/web", "127.0.0.1:5008/health"} {
		if !strings.Contains(dockerfile, expected) {
			t.Fatalf("Dockerfile missing %q", expected)
		}
	}
	compose := readReleaseFile(t, "docker-compose.yml")
	for _, expected := range []string{
		"moovie-new-web", "moovie-new-worker", `"5008:5008"`, "read_only: true", "postgres_default:", "external: true",
		`JOBS_IN_WEB: "false"`, "GOMEMLIMIT", "mem_limit:", "pids_limit:", "DB_MAX_CONNS", "127.0.0.1:5008/health",
		"HTTP_PROXY: socks5://cloudflare-warp:1080", "HTTPS_PROXY: socks5://cloudflare-warp:1080", "NO_PROXY: localhost,127.0.0.1,postgres,danmu-api",
	} {
		if !strings.Contains(compose, expected) {
			t.Fatalf("compose file missing %q", expected)
		}
	}
	buildScript := readReleaseFile(t, "build.sh")
	for _, expected := range []string{`git -C "$repo_root" pull --ff-only`, "docker compose up -d --build --force-recreate", "docker compose ps"} {
		if !strings.Contains(buildScript, expected) {
			t.Fatalf("build script missing %q", expected)
		}
	}
	dockerIgnore := readReleaseFile(t, ".dockerignore")
	if !strings.Contains(dockerIgnore, ".env\n") {
		t.Fatal(".dockerignore must exclude the real environment file")
	}
	if !strings.Contains(dockerIgnore, ".migration-reports\n") {
		t.Fatal(".dockerignore must exclude generated migration reports")
	}
}

func readReleaseFile(t *testing.T, path ...string) string {
	t.Helper()
	parts := append([]string{"..", ".."}, path...)
	contents, err := os.ReadFile(filepath.Join(parts...))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
