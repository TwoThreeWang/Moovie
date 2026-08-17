package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/admin"
	"github.com/TwoThreeWang/Moovie/new/internal/catalog"
	"github.com/TwoThreeWang/Moovie/new/internal/content"
	"github.com/TwoThreeWang/Moovie/new/internal/danmaku"
	"github.com/TwoThreeWang/Moovie/new/internal/douban"
	"github.com/TwoThreeWang/Moovie/new/internal/feedback"
	"github.com/TwoThreeWang/Moovie/new/internal/history"
	"github.com/TwoThreeWang/Moovie/new/internal/identity"
	"github.com/TwoThreeWang/Moovie/new/internal/library"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
	"github.com/TwoThreeWang/Moovie/new/internal/operations"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/auth"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/httpserver"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/outbound"
	platformweb "github.com/TwoThreeWang/Moovie/new/internal/platform/web"
	"github.com/TwoThreeWang/Moovie/new/internal/playback"
	"github.com/TwoThreeWang/Moovie/new/internal/recommendation"
	"github.com/TwoThreeWang/Moovie/new/internal/report"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
	"github.com/TwoThreeWang/Moovie/new/internal/social"
	"github.com/TwoThreeWang/Moovie/new/internal/workqueue"
	"github.com/gin-gonic/gin"
)

// contentPages 列出需要与共享 layout、partial 一起解析的页面模板。
// 显式维护清单可以让模板缺失或重名在启动阶段暴露，而不是等用户访问时才报错。
var contentPages = []string{"home", "search", "trends", "about", "advertise", "changelog", "dmca", "copyright_restricted", "privacy", "terms", "404", "player", "player_embed", "iptv", "tvbox", "play", "watch", "login", "register", "dashboard", "settings", "movie", "fetching", "recommendations", "foryou", "share", "share_monthly", "cinema", "feedback", "admin_feedback", "discover", "admin_dashboard", "admin_users", "admin_sites", "admin_cache", "admin_copyright", "admin_category", "admin_matches", "admin_jobs"}

type doubanResourceLister interface {
	ListResourcesByDoubanID(ctx context.Context, doubanID string) ([]search.LinkedResourceRow, error)
	HasPlayableResource(ctx context.Context, mediaID int) (bool, error)
}

// catalogResourceListerAdapter 只做模型转换，让 catalog 不必依赖 search 的具体结构体。
type catalogResourceListerAdapter struct{ lister doubanResourceLister }

func (a catalogResourceListerAdapter) ListResourcesByDoubanID(ctx context.Context, doubanID string) ([]catalog.LinkedResource, error) {
	rows, err := a.lister.ListResourcesByDoubanID(ctx, doubanID)
	if err != nil {
		return nil, err
	}
	result := make([]catalog.LinkedResource, len(rows))
	for i, r := range rows {
		result[i] = catalog.LinkedResource{
			SourceKey: r.SourceKey, VodID: r.VodID,
			VodName: r.VodName, VodPic: r.VodPic, VodYear: r.VodYear,
			VodArea: r.VodArea, TypeName: r.TypeName, VodActor: r.VodActor,
			VodRemarks: r.VodRemarks, VodDoubanID: r.VodDoubanID,
			AvgSpeedMs: r.AvgSpeedMs, SampleCount: r.SampleCount, FailedCount: r.FailedCount,
		}
	}
	return result, nil
}

func (a catalogResourceListerAdapter) HasPlayableResource(ctx context.Context, mediaID int) (bool, error) {
	return a.lister.HasPlayableResource(ctx, mediaID)
}

// discoverPopularAdapter 把播放域的热门结果转换成发现页需要的轻量结构。
type discoverPopularAdapter struct{ provider playback.PopularProvider }

func (adapter discoverPopularAdapter) Popular(ctx context.Context, mediaType string) ([]catalog.PopularSubject, error) {
	subjects, err := adapter.provider.Popular(ctx, mediaType)
	if err != nil {
		return nil, err
	}
	result := make([]catalog.PopularSubject, 0, len(subjects))
	for _, subject := range subjects {
		result = append(result, catalog.PopularSubject{ID: subject.ID, Title: subject.Title, Rate: subject.Rate, Cover: subject.Cover, URL: subject.URL, IsNew: subject.IsNew, EpisodesInfo: subject.EpisodesInfo})
	}
	return result, nil
}

