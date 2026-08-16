// datamigrate 把旧库 moovie 的业务数据迁移到新库 moovie_v2。
//
// 默认是只读 dry-run；只有同时通过全部门禁才会写入目标库，
// 并且所有写入都在一个事务里，任一步失败整体回滚。
// 旧库连接始终是只读事务，工具没有写旧库的代码路径。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/datamigrate"
	platformconfig "github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/jackc/pgx/v5"
)

const (
	sourceConfirmation = "moovie"
	targetConfirmation = "moovie_v2"
	// 十六进制内容是 ASCII "MoovieV2"，同一目标库同时只允许一个迁移事务。
	migrationAdvisoryLock int64 = 0x4d6f6f7669655632
)

// report 不包含 DSN、用户名或密码，可直接保存为发布审计证据。
type report struct {
	Mode           string                   `json:"mode"`
	Status         string                   `json:"status"`
	StartedAt      time.Time                `json:"started_at"`
	FinishedAt     time.Time                `json:"finished_at"`
	SourceDatabase string                   `json:"source_database"`
	TargetDatabase string                   `json:"target_database"`
	Schema         string                   `json:"schema"`
	Blockers       []string                 `json:"apply_blockers,omitempty"`
	Inspection     datamigrate.Inspection   `json:"inspection"`
	ApplyResult    *datamigrate.ApplyResult `json:"apply_result,omitempty"`
}

