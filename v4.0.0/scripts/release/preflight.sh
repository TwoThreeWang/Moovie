#!/bin/sh
# 发布预检全部为检查动作；不会执行数据导入、清理、切流或回滚。
set -eu

# 明确拒绝缺失变量，避免命令把空字符串误当成合法目标。
require_env() {
    value=$(printenv "$1" 2>/dev/null || true)
    if [ -z "$value" ]; then
        echo "missing required environment variable: $1" >&2
        exit 2
    fi
}

for name in NEW_BASE_URL SEO_MOVIE_ID SEO_SOURCE_KEY SEO_VOD_ID SEO_PUBLIC_USER_ID SEO_YEAR_MONTH; do
    require_env "$name"
done
if [ "${LOCAL_PREFLIGHT-}" != "true" ]; then
    require_env MIGRATION_TARGET_DSN
fi

# 旧系统已下线后 OLD_BASE_URL 可以不设置：新旧对比类检查会被跳过，
# 其余检查照常执行。旧站仍可访问时应当继续设置它，以便守住 SEO 路径兼容。
compare_with_legacy=false
if [ -n "${OLD_BASE_URL-}" ]; then
    compare_with_legacy=true
fi

remote=false
for base_url in ${OLD_BASE_URL-} "$NEW_BASE_URL"; do
    case "$base_url" in
        http://localhost:*|https://localhost:*|http://127.0.0.1:*|https://127.0.0.1:*) ;;
        *) remote=true ;;
    esac
done
# 访问非本机地址前需要固定确认词，防止误对生产环境发起压力检查。
if [ "$remote" = true ] && [ "${REMOTE_PREFLIGHT_CONFIRM-}" != "read-only-moovie-preflight" ]; then
    echo "remote preflight requires REMOTE_PREFLIGHT_CONFIRM=read-only-moovie-preflight" >&2
    exit 2
fi

# 各阶段按从低成本静态检查到真实 HTTP 压力检查的顺序执行，任一失败立即停止。
echo "[1/7] release source inclusion"
if [ "${LOCAL_PREFLIGHT-}" = "true" ]; then
    if [ "$remote" = true ]; then
        echo "LOCAL_PREFLIGHT=true is only allowed when every configured base URL is local" >&2
        exit 2
    fi
    ./scripts/release/source-audit.sh --local
else
    ./scripts/release/source-audit.sh
fi

echo "[2/7] static, unit, vet and build gates"
make check

echo "[3/7] race detector"
make race

echo "[4/7] route and SEO compatibility"
if [ "$compare_with_legacy" = true ]; then
    GOCACHE="${GOCACHE:-/private/tmp/gocache}" go run ./cmd/compatcheck \
        -old "$OLD_BASE_URL" \
        -new "$NEW_BASE_URL" \
        -manifest ./compat/seo_cases.json \
        -require-all
else
    echo "skipped: OLD_BASE_URL unset; SEO 路径契约改由 compat/seo_cases.json 与路由测试守护"
fi

echo "[5/7] sitemap URL-set compatibility"
if [ "$compare_with_legacy" = true ]; then
    if [ "${LOCAL_PREFLIGHT-}" = "true" ]; then
        GOCACHE="${GOCACHE:-/private/tmp/gocache}" go run ./cmd/sitemapcheck \
            -old "$OLD_BASE_URL" \
            -new "$NEW_BASE_URL" \
            -allow-balanced-drift "${LOCAL_SITEMAP_BALANCED_DRIFT:-4}"
    else
        GOCACHE="${GOCACHE:-/private/tmp/gocache}" go run ./cmd/sitemapcheck \
            -old "$OLD_BASE_URL" \
            -new "$NEW_BASE_URL"
    fi
else
    echo "skipped: OLD_BASE_URL unset"
fi

echo "[6/7] read-only refactored architecture audit"
if [ "${LOCAL_PREFLIGHT-}" = "true" ]; then
    releaseaudit_target="-target-env=./.env"
else
    releaseaudit_target="-dsn=$MIGRATION_TARGET_DSN"
fi
if [ "${REQUIRE_POPULARITY_SNAPSHOTS:-false}" = "true" ]; then
    GOCACHE="${GOCACHE:-/private/tmp/gocache}" go run ./cmd/releaseaudit \
        "$releaseaudit_target" \
        -require-popularity \
        -required-popularity-sources "${REQUIRED_POPULARITY_SOURCES:-douban,tmdb,activity}" \
        -max-popularity-age "${MAX_POPULARITY_AGE:-2h}"
else
    GOCACHE="${GOCACHE:-/private/tmp/gocache}" go run ./cmd/releaseaudit \
        "$releaseaudit_target"
fi

echo "[7/7] refactored burst stability and health isolation"
GOCACHE="${GOCACHE:-/private/tmp/gocache}" go run ./cmd/burstcheck \
    -target "$NEW_BASE_URL" \
    -path "${BURST_PATH:-/movie/1292052}" \
    -requests "${BURST_REQUESTS:-500}" \
    -concurrency "${BURST_CONCURRENCY:-50}" \
    -max-p95 "${BURST_MAX_P95:-5s}" \
    -require-shedding="${BURST_REQUIRE_SHEDDING:-false}"

echo "automated preflight passed"
echo "manual browser journeys, Core Web Vitals and canary observation remain required"
