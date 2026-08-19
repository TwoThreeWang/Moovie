package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/douban"
	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/operations"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	"github.com/TwoThreeWang/Moovie/new/internal/playback"
	"github.com/TwoThreeWang/Moovie/new/internal/report"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
)

func main() {
	// Worker 与 Web 使用同一套配置解析和校验，避免两个进程对变量含义理解不一致。
	if err := config.LoadDotEnv(".env"); err != nil {
		slog.Error("env file loading failed", "error", err)
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}

	connectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	pool, err := database.Connect(connectCtx, cfg.Database.DSN(), cfg.Database.MaxConns)
	cancel()
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if cfg.Database.Migrate {
		// 本地可由 Worker 应用 migration；生产通常在受控发布步骤完成后关闭此开关。
		migrationCtx, migrationCancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = database.Migrate(migrationCtx, pool)
		migrationCancel()
		if err != nil {
			slog.Error("database migration failed", "error", err)
			os.Exit(1)
		}
	}

	// 所有 Store 共享同一个有界 pgx 连接池，连接上限由 DB_MAX_CONNS 控制。
	users := identity.NewPostgresStore(pool)
	libraryStore := library.NewPostgresStore(pool)
	queueStore := workqueue.NewPostgresStore(pool)
	jobs := douban.NewQueueJobStore(queueStore)
	reports := report.NewPostgresStore(pool)
	movies := catalog.NewPostgresStore(pool)
	mediaStore := mediaidentity.NewPostgresStore(pool)
	searchStore := search.NewPostgresStore(pool)
	// 外部 Provider 复用同一个有界 Client，防止任务高峰为每个来源无限创建连接。
	client := outbound.NewClient(cfg.Search.SourceTimeout, cfg.OutboundMaxConnsPerHost)
	defer client.CloseIdleConnections()
	// AI Gateway 单独一个 Client：LLM 的响应时间和抓取源不在一个量级，共用超时会让语义改写全部超时。
	aiClient := outbound.NewClient(cfg.Catalog.AITimeout, 4)
	defer aiClient.CloseIdleConnections()
	metadataProvider := catalog.NewDoubanProvider(client, movies, catalog.WithDoubanCanonicalWriter(mediaStore), catalog.WithDoubanRequestInterval(cfg.Catalog.DoubanRequestInterval))
	tmdbProvider := catalog.NewTMDBProvider(client, movies, cfg.Catalog.TMDBToken,
		catalog.WithTMDBCanonicalWriter(mediaStore), catalog.WithTMDBMediaUnitWriter(mediaStore))
	// SPARQL 批量查询比普通抓取慢得多，单独一个 Client，超时按 Wikidata 的查询上限配。
	wikidataClient := outbound.NewClient(cfg.Catalog.WikidataTimeout, 2)
	defer wikidataClient.CloseIdleConnections()
	imdbBackfill := catalog.NewIMDbBackfillHandler(movies, movies,
		catalog.NewWikidataResolver(wikidataClient, cfg.Catalog.WikidataEndpoint, cfg.Catalog.WikidataUserAgent),
		catalog.WithIMDbFallback(catalog.NewWMDBResolver(client, "", cfg.Catalog.IMDbLookupInterval)),
		catalog.WithIMDbBatchSize(cfg.Catalog.IMDbBackfillBatch))
	embeddingService := catalog.NewEmbeddingService(client, movies, catalog.EmbeddingConfig{
		OllamaHost: cfg.Catalog.OllamaHost, OllamaModel: cfg.Catalog.OllamaModel,
		CFGatewayURL: cfg.Catalog.CFGatewayURL, CFAPIToken: cfg.Catalog.CFAPIToken,
		CFAIModel: cfg.Catalog.CFAIModel,
	}, catalog.WithEmbeddingAIClient(aiClient))
	refreshOptions := []catalog.RefreshHandlerOption{catalog.WithRefreshReviews(metadataProvider)}
	if cfg.Catalog.TMDBToken != "" {
		refreshOptions = append(refreshOptions, catalog.WithRefreshBackdrops(tmdbProvider))
	}
	metadataHandler := catalog.NewRefreshHandler(movies, metadataProvider, embeddingService, refreshOptions...)
	doubanPopular := playback.NewDoubanPopularProvider(client)
	popularSources := []playback.PopularSource{
		{Name: "douban", Weight: 0.30, Provider: doubanPopular},
		{Name: "activity", Weight: 0.40, Provider: playback.NewActivityPopularProvider(pool)},
	}
	if cfg.Catalog.TMDBToken != "" {
		popularSources = append(popularSources, playback.PopularSource{Name: "tmdb", Weight: 0.30,
			Provider: playback.NewTMDBPopularProvider(client, cfg.Catalog.TMDBToken, mediaStore)})
	}
	popularityRefresher := playback.NewPopularityRefresher(playback.NewPopularitySnapshotStore(pool),
		playback.NewCompositePopularProvider(popularSources...), cfg.Popularity.RefreshInterval)
	syncService := douban.NewService(douban.NewClient(client), libraryStore, jobs)
	reportService := report.NewService(reports, libraryStore, movies)
	doubanHandler := douban.NewTaskHandler(jobs, users, syncService, douban.WithMonthlyGenerator(reportService))
	operationsService := operations.NewService(searchStore,
		operations.WithJobQueueCleanup(operations.NewMetricsStore(pool).DeleteExpiredJobs))
	dispatcher := workqueue.NewDispatcher(queueStore, cfg.Worker.Concurrency, cfg.Worker.Poll)
	for _, taskType := range []string{catalog.RefreshProviderDouban, catalog.RefreshProviderReviews, catalog.RefreshProviderTMDB, catalog.RefreshProviderEmbedding} {
		dispatcher.Handle(taskType, 10*time.Minute, metadataHandler.Handle)
	}
	dispatcher.Handle("metadata_schedule", 2*time.Minute, metadataHandler.Schedule)
	dispatcher.Handle(catalog.TaskIMDbBackfill, 5*time.Minute, imdbBackfill.Handle)
	dispatcher.Handle(douban.TaskSync, 30*time.Minute, doubanHandler.Handle)
	dispatcher.Handle(douban.TaskDaily, 30*time.Minute, doubanHandler.HandleDaily)
	dispatcher.Handle(playback.TaskPopularityRefresh, 15*time.Minute, popularityRefresher.Handle)
	dispatcher.Handle(operations.TaskCleanup, 30*time.Minute, operationsService.HandleCleanup)
	dispatcher.Handle(operations.TaskHealthCheck, 5*time.Minute, operationsService.HandleHealthCheck)
	dispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: "metadata_schedule", SubjectKey: "global", Reason: "scheduled"}, Interval: time.Minute})
	dispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: catalog.TaskIMDbBackfill, SubjectKey: "global", Reason: "scheduled"}, Interval: time.Minute, InitialDelay: 30 * time.Second})
	dispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: douban.TaskDaily, SubjectKey: "global", Reason: "scheduled"}, Interval: 24 * time.Hour, InitialDelay: time.Minute})
	dispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: playback.TaskPopularityRefresh, SubjectKey: "global", Reason: "scheduled"}, Interval: cfg.Popularity.RefreshInterval})
	dispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: operations.TaskCleanup, SubjectKey: "global", Reason: "scheduled"}, Interval: 24 * time.Hour})
	dispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: operations.TaskHealthCheck, SubjectKey: "global", Reason: "scheduled"}, Interval: time.Hour, InitialDelay: time.Hour})
	if err := dispatcher.Start(); err != nil {
		slog.Error("worker dispatcher failed to start", "error", err)
		os.Exit(1)
	}

	// Worker 没有 HTTP Server，主 goroutine 只等待停止信号，然后停止唯一的统一 Dispatcher。
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	slog.Info("worker started", "environment", cfg.Env)
	<-signalCtx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()
	if err := dispatcher.Stop(shutdownCtx); err != nil {
		slog.Error("worker dispatcher shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("worker stopped")
}
