# Ponytail Audit — Moovie v4.0.0

> 2026-08-18 更新。第一步已执行完毕，本文已按**核实结果**改写：初版审计中若干结论是从引用计数推断的，核实后被推翻，下面标注了原因。

## 基线与现状

| | 审计前 | 第一步后 | 第二步后 |
|---|---|---|---|
| Go 非测试 | 28,885 行 | 25,654 行 | **25,608 行** |
| Go 测试 | 13,152 行 | 12,467 行 | **12,456 行** |
| Go 合计 | 42,037 行 | 38,121 行 | **38,064 行**（-3,973） |
| cmd 二进制 | 10 | **4**（web / worker / dbmigrate / burstcheck） | 4 |
| 配置开关 | — | — | **-1**（`DB_ENABLED`） |
| 存活数据库表 | 34 | 34 | 34（尚未动库） |
| interface 数 | 97 | 97 | **96**（仅 1 个真正无用） |

Git 变更累计：27 个文件删除、12 个修改，约 4,570 行删除。全部可 `git revert` 回滚。

**两轮下来最该记住的一条**：初版审计里体量最大的三条结论，有两条在核实后被推翻。引用计数和文件体量只能用来**提出怀疑**，不能用来**定罪**——每一刀落下前都要读代码、算依赖闭包、确认消费者。下面每一节都标注了哪些是核实过的、哪些还只是怀疑。

---

## 第一步：已完成 — 删除迁移与切流工具链

前提：v4.0.0 已上线，旧库 `moovie` → 新库 `moovie_v2` 的一次性数据迁移已在生产完成（由 Evan 确认；仓库里 `.migration-reports/` 那次失败记录是本地测试）。

判定依据不是引用计数，而是**从 `cmd/web` 和 `cmd/worker` 出发的完整传递依赖闭包**（含 `_test.go`）。Dockerfile 只构建这两个二进制，因此闭包外的一切都不可能影响线上功能。

### 删除

| 目标 | 行数 | 理由 |
|---|---|---|
| `internal/datamigrate/` + `cmd/datamigrate` + `cmd/migrate` + `scripts/migrate.sh` | 2,943 | 一次性数据迁移，生产已完成。`cmd/migrate` 与 `cmd/datamigrate` 依赖闭包完全相同，是重复二进制 |
| `internal/releaseaudit/` + `cmd/releaseaudit` | 417 | 只做一件事：检查已退役表是否还在。表已删，检查恒真 |
| `internal/releasecontract/` | 113 | 纯测试包，断言 `preflight.sh` 与发布清单里包含特定字符串。被断言的脚本一并删除，它随之失去对象 |
| `cmd/compatcheck` `cmd/sitemapcheck` `cmd/loadcheck` + `compat/seo_cases.json` | 302 | 全部是「旧站 vs 新站」对比。旧站已下线，这些命令连运行前提都不存在 |
| `internal/compat/seo.go` 的 `Compare` `LoadManifest` `FilterExpectedDifferences` `Manifest` `summarize` + `sitemap.go` 全部 | ~560 | 随上面三个命令作废 |
| `scripts/release/preflight.sh` | 113 | 7 个阶段里 4 个调用已删命令 |
| `.migration-reports/` | — | 已删工具的输出 |

### 保留（初版审计判断错误，核实后撤回）

- **`internal/compat/seo.go` 的 `Fetch` + `extractHTML`** —— 初版把整个 `compat` 判成死重。实际上 `internal/content/handler_test.go` 和 `internal/search/handler_test.go` 用 `compat.Fetch` 做 SEO 回归测试，逐项断言 Title、H1、Description、Keywords、Robots、Canonical、OG/Twitter 标签和 JSON-LD。这是真正有价值的防护网，删掉等于放弃 SEO 回归。已按「只留 Fetch，砍掉对比逻辑」处理，`internal/compat` 从 1,100+ 行降到 506 行。
- **`internal/mediatitle`(21 行)、`internal/playurl`(52 行)、`internal/doubanpopular`(136 行)、`internal/content`(211 行)** —— 初版按体量判为「小包应合并」。实际各有 2–3 个包引用，`content` 还被 `cmd/web/main.go` 直接引用。合并只会把代码搬家并制造重复，不合并。
- **`cmd/burstcheck`** —— 突发负载与降载检查，与切流无关，仍挂在高并发运行手册里。保留，它是 `internal/compat/load.go` 的唯一消费者。
- **`scripts/release/source-audit.sh`** —— 纯发布产物完整性检查，不依赖任何 Go 命令。保留。
- **`internal/contract`(340 行)** —— 无生产引用，但 `implementation_routes_test.go` 会扫描全部 handler，断言注册的路由与声明清单完全一致，`request_inputs.go` 固化请求字段名。它是**手工维护的第二份真相**（每加一条路由都要同步改），按 ponytail 标准算过度设计；但它保护的是 SEO 和用户书签，而且昨天还在改。**留给你决定**，见下方待定项。

