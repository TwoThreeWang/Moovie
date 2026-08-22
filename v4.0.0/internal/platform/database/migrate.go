package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockID 是执行迁移时使用的数据库咨询锁 ID，保证多实例同时启动也只有一个在迁移。
const migrationLockID int64 = 746786689410227

// Migrate 在单个事务中按版本顺序执行尚未应用的 SQL。
// PostgreSQL advisory transaction lock 保证多个 Web/Worker 同时启动时只有一个实例迁移。
func Migrate(ctx context.Context, database Beginner) error {
	return migrateThrough(ctx, database, "")
}

// MigrateThrough 用于数据迁移脚本分两段执行结构迁移：先准备最终表，
// 待数据复验通过后再删除过渡表。through 为空时与 Migrate 完全相同。
func MigrateThrough(ctx context.Context, database Beginner, through string) error {
	return migrateThrough(ctx, database, strings.TrimSpace(through))
}

// migrateThrough 只执行到指定版本为止，测试用来验证某个迁移前后的状态。
func migrateThrough(ctx context.Context, database Beginner, through string) error {
	migrations, err := loadMigrations(migrationFiles)
	if err != nil {
		return err
	}
	if through != "" {
		found := false
		for index, migration := range migrations {
			if migration.version == through {
				migrations = migrations[:index+1]
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("migration version %s does not exist", through)
		}
	}
	transaction, err := database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer transaction.Rollback(context.WithoutCancel(ctx))

	if _, err := transaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err := transaction.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT NOW())`); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	for _, migration := range migrations {
		var applied bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, migration.version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", migration.version, err)
		}
		if applied {
			continue
		}
		if _, err := transaction.Exec(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.version, err)
		}
		if _, err := transaction.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, migration.version); err != nil {
			return fmt.Errorf("record migration %s: %w", migration.version, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

// migration 是一条待执行的迁移：version 取自文件名前缀（如 0001），sql 是文件全文。
type migration struct {
	version string
	sql     string
}

// loadMigrations 读出内嵌的迁移文件并校验：版本号不能重复、内容不能为空。
func loadMigrations(files fs.FS) ([]migration, error) {
	// 先按文件路径排序，再校验版本唯一和内容非空，使不同机器得到完全相同的执行顺序。
	paths, err := fs.Glob(files, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(paths)
	migrations := make([]migration, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		version := strings.SplitN(filepath.Base(path), "_", 2)[0]
		if version == "" {
			return nil, fmt.Errorf("migration %s has no version", path)
		}
		if _, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("duplicate migration version %s", version)
		}
		contents, err := fs.ReadFile(files, path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", path, err)
		}
		if strings.TrimSpace(string(contents)) == "" {
			return nil, fmt.Errorf("migration %s is empty", path)
		}
		seen[version] = struct{}{}
		migrations = append(migrations, migration{version: version, sql: string(contents)})
	}
	if len(migrations) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	return migrations, nil
}
