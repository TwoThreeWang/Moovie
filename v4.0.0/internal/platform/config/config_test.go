package config

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDotEnvLoadsValuesWithoutOverridingProcessEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# comment\nexport APP_ENV=production\nSITE_URL=\"https://example.com\"\nEMPTY=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APP_ENV", "test")
	os.Unsetenv("SITE_URL")
	os.Unsetenv("EMPTY")
	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv() error = %v", err)
	}
	if os.Getenv("APP_ENV") != "test" || os.Getenv("SITE_URL") != "https://example.com" {
		t.Fatalf("loaded env = APP_ENV=%q SITE_URL=%q", os.Getenv("APP_ENV"), os.Getenv("SITE_URL"))
	}
	if value, exists := os.LookupEnv("EMPTY"); !exists || value != "" {
		t.Fatalf("empty env = %q/%v", value, exists)
	}
}

func TestLoadDotEnvAllowsMissingFileAndRejectsMalformedLines(t *testing.T) {
	if err := LoadDotEnv(filepath.Join(t.TempDir(), "missing.env")); err != nil {
		t.Fatalf("missing file error = %v", err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("not-an-assignment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(path); err == nil {
		t.Fatal("malformed env file accepted")
	}
}

func TestDatabaseConfigFromDotEnvBuildsEscapedDSNWithoutMutatingEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	contents := "DB_HOST=db.example\nDB_PORT=5544\nDB_USER=movie user\nDB_PASSWORD='p@ss/word'\nDB_NAME=moovie_v2\nDB_SSLMODE=require\nDB_TIMEZONE=UTC\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DB_NAME", "process_database")

	cfg, err := DatabaseConfigFromDotEnv(path)
	if err != nil {
		t.Fatalf("DatabaseConfigFromDotEnv() error = %v", err)
	}
	parsed, err := url.Parse(cfg.DSN())
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	password, _ := parsed.User.Password()
	if parsed.Host != "db.example:5544" || parsed.User.Username() != "movie user" || password != "p@ss/word" || parsed.Path != "/moovie_v2" {
		t.Fatalf("parsed DSN = host=%q user=%q password=%q path=%q", parsed.Host, parsed.User.Username(), password, parsed.Path)
	}
	if os.Getenv("DB_NAME") != "process_database" {
		t.Fatalf("process DB_NAME was mutated: %q", os.Getenv("DB_NAME"))
	}
}

