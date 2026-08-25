#!/bin/sh
# 只审计发布源码是否完整，不构建镜像，也不修改 Git 或业务数据。
set -eu

# 所有路径都从脚本自身位置计算，避免调用者当前目录影响审计目标。
script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
new_root=$(CDPATH= cd -- "$script_dir/../.." && pwd)
repository_root=$(CDPATH= cd -- "$new_root/.." && pwd)
mode=${1-}
case "$mode" in
    ""|--local) ;;
    *)
        echo "usage: $0 [--local]" >&2
        exit 2
        ;;
esac

# 这些文件和目录缺少任意一个，最终发布产物都无法完整运行。
for required in go.mod go.sum Dockerfile docker-compose.yml .dockerignore web/templates internal/platform/database/migrations; do
    if [ ! -e "$new_root/$required" ]; then
        echo "release source is missing: $required" >&2
        exit 2
    fi
done

if ! grep -Eq '^\.env$' "$new_root/.dockerignore"; then
    echo ".dockerignore must exclude .env" >&2
    exit 2
fi

if [ "$mode" = "--local" ]; then
    echo "local source audit passed; Git inclusion remains a mandatory release gate"
elif git -C "$repository_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    # 正式模式逐文件确认已被 Git 跟踪，防止忽略的 new/ 在部署时变成空目录。
    find "$new_root" -type f \
        ! -name '.env' \
        ! -name 'coverage.out' \
        ! -name '*.log' \
        ! -path "$new_root/bin/*" \
		! -path "$new_root/.migration-reports/*" \
        -print | while IFS= read -r path; do
            relative=${path#"$repository_root"/}
            if ! git -C "$repository_root" ls-files --error-unmatch -- "$relative" >/dev/null 2>&1; then
                echo "release source is not tracked by Git: $relative" >&2
                echo "the deployment artifact would omit refactored files; include the complete new/ tree before preflight" >&2
                exit 2
            fi
        done
elif [ "${SOURCE_INCLUSION_CONFIRMED-}" != "true" ]; then
    echo "Git metadata is unavailable; set SOURCE_INCLUSION_CONFIRMED=true only after verifying the complete new/ tree in the release artifact" >&2
    exit 2
fi

if [ "$mode" != "--local" ]; then
    echo "release source inclusion audit passed"
fi
