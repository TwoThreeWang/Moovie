package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	platformconfig "github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/releaseaudit"
	"github.com/jackc/pgx/v5"
)

func main() {
	// releaseaudit 只检查重构目标库的不变量和快照新鲜度，不提供修复或写入选项。
	dsn := flag.String("dsn", strings.TrimSpace(os.Getenv("MIGRATION_TARGET_DSN")), "read-only refactored PostgreSQL DSN")
	targetEnv := flag.String("target-env", "", "local .env file containing refactored database settings (overrides -dsn)")
	targetDatabase := flag.String("target-database", "", "override only the database name from -target-env or -dsn for a rehearsal database")
	requirePopularity := flag.Bool("require-popularity", false, "require fresh four-category snapshots with all configured sources")
	requiredSources := flag.String("required-popularity-sources", "douban,tmdb,activity", "comma-separated popularity sources required at cutover")
	maxPopularityAge := flag.Duration("max-popularity-age", 2*time.Hour, "maximum age of the latest ready popularity snapshot")
	jsonOutput := flag.Bool("json", false, "emit machine-readable JSON")
	flag.Parse()
	if strings.TrimSpace(*targetEnv) != "" {
		database, err := platformconfig.DatabaseConfigFromDotEnv(*targetEnv)
		if err != nil {
			fatalf("read target env file: %v", err)
		}
		*dsn = database.DSN()
	}
	if strings.TrimSpace(*dsn) == "" {
		fatalf("-dsn, -target-env, or MIGRATION_TARGET_DSN is required; this command is read-only and has no apply mode")
	}
	config, err := pgx.ParseConfig(*dsn)
	if err != nil {
		fatalf("parse target DSN: %v", err)
	}
	if databaseName := strings.TrimSpace(*targetDatabase); databaseName != "" {
		config.Database = databaseName
	}
	if strings.EqualFold(config.Database, "moovie") {
		fatalf("refusing to audit legacy database name moovie as the refactored target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		fatalf("connect target: %v", err)
	}
	defer connection.Close(context.Background())
	// 可重复读事务确保同一次审计中的表、约束和计数来自同一个数据库快照。
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		fatalf("begin target read-only transaction: %v", err)
	}
	defer transaction.Rollback(context.Background())
	summary, err := releaseaudit.Audit(ctx, pgxQuerier{transaction: transaction}, releaseaudit.Options{
		RequirePopularity:         *requirePopularity,
		RequiredPopularitySources: strings.Split(*requiredSources, ","),
		MaxPopularityAge:          *maxPopularityAge,
	})
	if err != nil {
		fatalf("audit refactored database: %v", err)
	}
	if *jsonOutput {
		encoded, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(encoded))
	} else {
		for _, result := range summary.Checks {
			fmt.Printf("%-5s %-40s value=%d  %s\n", result.Status, result.Name, result.Value, result.Description)
		}
		fmt.Println("observations:")
		for _, result := range summary.Observations {
			fmt.Printf("  %-38s %d\n", result.Name, result.Value)
		}
		fmt.Printf("summary: failed=%d warnings=%d\n", summary.Failed, summary.Warnings)
	}
	if summary.Failed > 0 {
		os.Exit(1)
	}
}

type pgxQuerier struct {
	transaction pgx.Tx
}

func (querier pgxQuerier) QueryRow(ctx context.Context, query string, arguments ...any) releaseaudit.Row {
	return querier.transaction.QueryRow(ctx, query, arguments...)
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(2)
}