func TestDatabaseConfigFromDotEnvRequiresExistingFileAndDatabaseName(t *testing.T) {
	if _, err := DatabaseConfigFromDotEnv(filepath.Join(t.TempDir(), "missing.env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v", err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("DB_HOST=localhost\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := DatabaseConfigFromDotEnv(path); err == nil || !strings.Contains(err.Error(), "DB_NAME") {
		t.Fatalf("missing DB_NAME error = %v", err)
	}
}

func TestLoadUsesIsolatedDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("DB_NAME", "")
	t.Setenv("DB_ENABLED", "")
	t.Setenv("DB_AUTO_MIGRATE", "")
	t.Setenv("PORT", "")
	t.Setenv("SEARCH_SOURCE_TIMEOUT_SECONDS", "")
	t.Setenv("SEARCH_TOTAL_TIMEOUT_SECONDS", "")
	t.Setenv("SEARCH_CACHE_MINUTES", "")
	t.Setenv("SEARCH_CACHE_ENTRIES", "")
	t.Setenv("SEARCH_SOURCE_MAX_CONCURRENCY", "")
	t.Setenv("SEARCH_BACKGROUND_MAX_CONCURRENCY", "")
	t.Setenv("SEARCH_BREAKER_ENABLED", "")
	t.Setenv("HTTP_MAX_IN_FLIGHT", "")
	t.Setenv("HTTP_MAX_HEAVY_IN_FLIGHT", "")
	t.Setenv("HTTP_MAX_IMAGE_IN_FLIGHT", "")
	t.Setenv("HTTP_QUEUE_TIMEOUT_MILLISECONDS", "")
	t.Setenv("HTTP_REQUEST_TIMEOUT_SECONDS", "")
	t.Setenv("HTTP_MAX_BODY_BYTES", "")
	t.Setenv("HTTP_MAX_HEADER_BYTES", "")
	t.Setenv("HTTP_MAX_CONNECTIONS", "")
	t.Setenv("HTTP_ACCESS_LOG_SAMPLE_PERCENT", "")
	t.Setenv("HTTP_ACCESS_LOG_MAX_PER_SECOND", "")
	t.Setenv("OUTBOUND_MAX_CONNS_PER_HOST", "")
	t.Setenv("DB_MAX_CONNS", "")
	t.Setenv("RESOURCE_MATCH_SHADOW", "")
	t.Setenv("RESOURCE_MATCH_AUTO_APPLY", "")
	t.Setenv("POPULARITY_REFRESH_MINUTES", "")
	t.Setenv("MEDIA_AUTO_MATCH_THRESHOLD", "")
	t.Setenv("MEDIA_REVIEW_MATCH_THRESHOLD", "")
	t.Setenv("TMDB_API_TOKEN", "")
	t.Setenv("OLLAMA_HOST", "")
	t.Setenv("OLLAMA_MODEL", "")
	t.Setenv("CF_GATEWAY_URL", "")
	t.Setenv("CF_API_TOKEN", "")
	t.Setenv("CF_AI_MODEL", "")
	t.Setenv("DANMU_API_BASE", "")
	t.Setenv("JOBS_IN_WEB", "")
	t.Setenv("WORKER_POLL_SECONDS", "")
	t.Setenv("WORKER_CONCURRENCY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "5008" {
		t.Fatalf("Port = %q, want 5008", cfg.Port)
	}
	if cfg.Database.Name != "moovie_new" {
		t.Fatalf("Database.Name = %q, want moovie_new", cfg.Database.Name)
	}
	if cfg.Database.Enabled || !cfg.Database.Migrate {
		t.Fatalf("database enable/migrate defaults changed: %+v", cfg.Database)
	}
	if cfg.Search.SourceTimeout != 10*time.Second || cfg.Search.TotalTimeout != 30*time.Second {
		t.Fatalf("search timeouts = %s/%s", cfg.Search.SourceTimeout, cfg.Search.TotalTimeout)
	}
	if cfg.Search.CacheTTL != 3*time.Hour || cfg.Search.CacheEntries != 200 || !cfg.Search.BreakerEnabled {
		t.Fatalf("search cache/breaker defaults changed: %+v", cfg.Search)
	}
	if cfg.Search.SourceMaxConcurrency != 6 || cfg.Search.BackgroundMaxConcurrency != 8 {
		t.Fatalf("search concurrency defaults = %+v", cfg.Search)
	}
	if cfg.HTTP.MaxInFlight != 64 || cfg.HTTP.MaxHeavyInFlight != 12 || cfg.HTTP.MaxImageInFlight != 24 ||
		cfg.HTTP.QueueTimeout != 100*time.Millisecond || cfg.HTTP.RequestTimeout != 30*time.Second ||
		cfg.HTTP.MaxBodyBytes != 1<<20 || cfg.HTTP.MaxHeaderBytes != 64<<10 || cfg.HTTP.MaxConnections != 512 ||
		cfg.HTTP.AccessLogSamplePercent != 100 || cfg.HTTP.AccessLogMaxPerSecond != 100 {
		t.Fatalf("HTTP resource defaults = %+v", cfg.HTTP)
	}
	if cfg.OutboundMaxConnsPerHost != 12 || cfg.Database.MaxConns != 12 {
		t.Fatalf("connection defaults = outbound:%d database:%+v", cfg.OutboundMaxConnsPerHost, cfg.Database)
	}
	if cfg.Search.MediaAutoMatchThreshold != 0.88 || cfg.Search.MediaReviewMatchThreshold != 0.68 {
		t.Fatalf("media match thresholds = %+v", cfg.Search)
	}
	if !cfg.Search.ResourceMatchShadow || cfg.Search.ResourceMatchAutoApply {
		t.Fatalf("resource match rollout defaults must remain shadow-only: %+v", cfg.Search)
	}
	if cfg.Popularity.RefreshInterval != 30*time.Minute {
		t.Fatalf("popularity refresh default = %+v", cfg.Popularity)
	}
	if cfg.Catalog.OllamaHost != "http://localhost:11434" || cfg.Catalog.OllamaModel != "quentinz/bge-base-zh-v1.5" || cfg.Catalog.CFAIModel != "custom-alibaba-coding/kimi-k2.5" {
		t.Fatalf("catalog external defaults changed: %+v", cfg.Catalog)
	}
	if cfg.Danmaku.APIBase != "" {
		t.Fatalf("danmaku API base = %q", cfg.Danmaku.APIBase)
	}
	if !cfg.JobsInWeb {
		t.Fatal("JOBS_IN_WEB default must preserve single-process scheduling")
	}
	if cfg.Worker.Poll != 2*time.Second || cfg.Worker.Concurrency != 4 {
		t.Fatalf("Worker = %+v", cfg.Worker)
	}
}

func TestLoadUsesSampledAccessLogsInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("SITE_URL", "https://example.com")
	t.Setenv("DB_ENABLED", "true")
	t.Setenv("DB_NAME", "moovie_new")
	t.Setenv("APP_SECRET", strings.Repeat("s", minimumProductionSecretBytes))
	t.Setenv("HTTP_ACCESS_LOG_SAMPLE_PERCENT", "")
	t.Setenv("HTTP_ACCESS_LOG_MAX_PER_SECOND", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.AccessLogSamplePercent != 10 || cfg.HTTP.AccessLogMaxPerSecond != 20 {
		t.Fatalf("production access log budget = %+v, want 10 percent and 20 per second", cfg.HTTP)
	}
}

func TestLoadRejectsUnsafeResourceLimits(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{key: "HTTP_MAX_HEAVY_IN_FLIGHT", value: "65"},
		{key: "HTTP_MAX_CONNECTIONS", value: "32"},
		{key: "SEARCH_SOURCE_MAX_CONCURRENCY", value: "65"},
		{key: "OUTBOUND_MAX_CONNS_PER_HOST", value: "129"},
		{key: "DB_MAX_CONNS", value: "101"},
		{key: "WORKER_CONCURRENCY", value: "65"},
		{key: "HTTP_ACCESS_LOG_SAMPLE_PERCENT", value: "101"},
		{key: "HTTP_ACCESS_LOG_MAX_PER_SECOND", value: "1001"},
	}
	for _, testCase := range tests {
		t.Run(testCase.key, func(t *testing.T) {
			t.Setenv("APP_ENV", "test")
			t.Setenv(testCase.key, testCase.value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted %s=%s", testCase.key, testCase.value)
			}
		})
	}
}