func main() {
	started := time.Now().UTC()
	sourceDSN := flag.String("source", strings.TrimSpace(os.Getenv("MIGRATION_SOURCE_DSN")), "旧 PostgreSQL DSN（也可用 MIGRATION_SOURCE_DSN）")
	targetDSN := flag.String("target", strings.TrimSpace(os.Getenv("MIGRATION_TARGET_DSN")), "新 PostgreSQL DSN（也可用 MIGRATION_TARGET_DSN）")
	sourceEnv := flag.String("source-env", "", "包含旧库配置的本地 .env；设置后覆盖 -source")
	targetEnv := flag.String("target-env", "", "包含新库配置的本地 .env；设置后覆盖 -target")
	schema := flag.String("schema", "public", "要迁移的数据库 schema")
	apply := flag.Bool("apply", false, "写入目标库；默认仅执行只读 dry-run")
	confirmSource := flag.String("confirm-source", "", "apply 时必须明确填写 moovie")
	confirmTarget := flag.String("confirm-target", "", "apply 时必须明确填写 moovie_v2")
	allowTestTarget := flag.Bool("allow-test-target", false, "仅允许写入 moovie_v2_cutover_test_ 前缀的隔离演练库")
	writeFreeze := flag.Bool("write-freeze-confirmed", false, "确认旧系统和新系统写入及 Worker 已冻结")
	jsonOutput := flag.Bool("json", false, "输出可保存的 JSON 计划或结果")
	timeout := flag.Duration("timeout", 60*time.Minute, "整体超时")
	flag.Parse()

	if strings.TrimSpace(*sourceEnv) != "" {
		database, err := platformconfig.DatabaseConfigFromDotEnv(*sourceEnv)
		if err != nil {
			fatalf("读取源库 env 文件失败: %v", err)
		}
		*sourceDSN = database.DSN()
	}
	if strings.TrimSpace(*targetEnv) != "" {
		database, err := platformconfig.DatabaseConfigFromDotEnv(*targetEnv)
		if err != nil {
			fatalf("读取目标库 env 文件失败: %v", err)
		}
		*targetDSN = database.DSN()
	}
	if strings.TrimSpace(*sourceDSN) == "" || strings.TrimSpace(*targetDSN) == "" {
		fatalf("源库和目标库配置都不能为空；请使用 DSN、环境变量或 -source-env/-target-env")
	}

	sourceConfig, err := pgx.ParseConfig(*sourceDSN)
	if err != nil {
		fatalf("解析源库 DSN 失败: %v", err)
	}
	targetConfig, err := pgx.ParseConfig(*targetDSN)
	if err != nil {
		fatalf("解析目标库 DSN 失败: %v", err)
	}
	if sameDatabase(sourceConfig, targetConfig) {
		fatalf("源库和目标库解析后指向同一个数据库，已拒绝继续")
	}
	if strings.EqualFold(targetConfig.Database, sourceConfirmation) {
		fatalf("拒绝把旧库 %s 作为目标库", sourceConfirmation)
	}
	testTarget := *allowTestTarget && strings.HasPrefix(strings.ToLower(targetConfig.Database), "moovie_v2_cutover_test_")
	if *apply && (!strings.EqualFold(sourceConfig.Database, sourceConfirmation) ||
		(!strings.EqualFold(targetConfig.Database, targetConfirmation) && !testTarget)) {
		fatalf("apply 只允许从数据库 %s 写入 %s，或显式允许的隔离演练库；当前为 %s -> %s",
			sourceConfirmation, targetConfirmation, sourceConfig.Database, targetConfig.Database)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	source, err := pgx.Connect(ctx, *sourceDSN)
	if err != nil {
		fatalf("连接源库失败: %v", err)
	}
	defer source.Close(context.Background())
	target, err := pgx.Connect(ctx, *targetDSN)
	if err != nil {
		fatalf("连接目标库失败: %v", err)
	}
	defer target.Close(context.Background())

	// 源库永远使用只读快照；apply 也只写目标库。
	sourceTx, err := source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		fatalf("创建源库只读事务失败: %v", err)
	}
	defer sourceTx.Rollback(context.Background())

	targetOptions := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if *apply {
		targetOptions.AccessMode = pgx.ReadWrite
	}
	targetTx, err := target.BeginTx(ctx, targetOptions)
	if err != nil {
		fatalf("创建目标库事务失败: %v", err)
	}
	defer targetTx.Rollback(context.Background())

	var acquired bool
	if err := targetTx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, migrationAdvisoryLock).Scan(&acquired); err != nil {
		fatalf("获取迁移锁失败: %v", err)
	}
	if !acquired {
		fatalf("另一个 datamigrate 正在使用当前目标库")
	}

	importer := datamigrate.Importer{Schema: *schema, Source: sourceTx, Target: targetTx, Specs: datamigrate.DefaultTables}
	inspection, err := importer.Inspect(ctx)
	if err != nil {
		fatalf("生成迁移计划失败: %v", err)
	}

	result := report{
		Mode:           modeName(*apply),
		StartedAt:      started,
		SourceDatabase: sourceConfig.Database,
		TargetDatabase: targetConfig.Database,
		Schema:         schemaName(*schema),
		Inspection:     inspection,
	}

	if !*apply {
		result.Status = "dry-run-complete"
		if inspection.Conflicts > 0 {
			result.Status = "dry-run-conflicts"
		}
		result.FinishedAt = time.Now().UTC()
		emit(result, *jsonOutput)
		if inspection.Conflicts > 0 {
			os.Exit(1)
		}
		return
	}

	if inspection.Conflicts > 0 {
		result.Blockers = append(result.Blockers, fmt.Sprintf("存在 %d 个未解决冲突，不能用参数绕过", inspection.Conflicts))
	}
	if *confirmSource != sourceConfirmation {
		result.Blockers = append(result.Blockers, "-confirm-source 必须填 moovie")
	}
	requiredTargetConfirmation := targetConfirmation
	if testTarget {
		requiredTargetConfirmation = targetConfig.Database
	}
	if *confirmTarget != requiredTargetConfirmation {
		result.Blockers = append(result.Blockers, "-confirm-target 必须与实际目标库名一致: "+requiredTargetConfirmation)
	}
	if !*writeFreeze {
		result.Blockers = append(result.Blockers, "缺少 -write-freeze-confirmed")
	}
	if len(result.Blockers) > 0 {
		result.Status = "apply-blocked"
		result.FinishedAt = time.Now().UTC()
		_ = targetTx.Rollback(context.Background())
		emit(result, *jsonOutput)
		os.Exit(1)
	}

	applied, err := importer.Apply(ctx, targetTx)
	if err != nil {
		_ = targetTx.Rollback(context.Background())
		fatalf("迁移写入失败（已回滚，目标库无变更）: %v", err)
	}
	if err := targetTx.Commit(ctx); err != nil {
		fatalf("提交迁移事务失败，结果可能不确定；不要直接重试，请先重新 dry-run 核对目标库: %v", err)
	}
	result.Status = "committed"
	result.ApplyResult = &applied
	result.FinishedAt = time.Now().UTC()
	emit(result, *jsonOutput)
}

