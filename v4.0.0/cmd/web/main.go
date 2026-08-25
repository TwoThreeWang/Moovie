// web 是网站主进程：装配所有依赖、注册路由、启动 HTTP 服务。
//
// 全站只有这一个地方创建 Store 和 Service，其他包之间只通过接口打交道。
// 后台任务由单独的 worker 进程跑（见 cmd/worker）。
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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
var contentPages = []string{"home", "search", "trends", "about", "advertise", "changelog", "dmca", "copyright_restricted", "privacy", "terms", "404", "player", "player_embed", "iptv", "tvbox", "play", "watch", "login", "register", "dashboard", "settings", "movie", "fetching", "recommendations", "foryou", "share", "share_monthly", "cinema", "feedback", "admin_feedback", "discover", "admin_dashboard", "admin_users", "admin_sites", "admin_cache", "admin_copyright", "admin_category", "admin_nsfw", "admin_matches", "admin_jobs"}

// discoverPopularAdapter 把播放域的热门结果转换成发现页需要的轻量结构。
type discoverPopularAdapter struct{ provider playback.PopularProvider }

// Popular 转换热门榜结果。
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

// main 按「配置 → 数据库 → Store → Service → Handler → 路由 → 启动」的顺序装配整个网站，
// 收到停止信号后优雅关闭。
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

	// ── 阶段 1：模板 ──────────────────────────────────────────────
	// 启动时就把所有 HTML 模板编译好，缺模板会直接报错退出。
	renderer, err := platformweb.LoadRenderer(filepath.Join(cfg.WebRoot, "templates"), contentPages)
	if err != nil {
		slog.Error("template loading failed", "error", err)
		os.Exit(1)
	}
	// ── 阶段 2：数据库连接 + Store 创建 ──────────────────────────
	// 所有 Store 变量按业务接口声明，底层实现统一来自 PostgreSQL。
	// Handler 和 Service 只依赖这些接口，因此不需要知道底层表结构。
	var itemStore search.ItemStore                // 资源条目（采集到的影视资源）
	var siteStore search.SiteStore                // 资源站配置
	var filterStore search.FilterStore            // 搜索过滤条件
	var searchLogStore search.SearchLogStore      // 搜索日志
	var healthStatStore search.HealthStatStore    // 资源站健康统计（熔断用）
	var readiness httpserver.ReadinessProbe       // 就绪探针（k8s 健康检查）
	var historyStore history.Store                // 用户播放历史
	var mediaIdentityStore mediaidentity.Resolver // 规范媒体身份（豆瓣 ID → 内部媒体 ID 的映射）
	var canonicalStore mediaidentity.Store        // 规范媒体写入
	var mediaIdentitySearch search.MediaIdentity  // 搜索时的媒体匹配
	var identityStore identity.Store              // 用户账号
	var doubanUserStore douban.UserStore          // 豆瓣账号绑定
	var doubanJobStore douban.JobStore            // 豆瓣同步任务
	var queueStore workqueue.Store                // 通用后台任务队列
	var reportStore report.Store                  // 月报
	var libraryStore library.Store                // 用户片库（想看/已看）
	var catalogStore catalog.Store                // 影片元数据（标题、海报、评分等）
	var metadataRefreshJobs catalog.RefreshQueue  // 元数据刷新任务队列
	var socialStore social.Store                  // 社交（分享）
	var feedbackStore feedback.Store              // 用户反馈
	var danmakuStore danmaku.Store                // 弹幕
	var adminSearchStore admin.SearchStore        // 后台管理的搜索
	var operationsStore operations.Store          // 运维（清理、健康检查）
	var databasePool *database.Pool
	connectContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	databasePool, err = database.Connect(connectContext, cfg.Database.DSN(), cfg.Database.MaxConns)
	cancel()
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	// 在启动 Web 服务前,检查配置是否启用了自动迁移,如果启用则执行数据库表结构更新。
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
	// 用同一个数据库连接池创建所有 Store。
	// 一个 Postgres Store 可能同时实现多个业务接口（如 postgresStore 同时是 ItemStore、SiteStore、FilterStore）。
	postgresStore := search.NewPostgresStore(databasePool)
	itemStore, siteStore, filterStore = postgresStore, postgresStore, postgresStore
	adminSearchStore = postgresStore
	operationsStore = postgresStore
	searchLogStore, healthStatStore = postgresStore, postgresStore
	postgresHistory := history.NewPostgresStore(databasePool)
	historyStore = postgresHistory
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
	// ── 阶段 3：进程级共享组件（HTTP Client、搜索并发控制、熔断器）───
	// 这些是全局单例，所有 Service 共用；重复创建会绕过并发上限。
	searchRunner := search.NewGoroutineRunner(cfg.Search.TotalTimeout, cfg.Search.BackgroundMaxConcurrency) // 控制后台搜索并发数
	searchHealth := search.NewHealthWithStore(cfg.Search.BreakerEnabled, healthStatStore)                   // 资源站熔断器
	searchHealth.Start()
	sourceClient := outbound.NewClient(cfg.Search.SourceTimeout, cfg.OutboundMaxConnsPerHost) // 访问外部资源站的 HTTP Client
	aiClient := outbound.NewClient(cfg.Catalog.AITimeout, 4)                                  // AI（向量化）专用 Client，超时比资源站长
	sourceCrawler := search.NewAppleCMSCrawler(sourceClient)                                  // 苹果 CMS 采集器
	// ── 阶段 4：Service 层（业务逻辑）──────────────────────────────
	// 数据提供者：豆瓣（抓取影片元数据、短评）和 TMDB（剧照、英文信息）。
	// 抓取到的元数据会通过 canonicalStore 写入 media_identity 表建立规范映射。
	doubanOptions := []catalog.DoubanOption{catalog.WithDoubanRequestInterval(cfg.Catalog.DoubanRequestInterval)}
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
	doubanService := douban.NewService(doubanClient, libraryStore, doubanJobStore) // 豆瓣标记同步（想看/已看导入）
	reportService := report.NewService(reportStore, libraryStore, catalogStore)    // 月度观影报告
	doubanTaskHandler := douban.NewTaskHandler(doubanJobStore, doubanUserStore, doubanService, douban.WithMonthlyGenerator(reportService))
	metricsStore := operations.NewMetricsStore(nil)
	if databasePool != nil {
		metricsStore = operations.NewMetricsStore(databasePool)
	}
	operationsService := operations.NewService(operationsStore, // 运维服务：定期清理过期任务、遥测
		operations.WithJobQueueCleanup(metricsStore.DeleteExpiredJobs),
		operations.WithTelemetryCleanup(metricsStore.DeleteExpiredTelemetry))
	tmdbProvider := catalog.NewTMDBProvider(sourceClient, catalogStore, cfg.Catalog.TMDBToken, tmdbOptions...)
	embeddingService := catalog.NewEmbeddingService(sourceClient, catalogStore, catalog.EmbeddingConfig{ // 向量化服务（相似推荐用）
		OllamaHost: cfg.Catalog.OllamaHost, OllamaModel: cfg.Catalog.OllamaModel,
		CFGatewayURL: cfg.Catalog.CFGatewayURL, CFAPIToken: cfg.Catalog.CFAPIToken,
		CFAIModel: cfg.Catalog.CFAIModel,
	}, catalog.WithEmbeddingAIClient(aiClient))
	// 主资料刷新：豆瓣抓取 → 可选 TMDB 合并 → 向量化，串行由后台任务驱动。
	var metadataRefreshHandler *catalog.RefreshHandler
	if metadataRefreshJobs != nil {
		refreshOptions := []catalog.RefreshHandlerOption{catalog.WithRefreshReviews(doubanProvider)}
		if cfg.Catalog.TMDBToken != "" {
			refreshOptions = append(refreshOptions, catalog.WithRefreshBackdrops(tmdbProvider))
		}
		metadataRefreshHandler = catalog.NewRefreshHandler(metadataRefreshJobs, doubanProvider, embeddingService, refreshOptions...)
	}
	// 搜索服务：聚合多个资源站的结果，支持媒体身份匹配（把不同站的同一部片子归并到一起）。
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
			RefreshWait:          cfg.Search.RefreshWait,
			SourceMaxConcurrency: cfg.Search.SourceMaxConcurrency,
			ResourceMatchShadow:  cfg.Search.ResourceMatchShadow, ResourceMatchAutoApply: cfg.Search.ResourceMatchAutoApply,
			MediaAutoMatchThreshold: cfg.Search.MediaAutoMatchThreshold, MediaReviewMatchThreshold: cfg.Search.MediaReviewMatchThreshold}, searchOptions...,
	)
	unifiedOptions := []search.UnifiedSearchOption{}
	if unifiedCatalog, ok := itemStore.(search.UnifiedCatalog); ok {
		unifiedOptions = append(unifiedOptions, search.WithUnifiedCatalog(unifiedCatalog))
	}
	unifiedOptions = append(unifiedOptions, search.WithUnifiedSuggestions(func(ctx context.Context, keyword string, limit int) ([]search.UnifiedItem, error) {
		suggestions, err := doubanProvider.SuggestExternal(ctx, keyword)
		if err != nil {
			return nil, err
		}
		items := make([]search.UnifiedItem, 0, min(limit, len(suggestions)))
		for _, suggestion := range suggestions {
			if len(items) == limit {
				break
			}
			if len(suggestion.ID) < 6 || len(suggestion.ID) > 9 || suggestion.Title == "" {
				continue
			}
			if _, parseErr := strconv.Atoi(suggestion.ID); parseErr != nil {
				continue
			}
			items = append(items, search.UnifiedItem{DoubanID: suggestion.ID, Title: suggestion.Title,
				OriginalTitle: suggestion.SubTitle, Year: suggestion.Year, MediaType: suggestion.Type, Poster: suggestion.Img,
				Resources: []search.UnifiedResource{}})
			if metadataRefreshJobs != nil {
				if _, queueErr := metadataRefreshJobs.EnqueueRefresh(ctx, suggestion.ID, catalog.RefreshProviderDouban, catalog.RefreshReasonSearchDiscovery, 0); queueErr != nil {
					slog.Warn("queue discovered metadata", "douban_id", suggestion.ID, "error", queueErr)
				}
			}
		}
		return items, nil
	}))
	unifiedSearchService := search.NewUnifiedSearchService(searchService, unifiedOptions...)
	searchHandler := search.NewHandler(
		cfg,
		searchService,
		search.WithSearchLogger(searchLogStore, searchRunner),
		search.WithUnifiedSearcher(unifiedSearchService),
	)
	// 播放详情服务：用户点"播放"时实时去资源站拉最新的播放地址。
	detailService := playback.NewDetailService(itemStore.(playback.Catalog), siteStore.(playback.SiteCatalog), sourceCrawler, searchRunner, cfg.Search.SourceTimeout)
	doubanPopular := playback.NewDoubanPopularProvider(sourceClient)
	popularSources := []playback.PopularSource{
		{Name: "douban", Provider: doubanPopular},
	}
	if resolver, ok := mediaIdentityStore.(playback.PopularIdentityResolver); ok && cfg.Catalog.TMDBToken != "" {
		tmdbPopular := playback.NewTMDBPopularProvider(sourceClient, cfg.Catalog.TMDBToken, resolver)
		popularSources = append(popularSources, playback.PopularSource{Name: "tmdb", Provider: tmdbPopular})
	}
	snapshotStore := playback.NewPopularitySnapshotStore(databasePool)
	siteTrendingSource := playback.NewSiteTrendingProvider(databasePool)
	popularityRefresher := playback.NewPopularityRefresher(snapshotStore,
		playback.NewCompositePopularProvider(popularSources...), siteTrendingSource, 24*time.Hour)
	popularProvider := playback.PopularProvider(snapshotStore)
	recommendationService := recommendation.NewService(catalogStore, recommendation.WithPersonalizer(postgresCatalogStore))
	recommendationSnapshots := recommendation.NewSnapshotStore(databasePool)
	recommendationRefresher := recommendation.NewRefresher(recommendationSnapshots, recommendationService)
	// ── 阶段 5（可选）：内嵌后台任务 ───────────────────────────────
	// 生产环境后台任务由独立的 cmd/worker 进程跑，但本地开发时开启 JOBS_IN_WEB
	// 可以把 worker 也跑在 web 进程里，省得同时启两个进程。
	var workerDispatcher *workqueue.Dispatcher
	if cfg.JobsInWeb {
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
			// IMDb 映射回填只在接了 Postgres 目录存储时可用；内存存储没有映射表可补。
			if mappingStore, ok := catalogStore.(catalog.IMDbMappingStore); ok {
				wikidataClient := outbound.NewClient(cfg.Catalog.WikidataTimeout, 2)
				imdbBackfill := catalog.NewIMDbBackfillHandler(mappingStore, metadataRefreshJobs,
					catalog.NewWikidataResolver(wikidataClient, cfg.Catalog.WikidataEndpoint, cfg.Catalog.WikidataUserAgent),
					catalog.WithIMDbFallback(catalog.NewWMDBResolver(sourceClient, "", cfg.Catalog.IMDbLookupInterval)),
					catalog.WithIMDbBatchSize(cfg.Catalog.IMDbBackfillBatch))
				workerDispatcher.Handle(catalog.TaskIMDbBackfill, 5*time.Minute, imdbBackfill.Handle)
				workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: catalog.TaskIMDbBackfill, SubjectKey: "global", Reason: "scheduled"}, Interval: time.Minute, InitialDelay: 30 * time.Second})
			}
			workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: "metadata_schedule", SubjectKey: "global", Reason: "scheduled"}, Interval: time.Minute})
		}
		workerDispatcher.Handle(playback.TaskPopularityRefresh, 15*time.Minute, popularityRefresher.Handle)
		workerDispatcher.Handle(playback.TaskSiteTrendingRefresh, 2*time.Minute, popularityRefresher.HandleSiteTrending)
		workerDispatcher.Handle(recommendation.TaskRefresh, 5*time.Minute, recommendationRefresher.Handle)
		workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: playback.TaskPopularityRefresh, SubjectKey: "global", Reason: "scheduled", Priority: 10}, Interval: 24 * time.Hour})
		workerDispatcher.Schedule(workqueue.Schedule{Spec: workqueue.Spec{TaskType: playback.TaskSiteTrendingRefresh, SubjectKey: "global", Reason: "scheduled", Priority: 10}, Interval: 24 * time.Hour})
		if err := workerDispatcher.Start(); err != nil {
			slog.Error("worker dispatcher failed to start", "error", err)
			os.Exit(1)
		}
	}
	// ── 阶段 6：Handler 层（HTTP 处理器）──────────────────────────
	// 每个业务模块一个 Handler，通过 WithXxx 选项注入可选依赖。
	// if xxx, ok := store.(SomeInterface) 这种写法是在检查 Store 是否实现了某个可选能力，
	// 实现了就注入，没实现就安全降级（页面上少一个区块而已）。
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
	if airReader, ok := mediaIdentityStore.(playback.AirScheduleReader); ok {
		playbackOptions = append(playbackOptions, playback.WithAirScheduleReader(airReader))
	}
	playbackOptions = append(playbackOptions, playback.WithAdFingerprintStore(playback.NewAdFingerprintPostgresStore(databasePool)))
	playbackHandler := playback.NewHandler(
		cfg,
		itemStore.(playback.Catalog),
		detailService,
		popularProvider,
		catalog.NewTitleFinder(catalogStore),
		playbackOptions...,
	)
	historyOptions := []history.HandlerOption{}
	if updateReader, ok := mediaIdentityStore.(history.TodayUpdateReader); ok {
		historyOptions = append(historyOptions, history.WithTodayUpdateReader(updateReader, cfg.Database.TimeZone))
	}
	if episodeReader, ok := mediaIdentityStore.(mediaidentity.EpisodeReader); ok {
		historyOptions = append(historyOptions, history.WithEpisodeReader(episodeReader))
	}
	if nsfwReader, ok := adminSearchStore.(history.NSFWKeywordReader); ok {
		historyOptions = append(historyOptions, history.WithNSFWKeywordReader(nsfwReader))
	}
	historyHandler := history.NewHandler(historyStore, cfg.AppSecret, historyOptions...)
	libraryHandler := library.NewHandler(libraryStore, cfg.AppSecret)
	identityHandler := identity.NewHandler(cfg, identityStore, identity.WithHistoryCounter(historyStore), identity.WithLibraryCounter(libraryStore), identity.WithMonthlyReportReader(reportStore), identity.WithFeedbackCounter(feedbackStore))
	doubanHandler := douban.NewHandler(cfg, doubanUserStore, doubanJobStore, doubanService, doubanTaskHandler)
	reportHandler := report.NewHandler(cfg, doubanUserStore, libraryStore, reportStore, reportService)
	catalogHandlerOptions := []catalog.HandlerOption{
		catalog.WithUserMovies(libraryStore),
		catalog.WithFetcher(doubanProvider, searchRunner),
		catalog.WithReviewFetcher(doubanProvider),
		catalog.WithBackgroundRunner(searchRunner),
		catalog.WithSuggester(doubanProvider),
		catalog.WithPopularProvider(discoverPopularAdapter{provider: popularProvider}),
		catalog.WithSiteTrending(discoverPopularAdapter{provider: popularProvider}),
		catalog.WithSimilarFinder(recommendationService),
	}
	if cfg.Catalog.TMDBToken != "" {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithBackdropSyncer(tmdbProvider))
	}
	if lister, ok := itemStore.(catalog.ResourceLister); ok {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithResourceLister(lister))
	}
	if metadataRefreshJobs != nil {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithRefreshQueue(metadataRefreshJobs))
	}
	if airReader, ok := mediaIdentityStore.(catalog.AirScheduleReader); ok {
		catalogHandlerOptions = append(catalogHandlerOptions, catalog.WithAirScheduleReader(airReader))
	}
	catalogHandler := catalog.NewHandler(cfg, catalogStore, catalogHandlerOptions...)
	contentHandler := content.NewHandler(cfg, catalog.NewSitemapProvider(catalogStore))
	recommendationHandler := recommendation.NewHandler(cfg, recommendationService, recommendationSnapshots).WithRefreshQueue(queueStore)
	socialHandler := social.NewHandler(cfg, socialStore)
	feedbackHandler := feedback.NewHandler(cfg, feedbackStore)
	danmakuClient := outbound.NewClient(25*time.Second, cfg.OutboundMaxConnsPerHost)
	danmakuService := danmaku.NewService(danmakuStore, danmakuClient, cfg.Danmaku.APIBase)
	danmakuHandler := danmaku.NewHandler(cfg, danmakuService)
	adminOptions := []admin.HandlerOption{admin.WithMetricsReader(metricsStore)}
	// 队列未接入时 queueStore 可能为 nil，此时后台不提供重试入口。
	if retrier, ok := queueStore.(admin.JobRetrier); ok {
		adminOptions = append(adminOptions, admin.WithJobRetrier(retrier))
	}
	adminHandler := admin.NewHandler(cfg, identityStore, adminSearchStore, catalogStore, feedbackStore, sourceCrawler, searchHealth,
		adminOptions...)
	// ── 阶段 7：路由注册 + HTTP 服务启动 ─────────────────────────
	// 所有路由在一个位置集中注册；全局中间件（限流、CORS 等）由 httpserver.New 先于这些路由安装。
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

	// ── 阶段 8：等待退出信号 ─────────────────────────────────────
	// 主 goroutine 阻塞在这里，等 Ctrl+C 或 kill 信号。
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

	// ── 阶段 9：优雅关闭 ─────────────────────────────────────────
	// 收到停止信号后按顺序关闭：HTTP 服务 → 后台搜索 → 熔断器 → Worker → 数据库 → HTTP Client。
	// 所有关闭共用同一个超时窗口，防止某一步卡住导致进程挂起。
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
