# P9 平滑迁移与发布验收清单

本清单是 `new/` 上线的发布边界。代码通过、数据库迁移、运行实例、灰度流量和正式切流必须分别留证，不能互相替代。

## 1. 发布前冻结项

- [ ] 发布版本包含整个 `new/`，并记录构建版本与提交号。
- [ ] `scripts/release/source-audit.sh` 通过；根仓库不再忽略发布所需的 `new/` 文件。

## 2. 自动化门禁

- [ ] `make check` 通过。
- [ ] `make race` 通过。
- [ ] `scripts/release/source-audit.sh` 通过。
- [ ] `make check` 中的页面 SEO 回归断言通过（`internal/content`、`internal/search` 逐项校验 Title、H1、Description、Robots、Canonical、OG/Twitter 和 JSON-LD）。
- [ ] `cmd/burstcheck` 的正常容量与强制降载场景通过：仅出现 200/受控 503、传输错误为 0、`/health` 零失败、压测后 `/ready` 为 200。
- [ ] 记录压测前/峰值/冷却后 RSS、CPU、文件描述符、数据库连接和容器重启次数；峰值 RSS 低于容器上限的 85%。
- [ ] 出站安全测试确认图片代理和资源站请求拒绝私网目标、非白名单重定向及 URL 凭据。
- [ ] 管理员资源站测试响应不包含 `vod_play_url`，外部标题和本地历史均通过文本节点渲染。

## 3. 数据迁移与对账（已于 2026-08 完成，不再重复执行）

旧库 `moovie` 到新库 `moovie_v2` 的一次性数据迁移、0031 最终删表、0036 任务队列统一和 `releaseaudit` 复验均已在生产完成并留证。对应工具（`cmd/datamigrate`、`scripts/migrate.sh`、`cmd/releaseaudit`、`scripts/release/preflight.sh`）已从仓库移除，历史版本见 Git。本节保留为发布记录索引，日常发布不再执行。

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
- [ ] 100% 后完成公开流量健康检查和写入 smoke test。

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

发布记录至少包含：版本、`make check` / `make race` 输出、`source-audit.sh` 与 `burstcheck` 结果、浏览器截图或录像、灰度时间线、监控截图、最终批准人和回滚演练结果。
