# P9 平滑迁移与发布验收清单

本清单是 `new/` 上线的发布边界。代码通过、数据库迁移、运行实例、灰度流量和正式切流必须分别留证，不能互相替代。

## 1. 发布前冻结项

- [ ] 发布版本包含整个 `new/`，并记录构建版本与提交号。
- [ ] `scripts/release/source-audit.sh` 通过；根仓库不再忽略发布所需的 `new/` 文件。
- [ ] 旧系统仍可独立启动，旧数据库没有执行破坏性 schema 变更。
- [ ] 新系统使用独立数据库，目标数据库名不是 `moovie`。
- [ ] 新系统复用旧系统当前有效的 `APP_SECRET`；迁移脚本的会话连续性门禁通过。
- [ ] 已记录迁移前关键表数量和负责人。

旧库备份可按实际运维需要另行执行，但不属于数据迁移或切流硬门禁。

## 2. 自动化门禁

- [ ] `make check` 通过。
- [ ] `make race` 通过。
- [ ] `scripts/release/preflight.sh` 通过。
- [ ] SEO 路由、sitemap URL 集合和受控并发负载对比通过。
- [ ] `cmd/burstcheck` 的正常容量与强制降载场景通过：仅出现 200/受控 503、传输错误为 0、`/health` 零失败、压测后 `/ready` 为 200。
- [ ] 记录压测前/峰值/冷却后 RSS、CPU、文件描述符、数据库连接和容器重启次数；峰值 RSS 低于容器上限的 85%。
- [ ] 新架构只读 `releaseaudit` 为零失败；warning 已逐项解释并留证。
- [ ] 出站安全测试确认图片代理和资源站请求拒绝私网目标、非白名单重定向及 URL 凭据。
- [ ] 管理员资源站测试响应不包含 `vod_play_url`，外部标题和本地历史均通过文本节点渲染。

预检只读，不提供 migration、cleanup 或 traffic apply 能力。远程预检必须显式设置：

```text
REMOTE_PREFLIGHT_CONFIRM=read-only-moovie-preflight
```

纯本地联调可设置 `LOCAL_PREFLIGHT=true`，此时只跳过 Git 纳入检查，并允许等量、有限的 sitemap 窗口边界漂移；它不能用于远程地址，也不能替代正式发布前的源码纳入门禁。正式检查必须在写冻结后以零 allowance 重跑。

## 3. 数据迁移与对账

- [ ] 阅读 [`README.md`](../README.md) 的“一键数据迁移怎么用”，确认 0030 准备、单事务导入、复验、0031 最终删表和 releaseaudit 的边界。
- [ ] 保存首次 dry-run JSON，记录 `run_id`、每表 insert/update/skip/target-only/conflict 和字段级差异。
- [ ] dry-run 冲突为 0；任何 update 或 target-only 都已逐表解释，不能用参数绕过冲突。
- [ ] 确认旧 `favorites` 已完整表示为 `user_movies`，且不会把已有 `watched` 降级为 `wish`。
- [ ] 旧库 `douban_sync_jobs` 和目标库 `worker_jobs` 均无 pending/running；任务记录不作为业务数据迁移，切流后由统一队列重新调度。
- [ ] 完成旧数据首次导入，保存 apply JSON，并核对 table/favorite/canonical/total mutations。
- [ ] 对账 `movies -> media`、`watch_histories -> playback_positions`、资源网、用户密码摘要、片单和关系孤儿。
- [ ] 写冻结后执行完整一键迁移；脚本内置复验必须为 insert=0、update=0、conflict=0、favorites待转换=0。
- [ ] 0031 已应用；最终新库不存在 `movies`、`watch_histories`、`resource_episodes`、`resource_playback_health`、`legacy_media_mappings` 和旧影子列。
- [ ] 0036 已应用；`metadata_refresh_jobs`、`douban_sync_jobs` 已迁入 `worker_jobs` 后删除。
- [ ] `releaseaudit` 零失败，且 `user_movies.media_id` 全部补齐、媒体身份一致。

## 4. Feature flag 顺序

每一步都应先观察，再进入下一步；异常时只回退当前读开关。

统一搜索、唯一播放进度、播放排序、自动换源和热门快照均已固定启用，不存在新旧双读或双写开关。仍需分阶段确认的只有资源匹配：