func TestLoadAllowsPopularityRefreshOverride(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("DB_ENABLED", "true")
	t.Setenv("POPULARITY_REFRESH_MINUTES", "45")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Popularity.RefreshInterval != 45*time.Minute {
		t.Fatalf("popularity refresh interval = %+v", cfg.Popularity)
	}
}

func TestLoadAllowsExplicitResourceMatchRolloutFlags(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("RESOURCE_MATCH_SHADOW", "false")
	t.Setenv("RESOURCE_MATCH_AUTO_APPLY", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Search.ResourceMatchShadow || !cfg.Search.ResourceMatchAutoApply {
		t.Fatalf("resource match rollout flags = %+v", cfg.Search)
	}
}

func TestLoadRejectsOverlappingMediaMatchThresholds(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("MEDIA_AUTO_MATCH_THRESHOLD", "0.70")
	t.Setenv("MEDIA_REVIEW_MATCH_THRESHOLD", "0.70")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MEDIA_REVIEW_MATCH_THRESHOLD") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestValidateRequiresDatabaseForSeparateWorkerMode(t *testing.T) {
	cfg := Config{Env: "test", Port: "5008", SiteName: "Moovie影牛", SiteURL: "http://localhost:5008", WebRoot: "./web", Database: DatabaseConfig{Name: "moovie_new"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want separate-worker database requirement")
	}
}

func TestValidateRejectsLegacyDatabaseName(t *testing.T) {
	for _, name := range []string{"moovie", "Moovie", "MOOVIE"} {
		cfg := Config{
			Env:      "development",
			Port:     "5008",
			SiteName: "Moovie影牛",
			SiteURL:  "http://localhost:5008",
			Database: DatabaseConfig{Name: name},
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate() error = nil for %q, want legacy database rejection", name)
		}
	}
}

func TestValidateRejectsLegacyDatabaseNameInProduction(t *testing.T) {
	cfg := Config{
		Env:       "production",
		Port:      "5008",
		SiteName:  "Moovie影牛",
		SiteURL:   "https://example.com",
		WebRoot:   "./web",
		AppSecret: "a-production-secret",
		Database:  DatabaseConfig{Name: "moovie"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want legacy database rejection")
	}
}

func TestValidateRejectsDefaultProductionSecret(t *testing.T) {
	cfg := validProductionConfig()
	cfg.AppSecret = defaultProductionSecret
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "APP_SECRET") {
		t.Fatalf("Validate() error = %v, want default secret rejection", err)
	}
}

func TestValidateRejectsWeakProductionSecret(t *testing.T) {
	cfg := validProductionConfig()
	cfg.AppSecret = "too-short"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "APP_SECRET") {
		t.Fatalf("Validate() error = %v, want weak secret rejection", err)
	}
}

func TestValidateRejectsInsecureProductionSiteURL(t *testing.T) {
	cfg := validProductionConfig()
	cfg.SiteURL = "http://example.com"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("Validate() error = %v, want production https rejection", err)
	}
}

