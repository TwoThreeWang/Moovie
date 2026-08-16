#!/usr/bin/env bash
#
# 一键数据迁移：旧库 moovie -> 新库 moovie_v2
#
# 它只是 cmd/datamigrate 的包装，不改变任何安全语义：
#   1. 把新库结构准备到 0030（暂不删过渡表）
#   2. 只读 dry-run，生成 JSON 报告
#   3. 冲突为 0 时在单事务内执行 apply
#   4. 再跑一次 dry-run 复验
#   5. 完成 0031 删除兼容表，再执行发布审计
#
# 冲突 > 0 时脚本一定停下，这条硬门禁不会被自动绕过。
#
# 用法：
#   ./scripts/migrate.sh                # 只做 dry-run（默认，安全）
#   ./scripts/migrate.sh --apply --write-freeze-confirmed
#                                        # 完整写入流程
#
# 环境变量：
#   SOURCE_ENV   旧库 .env 路径   默认 ../.env
#   TARGET_ENV   新库 .env 路径   默认 ./.env
#   REPORT_DIR   报告输出目录     默认 ./.migration-reports
#
# 写入模式必须显式确认旧 Web/Worker 已停止写入。

set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NEW_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$NEW_ROOT"

SOURCE_ENV="${SOURCE_ENV:-../.env}"
TARGET_ENV="${TARGET_ENV:-./.env}"
REPORT_DIR="${REPORT_DIR:-$NEW_ROOT/.migration-reports}"
RUN_ID="${RUN_ID:-migration-$(date -u +%Y%m%dT%H%M%SZ)}"
export GOCACHE="${GOCACHE:-/private/tmp/gocache}"

APPLY=false
WRITE_FREEZE=false
for arg in "$@"; do
  case "$arg" in
    --apply)   APPLY=true ;;
    --write-freeze-confirmed) WRITE_FREEZE=true ;;
    -h|--help) sed -n '2,${/^[^#]/q;p;}' "${BASH_SOURCE[0]}"; exit 0 ;;
    *)         echo "未知参数: $arg" >&2; exit 2 ;;
  esac
done

