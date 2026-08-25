# 高并发容量与容器稳定性手册

## 目标

突发流量到达资源预算后，新系统必须快速返回带 `Retry-After: 1` 的受控 `503`，并持续保证 `/health` 可用；不能靠无限增加 goroutine、数据库连接、上游连接或缓存内存来维持表面成功率。

`/health` 是不访问数据库的存活探针，只能用于容器健康检查。`/ready` 会验证 PostgreSQL，只能用于负载均衡摘流；数据库池暂时饱和时不得据此自动重启容器。

## 默认资源预算

| 资源 | 默认值 | 作用 |
| --- | ---: | --- |
| HTTP 总在途请求 | 64 | 限制整个 Web 进程同时处理的请求 |
| 搜索、详情、发现、推荐等重请求 | 12 | 防止数据库和外部 API 被同时打满 |
| 图片代理请求 | 24 | 限制上游图片连接与文件描述符 |
| 过载排队时间 | 100ms | 超时后立即降载，不累积长队列 |
| TCP 连接 | 512 | 限制空闲连接和文件描述符占用 |
| 请求体 / 请求头 | 1MiB / 64KiB | 拒绝异常大输入 |
| PostgreSQL Web / Worker | 12 / 6 | 多副本总和不得超过数据库连接预算 |
| 单个上游主机连接 | 12 | 防止搜索或图片请求创建无限 socket |
| 单次搜索资源站并发 | 6 | 资源站数量增加时仍保持固定扇出 |
| 后台机会任务 | 8 | 达到上限后丢弃可重试任务，不创建等待 goroutine |
| 搜索缓存 | 200 条 | 仅保存页面渲染字段，不缓存正文和播放地址 |
| 个性化推荐缓存 | 512 用户 / 1 小时 | 同用户冷请求合并，淘汰过期或最早项，只保留渲染字段 |
| AppleCMS 单响应 | 4MiB | 流式解码并拒绝超大响应 |
| 成功访问日志 | 生产 10% 且最多 20 条/秒 | 防止突发流量放大日志 CPU、磁盘和容器 I/O |

Docker Compose 默认把 Web 和 Worker 分开。Web 使用 1GiB 容器上限与 `GOMEMLIMIT=700MiB`；Worker 使用 512MiB 与 `GOMEMLIMIT=320MiB`。Go 堆上限必须低于容器总内存，为 goroutine 栈、socket buffer、模板响应和运行时保留余量。

## 先确认旧容器为什么重启

不要只看 `RestartCount`。至少保留下面证据：

```bash
docker inspect moovie-app --format '{{json .State}}'
docker inspect moovie-app --format 'oom={{.State.OOMKilled}} exit={{.State.ExitCode}} restarts={{.RestartCount}} error={{.State.Error}}'
docker stats --no-stream moovie-app
docker events --since 30m --filter container=moovie-app
```

- `OOMKilled=true` 或退出码 `137`：优先检查缓存、响应体、goroutine、连接数和容器内存余量。
- `/ready` 失败后被编排器重启：改用 `/health` 做 liveness，把 `/ready` 只用于摘流。
- panic 或退出码 `1/2`：按退出前最后一个 `request_id` 和错误栈定位，不应归因于并发。
- 宿主机整体 OOM、磁盘满或日志暴涨：单容器指标正常也不能排除宿主机资源问题。

## 本地直接运行的突发压测

本地验收继续使用直接 `go run`，不依赖 Docker：

```bash
PORT=5009 \
SITE_URL=http://127.0.0.1:5009 \
JOBS_IN_WEB=false \
DB_AUTO_MIGRATE=false \
GOMEMLIMIT=700MiB \
GOMAXPROCS=2 \
GOCACHE=/private/tmp/gocache \
go run ./cmd/web
```

正常容量门禁：

```bash
GOCACHE=/private/tmp/gocache go run ./cmd/burstcheck \
  -target http://127.0.0.1:5009 \
  -path /movie/1292052 \
  -requests 2000 \
  -concurrency 200 \
  -max-p95 5s
```

降载故障注入：启动时临时设置 `HTTP_MAX_IN_FLIGHT=8`、`HTTP_MAX_HEAVY_IN_FLIGHT=2`、`HTTP_QUEUE_TIMEOUT_MILLISECONDS=10`，然后增加 `-require-shedding`。必须同时看到成功响应和受控 503，不能出现连接错误、健康检查失败或进程退出。

压测期间记录监听进程的 RSS、CPU、线程和文件描述符，并在停止发压后继续观察至少 60 秒：

```bash
lsof -nP -iTCP:5009 -sTCP:LISTEN
ps -o pid,rss,vsz,%cpu,%mem,etime,command -p <pid>
lsof -p <pid> | wc -l
```

## 灰度准入标准

- 压测返回状态只能是 `200` 和受控 `503`，传输错误必须为 0。
- 压测期间 `/health` 失败数为 0；结束后 `/ready` 恢复为 200。
- 容器不 OOM、不重启；峰值 RSS 不超过容器上限的 85%，结束后不持续单调增长。
- PostgreSQL 活跃连接不超过配置总和；副本数 × `DB_MAX_CONNS` 加 Worker 连接必须留出管理连接余量。
- 5% 灰度的应用降载率目标低于 1%；持续超过 5% 时先扩容或减少重请求，不得直接放大全部并发上限。
- 成功动态请求日志在 production 默认采样 10%，且硬限制为每秒 20 条；慢请求、真实 5xx 和每秒一次的降载汇总仍保留。

## 灰度期间监控与处置

必须同时观察：容器 RSS/CPU、重启次数、goroutine、文件描述符、PostgreSQL 使用/等待连接、上游超时、P95/P99、`X-Moovie-Overload` 503 比例、Worker 队列长度和 `/ready` 状态。

1. 降载率上升但 RSS 稳定：增加 Web 副本或降低边缘入口速率，先不要提高单实例并发。
2. RSS 持续增长：停止加流量，保留 heap/goroutine 证据并回退；不要用重启掩盖泄漏。
3. 数据库等待增加：降低 `HTTP_MAX_HEAVY_IN_FLIGHT` 或 `DB_MAX_CONNS`，核算全部副本连接总和。
4. 外部来源超时增加：保持断路器和上游连接上限，使用缓存/快照降级，不延长超时。
5. Worker 资源高：单独限制或暂停 Worker；Web 不得重新启用 `JOBS_IN_WEB=true`。

静态资源和 `/api/proxy/image/` 应在反向代理或 CDN 缓存。边缘层负责每 IP 速率限制、Bot 防护和连接队列；应用内闸门是最后一道进程保护，不能替代边缘控制。
