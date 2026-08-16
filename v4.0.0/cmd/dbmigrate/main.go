// dbmigrate 只负责把新库结构升级到当前代码所需的最终版本。
// 数据迁移脚本调用它，使用户不需要先手工启动 Web 进程建表。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	platformconfig "github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

func main() {
	targetDSN := flag.String("target", strings.TrimSpace(os.Getenv("MIGRATION_TARGET_DSN")), "新 PostgreSQL DSN")
	targetEnv := flag.String("target-env", "", "包含新库配置的 .env；设置后覆盖 -target")
	through := flag.String("through", "", "只执行到指定版本；留空表示执行全部版本")
	timeout := flag.Duration("timeout", 10*time.Minute, "结构迁移超时")
	flag.Parse()

	if strings.TrimSpace(*targetEnv) != "" {
		cfg, err := platformconfig.DatabaseConfigFromDotEnv(*targetEnv)
		if err != nil {
			fatalf("读取目标库 env 失败: %v", err)
		}
		*targetDSN = cfg.DSN()
	}
	if strings.TrimSpace(*targetDSN) == "" {
		fatalf("必须提供 -target-env、-target 或 MIGRATION_TARGET_DSN")
	}
	parsed, err := pgx.ParseConfig(*targetDSN)
	if err != nil {
		fatalf("解析目标库 DSN 失败: %v", err)
	}
	if strings.EqualFold(parsed.Database, "moovie") {
		fatalf("拒绝对旧库 moovie 执行新系统结构迁移")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	pool, err := database.Connect(ctx, *targetDSN, 1)
	if err != nil {
		fatalf("连接目标库失败: %v", err)
	}
	defer pool.Close()
	if err := database.MigrateThrough(ctx, pool, *through); err != nil {
		fatalf("升级目标库结构失败（当前版本事务已回滚）: %v", err)
	}
	version := "最新版本"
	if strings.TrimSpace(*through) != "" {
		version = strings.TrimSpace(*through)
	}
	fmt.Printf("目标库 %s 结构已升级到 %s\n", parsed.Database, version)
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}