log()  { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m!!  %s\033[0m\n' "$*" >&2; }
die()  { printf '\033[1;31mXX  %s\033[0m\n' "$*" >&2; exit 1; }

command -v go >/dev/null || die "找不到 go"
command -v jq >/dev/null || die "找不到 jq，请先安装：brew install jq"
[[ -f "$SOURCE_ENV" ]] || die "源库 env 不存在: ${SOURCE_ENV}"
[[ -f "$TARGET_ENV" ]] || die "目标库 env 不存在: ${TARGET_ENV}"

# 迁移密码哈希只能保证用户可以重新登录；要让已经签发的 JWT Cookie 在切流后继续有效，
# 两套程序还必须使用同一个 APP_SECRET。这里只在内存中比较，不把密钥写入日志或报告。
read_env_value() {
  local file="$1" wanted="$2"
  awk -v wanted="$wanted" '
    /^[[:space:]]*(#|$)/ { next }
    {
      line = $0
      sub(/^[[:space:]]*export[[:space:]]+/, "", line)
      separator = index(line, "=")
      if (separator == 0) { next }
      key = substr(line, 1, separator - 1)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", key)
      if (key != wanted) { next }
      value = substr(line, separator + 1)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (length(value) >= 2 && ((substr(value, 1, 1) == "\"" && substr(value, length(value), 1) == "\"") || (substr(value, 1, 1) == "\047" && substr(value, length(value), 1) == "\047"))) {
        value = substr(value, 2, length(value) - 2)
      }
      print value
      exit
    }
  ' "$file"
}

if [[ "$APPLY" == true ]]; then
  SOURCE_APP_SECRET="$(read_env_value "$SOURCE_ENV" APP_SECRET)"
  TARGET_APP_SECRET="$(read_env_value "$TARGET_ENV" APP_SECRET)"
  [[ -n "$SOURCE_APP_SECRET" ]] || die "源库 env 缺少 APP_SECRET，无法保证已登录用户无感切换"
  [[ -n "$TARGET_APP_SECRET" ]] || die "目标库 env 缺少 APP_SECRET，无法保证已登录用户无感切换"
  [[ "$SOURCE_APP_SECRET" == "$TARGET_APP_SECRET" ]] || die "两份 env 的 APP_SECRET 不一致；请让新系统复用旧密钥后再迁移，否则现有登录会话会失效"
  unset SOURCE_APP_SECRET TARGET_APP_SECRET
fi

mkdir -p "$REPORT_DIR"
chmod 700 "$REPORT_DIR" 2>/dev/null || true

DRY_BEFORE="$REPORT_DIR/$RUN_ID.dry-run-before.json"
APPLY_JSON="$REPORT_DIR/$RUN_ID.apply.json"
DRY_AFTER="$REPORT_DIR/$RUN_ID.dry-run-after.json"
AUDIT_JSON="$REPORT_DIR/$RUN_ID.release-audit.json"

# datamigrate 在有冲突时退出码为 1，这里要先拿到 JSON 再自己判断。
run_dry_run() {
  local out="$1" code=0
  go run ./cmd/datamigrate \
    --source-env "$SOURCE_ENV" --target-env "$TARGET_ENV" --json \
    >"$out" 2>"$out.err" || code=$?
  if [[ ! -s "$out" ]]; then
    cat "$out.err" >&2
    die "dry-run 未产出报告（退出码 ${code}）"
  fi
}

if [[ "$APPLY" == true ]]; then
  [[ "$WRITE_FREEZE" == true ]] || die "apply 前必须停止旧 Web/Worker 写入，并加 --write-freeze-confirmed"
  log "1/6 准备目标库结构到 0030（暂不删兼容表）"
  go run ./cmd/dbmigrate --target-env "$TARGET_ENV" --through 0030
fi

if [[ "$APPLY" == true ]]; then
  log "2/6 只读 dry-run  (run_id=${RUN_ID})"
else
  log "只读 dry-run  (run_id=${RUN_ID})"
fi
run_dry_run "$DRY_BEFORE"

CONFLICTS=$(jq -r '.inspection.conflicts // 0' "$DRY_BEFORE")
jq -r '"  " + (.inspection.summary
  | "总计  source=\(.source_rows) target=\(.target_rows) insert=\(.insert_rows) update=\(.update_rows) skip=\(.skip_rows) only-new=\(.target_only_rows)"),
  "  favorites 待转换 = \(.inspection.favorites.would_insert // 0)"' "$DRY_BEFORE"

if [[ "$CONFLICTS" -gt 0 ]]; then
  warn "发现 ${CONFLICTS} 个冲突，必须先修数据，脚本不会绕过。明细："
  jq -r '.inspection.tables[]
         | select(((.natural_key_missing//[]|length) + (.duplicate_source_keys//0)) > 0)
         | "    [\(.table)] 自然键缺失=\(.natural_key_missing//[]|join(",")) 重复键=\(.duplicate_source_keys//0)"' "$DRY_BEFORE" || true
  jq -r '.inspection.tables[] | . as $t | (.check_violations//[])[]
         | "    [\($t.table)] \(.)"' "$DRY_BEFORE" || true
  # favorites 的冲突不在 tables 里，必须单独打印，否则会出现「有冲突但无明细」。
  jq -r '.inspection.favorites
         | select(((.missing_users//0) + (.missing_movies//0)) > 0)
         | "    [favorites] 源库缺失用户=\(.missing_users//0) 缺失影片=\(.missing_movies//0)"' "$DRY_BEFORE" || true
  die "报告：${DRY_BEFORE}"
fi

jq -r '.inspection.tables[] | select((.changed_columns//[]|length) > 0)
       | "  将覆盖 [\(.table)]: \(.changed_columns|join(", "))"' "$DRY_BEFORE" || true
jq -r '.inspection.tables[] | select((.unchecked_constraints//[]|length) > 0)
       | "  提示 [\(.table)] 有无法预检的 CHECK 约束: \(.unchecked_constraints|join(", "))"' "$DRY_BEFORE" || true
echo "  报告: ${DRY_BEFORE}"

if [[ "$APPLY" != true ]]; then
  log "dry-run 完成，无冲突。确认无误后加 --apply 执行真正迁移。"
  exit 0
fi

log "3/6 写入目标库（单事务，失败自动回滚）"
apply_code=0
go run ./cmd/datamigrate \
  --source-env "$SOURCE_ENV" --target-env "$TARGET_ENV" \
  --apply \
  --confirm-source moovie \
  --confirm-target moovie_v2 \
  --write-freeze-confirmed \
  --json >"$APPLY_JSON" 2>"$APPLY_JSON.err" || apply_code=$?

if [[ ! -s "$APPLY_JSON" ]]; then
  cat "$APPLY_JSON.err" >&2
  die "apply 未产出报告（退出码 ${apply_code}）。目标事务已回滚。"
fi
STATUS=$(jq -r '.status' "$APPLY_JSON")
if [[ "$STATUS" != "committed" ]]; then
  warn "apply 未提交，status=${STATUS}。阻断原因："
  jq -r '(.apply_blockers // [])[] | "    - " + .' "$APPLY_JSON"
  die "目标库未发生变更。报告：${APPLY_JSON}"
fi
jq -r '"  已提交: insert=\(.apply_result.table_inserts) update=\(.apply_result.table_updates) favorites=\(.apply_result.favorite_inserts) 新架构回填=\(.apply_result.canonical_mutations) 序列重置=\(.apply_result.sequences_reset)"' "$APPLY_JSON"

log "4/6 复验 dry-run（期望 insert/update/conflicts 全为 0）"
run_dry_run "$DRY_AFTER"
R_INSERT=$(jq -r '.inspection.summary.insert_rows // 0' "$DRY_AFTER")
R_UPDATE=$(jq -r '.inspection.summary.update_rows // 0' "$DRY_AFTER")
R_CONFLICT=$(jq -r '.inspection.conflicts // 0' "$DRY_AFTER")
R_FAV=$(jq -r '.inspection.favorites.would_insert // 0' "$DRY_AFTER")
echo "  insert=${R_INSERT} update=${R_UPDATE} conflicts=${R_CONFLICT} favorites待转换=${R_FAV}"

if [[ "$R_INSERT" -ne 0 || "$R_UPDATE" -ne 0 || "$R_CONFLICT" -ne 0 || "$R_FAV" -ne 0 ]]; then
  die "数据已提交，但复验数字不符合预期，请人工核对报告后再切流。"
fi

log "5/6 完成最终结构（删除兼容表与影子列）"
go run ./cmd/dbmigrate --target-env "$TARGET_ENV"

log "6/6 执行只读发布审计"
audit_code=0
go run ./cmd/releaseaudit --target-env "$TARGET_ENV" --json >"$AUDIT_JSON" 2>"$AUDIT_JSON.err" || audit_code=$?
if [[ ! -s "$AUDIT_JSON" ]]; then
  cat "$AUDIT_JSON.err" >&2
  die "发布审计未产出报告（退出码 ${audit_code}）"
fi
AUDIT_FAILED=$(jq -r '.failed // 0' "$AUDIT_JSON")
AUDIT_WARNINGS=$(jq -r '.warnings // 0' "$AUDIT_JSON")
echo "  failed=${AUDIT_FAILED} warnings=${AUDIT_WARNINGS}"
if [[ "$AUDIT_FAILED" -ne 0 ]]; then
  jq -r '.checks[] | select(.status == "fail") | "    - \(.name): value=\(.value) \(.description)"' "$AUDIT_JSON"
  die "最终数据已保留，但发布审计未通过，不能切流。"
fi

log "完成。全部报告在 ${REPORT_DIR}/  run_id=${RUN_ID}"
echo "旧库数据已迁入最终结构并通过发布审计。"