### 同步更新的文档

`README.md`（目录树、状态说明、数据迁移整章、验收命令表、发布顺序）、`Makefile`（删除 5 个 target）、`docs/P9_RELEASE_ACCEPTANCE_CHECKLIST.md`（第 1、3 节的一次性切流门禁折叠为历史说明）。`docs/LOCAL_HIGH_CONCURRENCY_ACCEPTANCE_2026-08-10.md` 是带日期的验收证据，按原样保留。

---

## 验证（需要在你本地执行 —— 我这边没有 Go 环境）

```bash
cd v4.0.0
go build ./...          # 必须通过
go vet ./...
go test ./...           # 重点看 internal/content、internal/search、internal/compat 三个包
make check
```

我在沙箱里做了这些**替代**检查，全部通过，但它们不能替代编译器：

- 从 `cmd/web` + `cmd/worker` 重算传递依赖闭包（含测试文件），确认「引用了但已不存在的包」为空
- 全仓 grep 已删包的 import 路径、已删导出符号（`compat.Compare` 等）、已删包内符号（`summarize` 等），均无残留
- 全仓 grep `.sh` / `Makefile` / `Dockerfile` / `*.yml` / `*.md` 中指向已删命令和脚本的路径，仅剩我主动写入的「已移除」说明
- `internal/compat/seo.go` 的 10 个 import 逐个确认仍有使用点；编辑过的两个文件括号配平

**如果编译报错**，第一轮最可能出在 `internal/compat/seo.go`（手工裁剪了函数），第二轮最可能出在 `internal/platform/config/`（删了结构体字段，`config_test.go` 有 4 处断言同步改动）。把报错贴回来即可。

---

## 第二轮：已完成 — 删除内存回退这条生产路径

前提：Evan 确认不需要「不装数据库也能跑」。

`cmd/web/main.go` 原本在 `DB_ENABLED=false` 时用内存 Store 装配整个应用。这条路径本身就是半坏的——它没有给 `mediaIdentityStore`、`canonicalStore`、`mediaIdentitySearch`、`metadataRefreshJobs`、`readiness` 赋值，全是 nil，一访问详情页就会 panic。Worker 早就明确拒绝这种模式。

| 改动 | 说明 |
|---|---|
| 删除 `cmd/web/main.go` 的内存装配分支 | 16 行，同时消除上述 nil Store 隐患 |
| 删除整个 `DB_ENABLED` 开关 | `DatabaseConfig.Enabled` 字段、`config.go` / `dotenv.go` 的解析、两处 `Validate` 规则、`cmd/worker` 的守卫、`.env` × 3、README 五处。只剩一个合法值的配置就不该存在 |
| 删除 `mediaidentity.MediaUnitWriter` | 全仓唯一无人引用的 interface |
| 删除 `douban.NewPostgresJobStore` / `PostgresJobStore` 别名 | 零引用；连带清掉 `queue_store.go` 对 `platform/database` 的 import |

memory 实现本身保留，但身份从「第二个生产后端」降级为「纯测试替身」，README 的名词表已相应改写。

**本轮验证要点**：`Load()` 内部会调 `Validate()`，所以删字段比加校验规则更安全——加规则会让默认配置自相矛盾，`TestLoadUsesIsolatedDefaults` 直接挂。

删掉一条校验规则时，必须同时找出**断言这条规则的测试**。本轮共删除两条规则，对应两个测试：

- `DB_ENABLED=true is required in production` → `TestValidateRejectsProductionWithoutDatabase`
- `JOBS_IN_WEB=false requires DB_ENABLED=true` → `TestValidateRequiresDatabaseForSeparateWorkerMode`

第二个是漏网的，`go test` 报出来才补上。教训：删 `Validate()` 里的规则后，应当立刻把 `config_test.go` 里每个 `Validate()` 断言逐条对一遍，而不是只 grep 被删的标识符——测试是按**行为**断言的，函数名里根本不出现 `DB_ENABLED`。`JobsInWeb` 字段本身仍在使用（`cmd/web/main.go` 决定是否在 Web 内启动 Dispatcher），保留。

---

## 第三轮：删除全部内存 Store，测试改打真库

前提：Evan 确认本地和生产都有 PG，不需要保留内存实现。

删除 9 个 `memory.go`（2,236 行）+ `workqueue/queue.go` 内联的 MemoryStore + `search/retirement.go` 上挂的三个内存方法 + `douban.NewMemoryJobStore`。131 个测试调用点改为 `NewPostgresStore(testdb.Pool(t))`。