1. 资源匹配保持 `RESOURCE_MATCH_SHADOW=true`、`RESOURCE_MATCH_AUTO_APPLY=false`。
2. 观察影子表质量数据后，再决定是否开启 `RESOURCE_MATCH_AUTO_APPLY`。

热门快照上线前运行：

```text
REQUIRE_POPULARITY_SNAPSHOTS=true
REQUIRED_POPULARITY_SOURCES=douban,tmdb,activity
MAX_POPULARITY_AGE=2h
```

快照不可用时必须自动回退豆瓣榜单；手动选源和播放器内核不受候选排序影响。

## 5. 真实浏览器与播放回归

- [ ] 桌面浏览器与 iOS/Safari 的 HLS 播放通过。
- [ ] 真实 HLS、FLV、弹幕、倍速、全屏和错误提示通过。
- [ ] 未登录本地进度、登录首次合并、首页与用户中心继续观看一致。
- [ ] 手动换源保持同一集和当前进度。
- [ ] 第三集故障只切换已确认的第三集候选；没有同集候选时停止。
- [ ] 自动下一集、播放完成、刷新页面恢复进度通过。
- [ ] 热门无资源影视明确显示不可播放，不产生错误播放入口。

静态检查、Go 测试和 HTTP 200 不能替代本节。

## 6. 资源生命周期

- [ ] 首次发布只允许状态降为 `cold`，不执行播放 URL 或元数据的物理删除。
- [ ] 至少观察 30 天后才生成首次 dry-run。
- [ ] dry-run 留存候选数、资源站/状态分布、历史引用、唯一资源、估算空间和样例。
- [ ] apply 必须引用未过期的 `batch_id` 并由人工确认。
- [ ] 抽样验证 cold 资源可恢复为 active，`media` 与外部 ID 数量不变。
- [ ] 对比降冷前后活跃影视可播放率，确认没有下降。

## 7. 灰度与正式切流

- [ ] 5% 只读灰度：健康、错误率、P95 和关键页面正常。
- [ ] 5% 灰度确认 `X-Moovie-Overload` 降载率低于 1%；持续超过 5% 时停止加流量并扩容或降低重请求预算。
- [ ] 25% 只读灰度：继续观察搜索、热门和详情页。
- [ ] 50% 只读灰度：完成一轮完整浏览器旅程。
- [ ] 100% 前确认数据迁移报告、写冻结、APP_SECRET 一致和 releaseaudit 零失败均有证据。
- [ ] 100% 后继续保持写冻结，直到公开流量健康检查和写入 smoke test 通过。
- [ ] 人工解除写冻结，并记录解除时间。

## 8. 监控与回滚演练

- [ ] 监控搜索延迟、unmatched 比例、播放进度总量/媒体关联量/纯资源量、播放十秒成功率和错误换集数。
- [ ] 监控容器 OOM/restart、RSS/CPU、goroutine、文件描述符、PostgreSQL 使用/等待连接及应用降载 503。
- [ ] 容器 liveness 使用 `/health`；`/ready` 只用于负载均衡摘流，不触发自动重启。
- [ ] Web 保持 `JOBS_IN_WEB=false`，独立 Worker 使用单独的内存、CPU 和数据库连接预算。
- [ ] 监控刷新队列、热门快照年龄、cold 数量、dry-run/apply 差异。
- [ ] 使用管理员只读接口 `/api/v2/admin/metrics` 核对上述指标，且 `wrong_unit_sessions` 必须为 0。
- [ ] 确认日志不包含完整带签名播放 URL、原始 IP 或敏感 token。
- [ ] 抽查业务错误日志可用 `request_id` 串联 HTTP 请求、搜索、history 与播放事件，且不记录搜索原词。
- [ ] 回退演练只摘除新流量并恢复冻结时的旧应用/旧库；不删除新库、不逆向执行 schema，也不尝试新库到旧库的反向迁移。
- [ ] 团队理解：解除新系统写冻结后，旧库不会获得新增片单、评论和进度，应用回退可能产生数据缺口。

## 9. 发布证据

发布记录至少包含：版本、迁移报告、releaseaudit JSON、preflight 输出、浏览器截图或录像、灰度时间线、监控截图、最终批准人和回滚演练结果。
