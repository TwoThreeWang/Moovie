// Package testdb 仅连接显式指定的独立测试数据库。
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
)

// 整个测试进程共用一个连接池，只初始化一次。
var (
	once     sync.Once
	shared   *database.Pool
	setErr   error
	lastTest string

	// truncations 记录每次清表是由哪个测试触发的，用于排查"数据被谁清掉了"。
	truncations []string
)

// Truncations 返回本进程内的清表历史，按发生顺序。
func Truncations() []string { return truncations }

// Pool 返回测试库连接，并在每个顶层测试开始时清空业务表。
//
// 必须逐个测试清：真库有唯一索引和外键，上一个用例留下的 sites、users 会让
// 下一个用例撞 duplicate key。同一个测试内多次调用只清一次（不少测试要同时
// 构造多个模块的 Store），子测试沿用父测试的数据。
func Pool(t *testing.T) *database.Pool {
	t.Helper()
	once.Do(func() { shared, setErr = open() })
	if setErr != nil {
		t.Fatalf("连接测试库失败: %v", setErr)
	}
	root, _, _ := strings.Cut(t.Name(), "/")
	if root != lastTest {
		lastTest = root
		truncations = append(truncations, root)
		if err := truncate(t.Context(), shared); err != nil {
			t.Fatalf("清空测试库失败: %v", err)
		}
	}
	return shared
}

// User 插入指定 ID 的用户，供 user_movies、playback_positions、monthly_reports、
// danmakus 等带 user_id 外键的表使用。
func User(t *testing.T, pool *database.Pool, ids ...int) {
	t.Helper()
	for _, id := range ids {
		if _, err := pool.Exec(t.Context(), `INSERT INTO users (id,email,username,password_hash)
VALUES ($1,$2,$3,'x') ON CONFLICT (id) DO NOTHING`, id, fmt.Sprintf("u%d@test.local", id), fmt.Sprintf("u%d", id)); err != nil {
			t.Fatalf("插入测试用户 %d: %v", id, err)
		}
		// 写后校验：ON CONFLICT DO NOTHING 可能静默跳过，外键失败时能立刻定位到这里。
		var exists bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists); err != nil {
			t.Fatalf("校验测试用户 %d: %v", id, err)
		}
		if !exists {
			t.Fatalf("测试用户 %d 播种后不存在", id)
		}
	}
}

// Media 插入指定 ID 的媒体，供 playback_positions、
// resource_match_candidates 等带 media_id 外键的表使用。
func Media(t *testing.T, pool *database.Pool, ids ...int) {
	t.Helper()
	for _, id := range ids {
		// title 必须留空：读取路径是 COALESCE(NULLIF(media.title,''), position.title)，
		// 给占位标题会顶掉测试自己写入的标题，让断言拿到错误的值。
		if _, err := pool.Exec(t.Context(), `INSERT INTO media (id,media_type)
VALUES ($1,'movie') ON CONFLICT (id) DO NOTHING`, id); err != nil {
			t.Fatalf("插入测试媒体 %d: %v", id, err)
		}
	}
}

// MediaUnit 插入指定 ID 的媒体单元（会一并保证其 media 存在），供
// playback_positions.media_unit_id 等外键使用。
func MediaUnit(t *testing.T, pool *database.Pool, unitID, mediaID int) {
	t.Helper()
	Media(t, pool, mediaID)
	if _, err := pool.Exec(t.Context(), `INSERT INTO media_units (id,media_id,unit_type,episode_key)
VALUES ($1,$2,'feature','feature') ON CONFLICT (id) DO NOTHING`, unitID, mediaID); err != nil {
		t.Fatalf("插入测试媒体单元 %d: %v", unitID, err)
	}
}

// truncate 清空除 schema_migrations 外的所有表。
func truncate(ctx context.Context, pool *database.Pool) error {
	_, err := pool.Exec(ctx, `DO $$ DECLARE tables text; BEGIN
SELECT string_agg(quote_ident(tablename),',') INTO tables FROM pg_tables
WHERE schemaname='public' AND tablename<>'schema_migrations';
IF tables IS NOT NULL THEN EXECUTE 'TRUNCATE '||tables||' RESTART IDENTITY CASCADE'; END IF;
END $$`)
	return err
}

// open 只允许显式指定测试库，禁止测试的 TRUNCATE 误清开发或生产数据。
func open() (*database.Pool, error) {
	dsn := os.Getenv("MOOVIE_TEST_DATABASE_URL")
	if dsn == "" {
		return nil, fmt.Errorf("请设置 MOOVIE_TEST_DATABASE_URL，库名必须以 moovie_test_ 开头")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || !strings.HasPrefix(strings.TrimPrefix(parsed.Path, "/"), "moovie_test_") {
		return nil, fmt.Errorf("拒绝连接非独立测试库")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := database.Connect(ctx, dsn, 8)
	if err != nil {
		return nil, err
	}
	if err := database.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
