// Package database 封装 PostgreSQL 连接池与数据库迁移。
// 这里把 pgx 的类型收敛成 Row/Rows/Executor 三个小接口，业务层的 Store 只依赖这些接口，
// 因此单元测试可以替换实现，业务代码也不会散落 pgx 的具体类型。
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Row 抽象单行查询结果，使业务 Store 不直接依赖 pgx 的具体类型。
type Row interface {
	Scan(destinations ...any) error
}

// Rows 抽象多行查询游标；调用方读取结束后必须 Close，并检查 Err。
type Rows interface {
	Next() bool
	Scan(destinations ...any) error
	Err() error
	Close()
}

// Executor 是连接池和事务共同实现的最小 SQL 执行接口，便于业务代码在事务内外复用。
type Executor interface {
	Query(ctx context.Context, query string, arguments ...any) (Rows, error)
	QueryRow(ctx context.Context, query string, arguments ...any) Row
	Exec(ctx context.Context, query string, arguments ...any) (int64, error)
}

// Transaction 在 Executor 基础上增加提交和回滚能力。
type Transaction interface {
	Executor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Beginner 表示可以开启事务的数据库对象，migration 和数据导入只依赖此窄接口。
type Beginner interface {
	Begin(ctx context.Context) (Transaction, error)
}

// Pool 包装 pgxpool，并对业务层暴露项目统一的数据库接口。
type Pool struct {
	pool *pgxpool.Pool
}

// Connect 创建有界 PostgreSQL 连接池并立即 Ping。
// maximumConnections 未提供时使用 15；生产入口通常会明确传入 DB_MAX_CONNS。
func Connect(ctx context.Context, dsn string, maximumConnections ...int) (*Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	maxConns := 15
	if len(maximumConnections) > 0 && maximumConnections[0] > 0 {
		maxConns = maximumConnections[0]
	}
	poolConfig.MaxConns = int32(maxConns)
	poolConfig.MinConns = 0
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = 15 * time.Minute
	poolConfig.HealthCheckPeriod = time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Pool{pool: pool}, nil
}

// Ping 探活，供 /ready 就绪探针使用。
func (pool *Pool) Ping(ctx context.Context) error { return pool.pool.Ping(ctx) }

// Close 关闭连接池。
func (pool *Pool) Close() { pool.pool.Close() }

// Query 执行多行查询；调用方必须 Close 返回的 Rows。
func (pool *Pool) Query(ctx context.Context, query string, arguments ...any) (Rows, error) {
	return pool.pool.Query(ctx, query, arguments...)
}

// QueryRow 执行单行查询，错误会在 Scan 时返回。
func (pool *Pool) QueryRow(ctx context.Context, query string, arguments ...any) Row {
	return pool.pool.QueryRow(ctx, query, arguments...)
}

// Exec 执行写操作并返回影响行数。
func (pool *Pool) Exec(ctx context.Context, query string, arguments ...any) (int64, error) {
	tag, err := pool.pool.Exec(ctx, query, arguments...)
	return tag.RowsAffected(), err
}

// Begin 开启事务，返回的 Transaction 与 Pool 用同一套接口，业务代码在事务内外可以复用。
func (pool *Pool) Begin(ctx context.Context) (Transaction, error) {
	transaction, err := pool.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTransaction{transaction: transaction}, nil
}

// pgxTransaction 把 pgx.Tx 适配成本包的 Transaction 接口。
type pgxTransaction struct {
	transaction pgx.Tx
}

// Query 在事务里执行查询。
func (transaction pgxTransaction) Query(ctx context.Context, query string, arguments ...any) (Rows, error) {
	return transaction.transaction.Query(ctx, query, arguments...)
}

// QueryRow 在事务里查一行。
func (transaction pgxTransaction) QueryRow(ctx context.Context, query string, arguments ...any) Row {
	return transaction.transaction.QueryRow(ctx, query, arguments...)
}

// Exec 在事务里执行写操作，返回影响行数。
func (transaction pgxTransaction) Exec(ctx context.Context, query string, arguments ...any) (int64, error) {
	tag, err := transaction.transaction.Exec(ctx, query, arguments...)
	return tag.RowsAffected(), err
}

// Commit 提交事务。
func (transaction pgxTransaction) Commit(ctx context.Context) error {
	return transaction.transaction.Commit(ctx)
}

// Rollback 回滚事务，已提交时返回的错误可以忽略。
func (transaction pgxTransaction) Rollback(ctx context.Context) error {
	return transaction.transaction.Rollback(ctx)
}