func TestValidateRejectsProductionWithoutDatabase(t *testing.T) {
	cfg := validProductionConfig()
	cfg.Database.Enabled = false
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "DB_ENABLED") {
		t.Fatalf("Validate() error = %v, want production database rejection", err)
	}
}

func TestValidateAcceptsHardenedProductionConfig(t *testing.T) {
	if err := validProductionConfig().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsRelativeSiteURL(t *testing.T) {
	cfg := validProductionConfig()
	cfg.Env = "test"
	cfg.SiteURL = "/moovie"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Validate() error = %v, want absolute SITE_URL rejection", err)
	}
}

func validProductionConfig() Config {
	return Config{
		Env:       "production",
		Port:      "5008",
		SiteName:  "Moovie影牛",
		SiteURL:   "https://example.com",
		WebRoot:   "./web",
		AppSecret: strings.Repeat("a", minimumProductionSecretBytes),
		JobsInWeb: true,
		Database:  DatabaseConfig{Enabled: true, Name: "moovie_new"},
	}
}

func TestDatabaseDSNRoundTripsCredentials(t *testing.T) {
	database := DatabaseConfig{
		Host: "localhost", Port: "5432", User: "movie user", Password: "p@ss/word",
		Name: "moovie_new", SSLMode: "disable", TimeZone: "Asia/Shanghai",
	}
	parsed, err := url.Parse(database.DSN())
	if err != nil {
		t.Fatalf("url.Parse(DSN) error = %v", err)
	}
	password, ok := parsed.User.Password()
	if parsed.User.Username() != database.User || !ok || password != database.Password {
		t.Fatalf("credentials did not round trip: user=%q password=%q", parsed.User.Username(), password)
	}
}