func main() {
	// 启动阶段先完成配置和模板校验；任一步失败都直接退出，避免带着半套配置接收请求。
	if err := config.LoadDotEnv(".env"); err != nil {
		slog.Error("env file loading failed", "error", err)
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration failed", "error", err)
		os.Exit(1)
	}

	// Store 变量先按业务接口声明，再根据 DB_ENABLED 选择 PostgreSQL 或内存实现。
	// Handler 和 Service 只依赖这些接口，因此不需要知道底层使用哪种存储。
	renderer, err := platformweb.LoadRenderer(filepath.Join(cfg.WebRoot, "templates"), contentPages)
	if err != nil {
		slog.Error("template loading failed", "error", err)
		os.Exit(1)
	}
	var itemStore search.ItemStore
	var siteStore search.SiteStore
	var filterStore search.FilterStore
	var searchLogStore search.SearchLogStore
	var healthStatStore search.HealthStatStore
	var readiness httpserver.ReadinessProbe
	var historyStore history.Store
	var mediaIdentityStore mediaidentity.Resolver
	var canonicalStore mediaidentity.Store
	var mediaIdentitySearch search.MediaIdentity
	var identityStore identity.Store
	var doubanUserStore douban.UserStore
	var doubanJobStore douban.JobStore
	var queueStore workqueue.Store
	var reportStore report.Store
	var libraryStore library.Store
	var catalogStore catalog.Store
	var metadataRefreshJobs catalog.RefreshQueue
	var socialStore social.Store
	var feedbackStore feedback.Store
	var danmakuStore danmaku.Store
	var adminSearchStore admin.SearchStore
	var operationsStore operations.Store
	var databasePool *database.Pool
	if cfg.Database.Enabled {
		// 数据库连接和 migration 都设置独立超时，防止启动过程无限卡住。
		connectContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		databasePool, err = database.Connect(connectContext, cfg.Database.DSN(), cfg.Database.MaxConns)
		cancel()
		if err != nil {
			slog.Error("database connection failed", "error", err)
			os.Exit(1)
		}
		if cfg.Database.Migrate {
			migrationContext, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err = database.Migrate(migrationContext, databasePool)
			cancel()
			if err != nil {
				databasePool.Close()
				slog.Error("database migration failed", "error", err)
				os.Exit(1)
			}
		}
		postgresStore := search.NewPostgresStore(databasePool)
		itemStore, siteStore, filterStore = postgresStore, postgresStore, postgresStore
		adminSearchStore = postgresStore
		operationsStore = postgresStore
		searchLogStore, healthStatStore = postgresStore, postgresStore
		historyStore = history.NewPostgresStore(databasePool)
		mediaStore := mediaidentity.NewPostgresStore(databasePool)
		mediaIdentityStore = mediaStore
		canonicalStore = mediaStore
		mediaIdentitySearch = mediaidentity.SearchAdapter{Store: mediaStore}
		postgresIdentityStore := identity.NewPostgresStore(databasePool)
		identityStore, doubanUserStore = postgresIdentityStore, postgresIdentityStore
		libraryStore = library.NewPostgresStore(databasePool)
		postgresCatalogStore := catalog.NewPostgresStore(databasePool)
		catalogStore = postgresCatalogStore
		metadataRefreshJobs = postgresCatalogStore
		queueStore = workqueue.NewPostgresStore(databasePool)
		doubanJobStore = douban.NewQueueJobStore(queueStore)
		reportStore = report.NewPostgresStore(databasePool)
		socialStore = social.NewPostgresStore(databasePool)
		feedbackStore = feedback.NewPostgresStore(databasePool)
		danmakuStore = danmaku.NewPostgresStore(databasePool)
		readiness = databasePool.Ping
	} else {
		// 内存实现主要用于无数据库的隔离启动和测试，不具备跨进程持久化能力。
		memoryStore := search.NewMemoryStore()
		itemStore, siteStore, filterStore = memoryStore, memoryStore, memoryStore
		adminSearchStore = memoryStore
		operationsStore = memoryStore
		searchLogStore, healthStatStore = memoryStore, memoryStore
		historyStore = history.NewMemoryStore()
		memoryIdentityStore := identity.NewMemoryStore()
		identityStore, doubanUserStore = memoryIdentityStore, memoryIdentityStore
		libraryStore = library.NewMemoryStore()
		catalogStore = catalog.NewMemoryStore()
		queueStore = workqueue.NewMemoryStore()
		doubanJobStore = douban.NewMemoryJobStore(queueStore)
		reportStore = report.NewMemoryStore()
		socialStore = social.NewMemoryStore(libraryStore, identityStore)
		feedbackStore = feedback.NewMemoryStore()
		danmakuStore = danmaku.NewMemoryStore()
	}
	// 搜索 Runner、健康状态和出站 Client 都是进程级共享对象；重复创建会绕过并发上限。
	searchRunner := search.NewGoroutineRunner(cfg.Search.TotalTimeout, cfg.Search.BackgroundMaxConcurrency)
	searchHealth := search.NewHealthWithStore(cfg.Search.BreakerEnabled, healthStatStore)
	searchHealth.Start()
	sourceClient := outbound.NewClient(cfg.Search.SourceTimeout, cfg.OutboundMaxConnsPerHost)
	sourceCrawler := search.NewAppleCMSCrawler(sourceClient)
	doubanOptions := []catalog.DoubanOption{}
	tmdbOptions := []catalog.TMDBOption{}
	if canonicalStore != nil {
		doubanOptions = append(doubanOptions, catalog.WithDoubanCanonicalWriter(canonicalStore))
		tmdbOptions = append(tmdbOptions, catalog.WithTMDBCanonicalWriter(canonicalStore))
		if unitWriter, ok := canonicalStore.(catalog.MediaUnitWriter); ok {
			tmdbOptions = append(tmdbOptions, catalog.WithTMDBMediaUnitWriter(unitWriter))
		}
	}
	doubanProvider := catalog.NewDoubanProvider(sourceClient, catalogStore, doubanOptions...)
	doubanClient := douban.NewClient(sourceClient)
	doubanService := douban.NewService(doubanClient, libraryStore, doubanJobStore)
	reportService := report.NewService(reportStore, libraryStore, catalogStore)
	doubanTaskHandler := douban.NewTaskHandler(doubanJobStore, doubanUserStore, doubanService, douban.WithMonthlyGenerator(reportService))
	metricsStore := operations.NewMetricsStore(nil)
	if databasePool != nil {
		metricsStore = operations.NewMetricsStore(databasePool)
	}
	operationsService := operations.NewService(operationsStore, feedbackStore,
		operations.WithJobQueueCleanup(metricsStore.DeleteExpiredJobs))
	tmdbProvider := catalog.NewTMDBProvider(sourceClient, catalogStore, cfg.Catalog.TMDBToken, tmdbOptions...)
	embeddingService := catalog.NewEmbeddingService(sourceClient, catalogStore, catalog.EmbeddingConfig{
		OllamaHost: cfg.Catalog.OllamaHost, OllamaModel: cfg.Catalog.OllamaModel,
		CFGatewayURL: cfg.Catalog.CFGatewayURL, CFAPIToken: cfg.Catalog.CFAPIToken,
		CFAIModel: cfg.Catalog.CFAIModel,
	})
	var metadataRefreshHandler *catalog.RefreshHandler
	if metadataRefreshJobs != nil {
		refreshOptions := []catalog.RefreshHandlerOption{catalog.WithRefreshReviews(doubanProvider)}
		if cfg.Catalog.TMDBToken != "" {
			refreshOptions = append(refreshOptions, catalog.WithRefreshBackdrops(tmdbProvider))
		}
		metadataRefreshHandler = catalog.NewRefreshHandler(metadataRefreshJobs, doubanProvider, embeddingService, refreshOptions...)
	}
	searchOptions := []search.ServiceOption{}
	if mediaIdentitySearch != nil {
		searchOptions = append(searchOptions, search.WithMediaIdentity(mediaIdentitySearch))
		if indexer, ok := mediaIdentitySearch.(search.ResourceEpisodeIndexer); ok {
			searchOptions = append(searchOptions, search.WithResourceEpisodeIndexer(indexer))
		}
	}
	searchService := search.NewService(
		itemStore,
		siteStore,
		filterStore,
		sourceCrawler,
		searchHealth,
		searchRunner,
		search.ServiceConfig{SourceTimeout: cfg.Search.SourceTimeout, TotalTimeout: cfg.Search.TotalTimeout,
			SourceMaxConcurrency: cfg.Search.SourceMaxConcurrency,
			ResourceMatchShadow:  cfg.Search.ResourceMatchShadow, ResourceMatchAutoApply: cfg.Search.ResourceMatchAutoApply,
			MediaAutoMatchThreshold: cfg.Search.MediaAutoMatchThreshold, MediaReviewMatchThreshold: cfg.Search.MediaReviewMatchThreshold}, searchOptions...,
	)
	unifiedOptions := []search.UnifiedSearchOption{}
	if unifiedCatalog, ok := itemStore.(search.UnifiedCatalog); ok {
		unifiedOptions = append(unifiedOptions, search.WithUnifiedCatalog(unifiedCatalog))
	}
	unifiedSearchService := search.NewUnifiedSearchService(searchService, unifiedOptions...)
	searchHandler := search.NewHandler(
		cfg,
		searchService,
		search.WithSearchLogger(searchLogStore, searchRunner),
		search.WithUnifiedSearcher(unifiedSearchService),
	)
	detailService := playback.NewDetailService(itemStore.(playback.Catalog), siteStore.(playback.SiteCatalog), sourceCrawler, searchRunner, cfg.Search.SourceTimeout)
	doubanPopular := playback.NewDoubanPopularProvider(sourceClient)
	popularSources := []playback.PopularSource{{Name: "douban", Weight: 0.30, Provider: doubanPopular}}
	if databasePool != nil {
		popularSources = append(popularSources, playback.PopularSource{Name: "activity", Weight: 0.40, Provider: playback.NewActivityPopularProvider(databasePool)})
	}
	if resolver, ok := mediaIdentityStore.(playback.PopularIdentityResolver); ok && cfg.Catalog.TMDBToken != "" {
		tmdbPopular := playback.NewTMDBPopularProvider(sourceClient, cfg.Catalog.TMDBToken, resolver)
		popularSources = append(popularSources, playback.PopularSource{Name: "tmdb", Weight: 0.30, Provider: tmdbPopular})
	}
	popularProvider := playback.PopularProvider(doubanPopular)
	var popularityRefresher *playback.PopularityRefresher
	if len(popularSources) > 1 {
		popularProvider = playback.NewCompositePopularProvider(popularSources...)
	}
	if databasePool != nil {
		snapshotStore := playback.NewPopularitySnapshotStore(databasePool)
		popularityRefresher = playback.NewPopularityRefresher(snapshotStore, playback.NewCompositePopularProvider(popularSources...), cfg.Popularity.RefreshInterval)
		// 快照不可用时 SnapshotPopularProvider 内部回退到豆瓣榜单。
		popularProvider = playback.NewSnapshotPopularProvider(snapshotStore, doubanPopular)
	}
	var workerDispatcher *workqueue.Dispatcher
	if cfg.JobsInWeb {
		// 单进程开发同样使用唯一 Dispatcher；生产环境关闭 JOBS_IN_WEB，交给 cmd/worker。
		workerDispatcher = workqueue.NewDispatcher(queueStore, cfg.Worker.Concurrency, cfg.Worker.Poll)
		workerDispatcher.Handle(douban.TaskSync, 30*time.Minute, doubanTaskHandler.Handle)
		workerDispatcher.Handle(douban.TaskDaily, 30*time.Minute, doubanTaskHandler.HandleDaily)
		workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: douban.TaskDaily, SubjectKey: "global", Reason: "scheduled"}, Interval: 24 * time.Hour, InitialDelay: time.Minute})
		workerDispatcher.Handle(operations.TaskCleanup, 30*time.Minute, operationsService.HandleCleanup)
		workerDispatcher.Handle(operations.TaskHealthCheck, 5*time.Minute, operationsService.HandleHealthCheck)
		workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: operations.TaskCleanup, SubjectKey: "global", Reason: "scheduled"}, Interval: 24 * time.Hour})
		workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: operations.TaskHealthCheck, SubjectKey: "global", Reason: "scheduled"}, Interval: time.Hour, InitialDelay: time.Hour})
		if metadataRefreshHandler != nil {
			for _, taskType := range []string{catalog.RefreshProviderDouban, catalog.RefreshProviderReviews, catalog.RefreshProviderTMDB, catalog.RefreshProviderEmbedding} {
				workerDispatcher.Handle(taskType, 10*time.Minute, metadataRefreshHandler.Handle)
			}
			workerDispatcher.Handle("metadata_schedule", 2*time.Minute, metadataRefreshHandler.Schedule)
			workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: "metadata_schedule", SubjectKey: "global", Reason: "scheduled"}, Interval: time.Minute})
		}
		if popularityRefresher != nil {
			workerDispatcher.Handle(playback.TaskPopularityRefresh, 15*time.Minute, popularityRefresher.Handle)
			workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: playback.TaskPopularityRefresh, SubjectKey: "global", Reason: "scheduled"}, Interval: cfg.Popularity.RefreshInterval})
		}
		if err := workerDispatcher.Start(); err != nil {
			slog.Error("worker dispatcher failed to start", "error", err)
			os.Exit(1)
		}
	}
	playbackOptions := []playback.HandlerOption{
		playback.WithSpeedStore(itemStore.(playback.SpeedStore)),
		playback.WithCopyrightChecker(searchService),
		playback.WithUserMovieStore(libraryStore),
		playback.WithMediaResolver(mediaIdentityStore),
	}
	if episodeReader, ok := mediaIdentityStore.(mediaidentity.EpisodeReader); ok {
		playbackOptions = append(playbackOptions, playback.WithEpisodeReader(episodeReader))
	}
	if eventWriter, ok := mediaIdentityStore.(mediaidentity.PlaybackEventWriter); ok {
		playbackOptions = append(playbackOptions, playback.WithPlaybackEventWriter(eventWriter))
	}
	// 播出日期只有 PostgreSQL 实现能提供；内存启动时播放页不展示更新时间区块。
	if airReader, ok := mediaIdentityStore.(playback.AirScheduleReader); ok {
		playbackOptions = append(playbackOptions, playback.WithAirScheduleReader(airReader))
	}
	playbackHandler := playback.NewHandler(
		cfg,
		itemStore.(playback.Catalog),
		detailService,
		popularProvider,
		catalog.NewTitleFinder(catalogStore),
		playbackOptions...,
	)
	historyOptions := []history.HandlerOption{}
	// 播出日期只有 PostgreSQL 实现能提供；内存启动时首页不展示"今日更新"。
	if updateReader, ok := mediaIdentityStore.(history.TodayUpdateReader); ok {
		historyOptions = append(historyOptions, history.WithTodayUpdateReader(updateReader, cfg.Database.TimeZone))
	}
	historyHandler := history.NewHandler(historyStore, cfg.AppSecret, historyOptions...)
	libraryHandler := library.NewHandler(libraryStore, cfg.AppSecret)
	identityHandler := identity.NewHandler(cfg, identityStore, identity.WithHistoryCounter(historyStore), identity.WithLibraryCounter(libraryStore), identity.WithMonthlyReportReader(reportStore), identity.WithFeedbackCounter(feedbackStore))
	doubanHandler := douban.NewHandler(cfg, doubanUserStore, doubanJobStore, doubanService, doubanTaskHandler)
	reportHandler := report.NewHandler(cfg, doubanUserStore, libraryStore, reportStore, reportService)
	var personalizer recommendation.Personalizer = recommendation.NewMemoryPersonalizer(catalogStore, libraryStore, historyStore)
	if databasePersonalizer, ok := catalogStore.(recommendation.Personalizer); ok {
		personalizer = databasePersonalizer
	}
	recommendationService := recommendation.NewService(catalogStore, recommendation.WithPersonalizer(personalizer))
	catalogHandlerOptions := []catalog.HandlerOption{
		catalog.WithUserMovies(libraryStore),
		catalog.WithFetcher(doubanProvider, searchRunner),
		catalog.WithReviewFetcher(doubanProvider),
		catalog.WithVectorEnricher(embeddingService),
		catalog.WithBackgroundRunner(searchRunner),
		catalog.WithSuggester(doubanProvider),
		catalog.WithPopularProvider(discoverPopularAdapter{provider: popularProvider}),
		catalog.WithSimilarFinder(recommendationService),
	}
	if cfg.Catalog.TMDBToken != "" {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithBackdropSyncer(tmdbProvider))
	}
	if lister, ok := itemStore.(doubanResourceLister); ok {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithResourceLister(catalogResourceListerAdapter{lister: lister}))
	}
	if metadataRefreshJobs != nil {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithRefreshQueue(metadataRefreshJobs))
	}
	// 同上：无数据库启动时详情页安全降级为不展示更新时间区块。
	if airReader, ok := mediaIdentityStore.(catalog.AirScheduleReader); ok {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithAirScheduleReader(airReader))
	}
	catalogHandler := catalog.NewHandler(cfg, catalogStore, catalogHandlerOptions...)
	contentHandler := content.NewHandler(cfg, catalog.NewSitemapProvider(catalogStore))
	recommendationHandler := recommendation.NewHandler(cfg, recommendationService)
	socialHandler := social.NewHandler(cfg, socialStore)
	feedbackHandler := feedback.NewHandler(cfg, feedbackStore)
	danmakuClient := outbound.NewClient(25*time.Second, cfg.OutboundMaxConnsPerHost)
	danmakuService := danmaku.NewService(danmakuStore, danmakuClient, cfg.Danmaku.APIBase)
	danmakuHandler := danmaku.NewHandler(cfg, danmakuService)
	adminHandler := admin.NewHandler(cfg, identityStore, adminSearchStore, catalogStore, feedbackStore, sourceCrawler, searchHealth,
		admin.WithMetricsReader(metricsStore))
	// 所有路由在一个位置集中注册；全局中间件由 httpserver.New 先于这些路由安装。
	server := httpserver.New(cfg, readiness, func(router *gin.Engine) {
		router.HTMLRender = renderer
		router.Use(auth.Optional(cfg.AppSecret), identity.LoadUser(identityStore, cfg.AppSecret, cfg.Env == "production"))
		contentHandler.Register(router, filepath.Join(cfg.WebRoot, "static"))
		searchHandler.Register(router)
		playbackHandler.Register(router)
		historyHandler.Register(router)
		libraryHandler.Register(router)
		identityHandler.Register(router)
		doubanHandler.Register(router)
		reportHandler.Register(router)
		catalogHandler.Register(router)
		recommendationHandler.Register(router)
		socialHandler.Register(router)
		feedbackHandler.Register(router)
		danmakuHandler.Register(router)
		adminHandler.Register(router)
	})
	// Server 放在 goroutine 中运行，让主 goroutine 可以同时等待退出信号或异常停止。
	errCh := make(chan error, 1)
	go func() {
		slog.Info("web server starting", "address", server.Addr, "environment", cfg.Env,
			"http_max_in_flight", cfg.HTTP.MaxInFlight,
			"http_max_heavy_in_flight", cfg.HTTP.MaxHeavyInFlight,
			"http_max_image_in_flight", cfg.HTTP.MaxImageInFlight,
			"http_max_connections", cfg.HTTP.MaxConnections,
			"search_source_concurrency", cfg.Search.SourceMaxConcurrency,
			"search_background_concurrency", cfg.Search.BackgroundMaxConcurrency,
			"database_max_connections", cfg.Database.MaxConns,
			"jobs_in_web", cfg.JobsInWeb,
		)
		errCh <- httpserver.ListenAndServe(server, cfg.HTTP.MaxConnections)
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("web server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	case <-signalCtx.Done():
	}

	// 收到 SIGINT/SIGTERM 后按同一超时窗口停止 HTTP、后台 Runner 和可选 Dispatcher，
	// 最后关闭数据库与空闲连接，避免发布时中断正在写入的数据。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	if err := searchRunner.Stop(shutdownCtx); err != nil {
		slog.Error("search background shutdown failed", "error", err)
		os.Exit(1)
	}
	if err := searchHealth.Stop(shutdownCtx); err != nil {
		slog.Error("search health shutdown failed", "error", err)
		os.Exit(1)
	}
	if workerDispatcher != nil {
		if err := workerDispatcher.Stop(shutdownCtx); err != nil {
			slog.Error("worker dispatcher shutdown failed", "error", err)
			os.Exit(1)
		}
	}
	if databasePool != nil {
		databasePool.Close()
	}
	sourceClient.CloseIdleConnections()
	danmakuClient.CloseIdleConnections()
	slog.Info("web server stopped")
}