func emit(result report, jsonOutput bool) {
	if jsonOutput {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			fatalf("编码 JSON 报告失败: %v", err)
		}
		fmt.Println(string(encoded))
		return
	}
	fmt.Printf("mode=%s status=%s source=%s target=%s schema=%s\n",
		result.Mode, result.Status, result.SourceDatabase, result.TargetDatabase, result.Schema)
	fmt.Printf("%-20s %8s %8s %8s %8s %8s %9s\n", "table", "source", "target", "insert", "update", "skip", "only-new")
	for _, plan := range result.Inspection.Tables {
		if !plan.Available {
			fmt.Printf("%-20s %s\n", plan.Table, plan.Note)
			continue
		}
		fmt.Printf("%-20s %8d %8d %8d %8d %8d %9d\n", plan.Table,
			plan.SourceRows, plan.TargetRows, plan.InsertRows, plan.UpdateRows, plan.SkipRows, plan.TargetOnlyRows)
		if len(plan.ChangedColumns) > 0 {
			fmt.Printf("  将覆盖字段: %s\n", strings.Join(plan.ChangedColumns, ", "))
		}
		if len(plan.MissingKeys) > 0 {
			fmt.Printf("  冲突 自然键缺失: %s\n", strings.Join(plan.MissingKeys, ", "))
		}
		if plan.DuplicateKeys > 0 {
			fmt.Printf("  冲突 旧库自然键重复: %d\n", plan.DuplicateKeys)
		}
		for _, violation := range plan.CheckViolations {
			fmt.Printf("  冲突 CHECK 约束: %s\n", violation)
		}
		for _, mismatch := range plan.TypeMismatches {
			fmt.Printf("  类型对齐: %s\n", mismatch)
		}
		if len(plan.UncheckedConstraints) > 0 {
			fmt.Printf("  提示 无法预检的 CHECK 约束: %s\n", strings.Join(plan.UncheckedConstraints, ", "))
		}
	}
	favorite := result.Inspection.Favorites
	fmt.Printf("favorites: available=%t source=%d insert=%d already=%d missing-users=%d missing-movies=%d\n",
		favorite.Available, favorite.SourceRows, favorite.WouldInsert, favorite.AlreadyExists,
		favorite.MissingUsers, favorite.MissingMovies)
	summary := result.Inspection.Summary
	fmt.Printf("summary: source=%d target=%d insert=%d update=%d skip=%d only-new=%d conflicts=%d\n",
		summary.SourceRows, summary.TargetRows, summary.InsertRows, summary.UpdateRows,
		summary.SkipRows, summary.TargetOnlyRows, result.Inspection.Conflicts)
	for _, blocker := range result.Blockers {
		fmt.Printf("BLOCKED: %s\n", blocker)
	}
	if result.ApplyResult != nil {
		fmt.Printf("committed: insert=%d update=%d favorites=%d canonical=%d sequences=%d total=%d\n",
			result.ApplyResult.TableInserts, result.ApplyResult.TableUpdates, result.ApplyResult.FavoriteInserts,
			result.ApplyResult.CanonicalMutations, result.ApplyResult.SequencesReset, result.ApplyResult.TotalMutations())
	}
	if result.Mode == "dry-run" {
		fmt.Println("dry-run only: 目标库未发生任何写入；新库独有记录会被保留")
	}
}

func schemaName(value string) string {
	if strings.TrimSpace(value) == "" {
		return "public"
	}
	return strings.TrimSpace(value)
}

func modeName(apply bool) string {
	if apply {
		return "apply"
	}
	return "dry-run"
}

func sameDatabase(first, second *pgx.ConnConfig) bool {
	if first == nil || second == nil {
		return false
	}
	return strings.EqualFold(first.Host, second.Host) && first.Port == second.Port &&
		strings.EqualFold(first.Database, second.Database)
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(2)
}
