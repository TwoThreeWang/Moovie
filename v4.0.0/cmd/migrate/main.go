// migrate 把旧库 moovie 的数据一次性迁移到新库 moovie_v2。
//
// 一个进程做完全部工作：升级新库结构、用 COPY 协议流式搬运数据、转换 favorites、
// 回填 canonical 新结构、重置序列、完成最终结构。不需要 shell 脚本，不需要 jq，
// 数据不经过 Go 的内存，因此几百万行也不会 OOM。
//
// 旧库连接始终是只读事务，本程序没有任何写旧库的代码路径。
// 新库的全部写入在一个事务里，任一步失败整体回滚，重跑不会留下半份数据。
//
//	migrate --source-env ../.env --target-env ./.env             # 只读预检
//	migrate --source-env ../.env --target-env ./.env --apply     # 执行迁移
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/datamigrate"
	platformconfig "github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

const (
	sourceDatabaseName = "moovie"
	targetDatabaseName = "moovie_v2"
)

func main() {
	sourceEnv := flag.String("source-env", "../.env", "旧库 .env 路径")
	targetEnv := flag.String("target-env", "./.env", "新库 .env 路径")
	apply := flag.Bool("apply", false, "执行迁移；默认只做只读预检")
	schema := flag.String("schema", "public", "要迁移的 schema")
	timeout := flag.Duration("timeout", 4*time.Hour, "整体超时")
	flag.Parse()

	sourceDSN := dsnFromEnv(*sourceEnv, "旧库")
	targetDSN := dsnFromEnv(*targetEnv, "新库")

	sourceConfig, err := pgx.ParseConfig(sourceDSN)
	if err != nil {
		fatalf("解析旧库 DSN 失败: %v", err)
	}
	targetConfig, err := pgx.ParseConfig(targetDSN)
	if err != nil {
		fatalf("解析新库 DSN 失败: %v", err)
	}
	if strings.EqualFold(sourceConfig.Database, targetConfig.Database) &&
		strings.EqualFold(sourceConfig.Host, targetConfig.Host) && sourceConfig.Port == targetConfig.Port {
		fatalf("旧库和新库指向同一个数据库，已拒绝继续")
	}
	if !strings.EqualFold(sourceConfig.Database, sourceDatabaseName) {
		fatalf("旧库必须是 %s，当前是 %s", sourceDatabaseName, sourceConfig.Database)
	}
	if !strings.EqualFold(targetConfig.Database, targetDatabaseName) {
		fatalf("新库必须是 %s，当前是 %s", targetDatabaseName, targetConfig.Database)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// 结构必须先到位，dry-run 也要读新库的表来对齐列。0030 之后的版本会删除
	// 过渡表，那些表正是数据迁移的中间结构，所以现在只升到 0030。
	step("升级新库结构到 0030")
	pool, err := database.Connect(ctx, targetDSN, 1)
	if err != nil {
		fatalf("连接新库失败: %v", err)
	}
	if err := database.MigrateThrough(ctx, pool, "0030"); err != nil {
		pool.Close()
		fatalf("升级新库结构失败（已回滚）: %v", err)
	}
	pool.Close()

	source, err := pgx.ConnectConfig(ctx, sourceConfig)
	if err != nil {
		fatalf("连接旧库失败: %v", err)
	}
	defer source.Close(context.Background())
	target, err := pgx.ConnectConfig(ctx, targetConfig)
	if err != nil {
		fatalf("连接新库失败: %v", err)
	}
	defer target.Close(context.Background())

	// 旧库全程只读，由 PostgreSQL 服务端强制；快照保证整个迁移看到同一份数据。
	sourceTx, err := source.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		fatalf("创建旧库只读事务失败: %v", err)
	}
	defer sourceTx.Rollback(context.Background())

	step("统计旧库数据量")
	total := int64(0)
	names := make([]string, 0, len(datamigrate.DefaultTables))
	for _, spec := range datamigrate.DefaultTables {
		names = append(names, spec.Table)
	}
	sort.Strings(names)
	for _, table := range names {
		var rows int64
		statement := fmt.Sprintf("SELECT count(*) FROM %s.%s", quoteIdent(*schema), quoteIdent(table))
		if err := sourceTx.QueryRow(ctx, statement).Scan(&rows); err != nil {
			fmt.Printf("  %-20s 旧库没有这张表，跳过\n", table)
			continue
		}
		total += rows
		if rows > 0 {
			fmt.Printf("  %-20s %10d\n", table, rows)
		}
	}
	fmt.Printf("  %-20s %10d\n", "合计", total)

	if !*apply {
		step("预检完成，未写入任何数据")
		fmt.Println("  确认无误后加 --apply 执行迁移")
		return
	}

	targetTx, err := target.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadWrite})
	if err != nil {
		fatalf("创建新库事务失败: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = targetTx.Rollback(context.Background())
		}
	}()

	step("流式搬运数据（COPY 协议，内存恒定）")
	started := time.Now()
	result, err := datamigrate.StreamImport(ctx, source, target, sourceTx, targetTx, *schema,
		datamigrate.DefaultTables, func(table string, rows int64) {
			if rows > 0 {
				fmt.Printf("  %-20s %10d\n", table, rows)
			}
		})
	if err != nil {
		fatalf("迁移失败（新库事务已回滚，无任何变更）: %v", err)
	}

	if err := targetTx.Commit(ctx); err != nil {
		fatalf("提交失败，结果不确定；不要直接重试，请先清空新库: %v", err)
	}
	committed = true
	fmt.Printf("  搬运 %d 行 favorites=%d 回填=%d 序列重置=%d 用时 %s\n",
		result.CopiedRows, result.FavoriteInserts, result.CanonicalMutations,
		result.SequencesReset, time.Since(started).Round(time.Second))

	step("完成最终结构（删除过渡表）")
	finalPool, err := database.Connect(ctx, targetDSN, 1)
	if err != nil {
		fatalf("连接新库失败: %v", err)
	}
	defer finalPool.Close()
	if err := database.MigrateThrough(ctx, finalPool, ""); err != nil {
		fatalf("完成最终结构失败: %v", err)
	}

	step("迁移完成")
}

func dsnFromEnv(path, label string) string {
	cfg, err := platformconfig.DatabaseConfigFromDotEnv(path)
	if err != nil {
		fatalf("读取%s env 失败 (%s): %v", label, path, err)
	}
	return cfg.DSN()
}

func quoteIdent(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func step(message string) {
	fmt.Printf("\n==> %s\n", message)
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "\nERROR "+format+"\n", arguments...)
	os.Exit(1)
}