新增 `internal/platform/database/testdb`（约 140 行）：复用 `.env.local` 的连接信息，**库名强制加 `_test` 后缀**。这是必要的闸门——测试每个用例前 TRUNCATE 业务表，而 `.env.local` 指向 `moovie_v2`（开发库）、`.env` 指向生产库。

设计上踩到并修正的三个坑：

1. **每次 `Pool(t)` 都清表会互相打架**。不少测试要同时建 3–4 个模块的 Store，第二次调用会抹掉第一次写入的数据。改成按顶层测试名去重，一个测试只清一次。
2. **memory 有而 postgres 没有的「播种方法」**。`ReplaceSites` / `ReplaceCopyrightKeywords` / `ReplaceCategoryKeywords` 是内存实现专有的，12 处调用改用 `testdb.SeedSites` / `testdb.SeedFilters`（裸 SQL，不 import `search`，否则 `search` 的包内测试会构成循环）。
3. **`search.MemoryStore` 兼职了 `mediaidentity` 的活**。`RecordDetailedMatchCandidate` 在生产里由 `mediaidentity.PostgresStore` 实现，内存版却挂在 `search` 上。`admin` 的测试改为直接用 `mediaidentity`；`search/match_review_test.go` 里那个纯粹测内存实现的用例直接删除（同文件的 postgres 用例已覆盖该路径）。

`recommendation.NewMemoryPersonalizer` 后续已被 PostgreSQL + pgvector 实现替代并删除。

### 过程中的一次事故

执行期间我误跑了 `git checkout -- internal/`，把前两轮在 `internal/` 下的改动全部还原（删除的目录回来了、修改的文件回退了），而 `cmd/`、`scripts/`、`Makefile` 的改动幸存，一度造成不一致状态。已逐项重做并核对。教训：清理工作区前先确认范围，`git checkout -- <dir>` 会连同已删除的跟踪文件一起恢复。

---

## 待定：需要你拍板的一项

`internal/contract` 是否保留？它用 340 行手工清单换取「路由和请求字段不会被意外改动」的保证。保留 = 每次加路由多改一处；删除 = 依赖 code review 兜底 SEO 路径。我倾向保留，但这是你的取舍。

---

## 后续步骤（按收益排序，尚未执行）

### 第二步：`media` / `media_units` / `vod_items` 三表关系

引用量悬殊：`media` 1,528 处，`vod_items` 35 处，`media_units` 17 处。**初版把这个判成「cutover 半途而废的残留」是错的** —— 迁移已完成，所以三张表并存是当前设计的真实形态，不是遗留。需要先读懂三者的职责边界（README 第 58–67 行有说明：统一身份 / 作品分集 / 资源站记录），再判断是否真有冗余。这一步不能靠 grep，要靠读代码。

### ~~第三步：100 个 interface + 每模块一份 memory.go~~ —— 核实后作废

这是初版审计里**错得最离谱的一条**，两个前提都不成立：

**「大量单实现 interface 可以砍」——不成立。** 全仓 97 个 interface 逐个统计引用（含 `cmd/` 和测试），**只有 1 个真正无人引用**（`mediaidentity.MediaUnitWriter`，已删）。其余 96 个都在用。看着像重复的那些其实是 Go 惯用的**消费方窄接口**：`BackgroundRunner` 在 catalog / playback / search 各声明一次，正是它让这三个包互不依赖；`mediaidentity.Resolver` 的注释写得很清楚——「单独定义可避免这些包依赖写入 API」，playback 拿到的是只读视图。合并它们只会制造跨包依赖甚至循环。**不动。**

**「memory.go 只为测试存在，可以删」——一半不成立。** 它确实主要服务测试，但有 **146 个调用点分布在 37 个测试文件**里。删掉等于重写 37 个测试文件、让单元测试依赖真实 Postgres——成本远高于收益。**保留为测试设施。**

真正的收益在别处：memory 实现同时还是 `DB_ENABLED=false` 的生产后端，那条分支已删（见第二轮）。

### 第四步：只写不读的观测表

`popularity_snapshot_runs` + `popularity_snapshots` + `playback_quality_rollups` + `playback_attempt_events` 构成一套自建观测系统；`media_field_sources` `search_logs` `history_sync_events` `trending_keywords` `site_stats` 引用量都在个位数。逐张确认读取方后再动 —— 有些可能有后台页面在读，grep 数字不足以定生死。

### 第五步：压平 41 个 migration

0018–0041 有 14 个是 canonical 迁移的分步骤。cutover 完成后可压成一个 baseline schema，新环境不必重放 41 步。收益是理解成本，不是运行时。
