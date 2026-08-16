package datamigrate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// 数值类型在 pgx 里可能以 int32/int64/float32/float64 返回，
// 归一化必须消除这种差异，否则每次 dry-run 都会报出不存在的 update。
func TestNormalizeCollapsesNumericWidths(t *testing.T) {
	for _, pair := range [][2]any{
		{int32(7), int64(7)},
		{int(7), int16(7)},
		{float32(1.5), float64(1.5)},
		{int64(0), int32(0)},
	} {
		if normalize(pair[0]) != normalize(pair[1]) {
			t.Fatalf("normalize(%#v)=%q 与 normalize(%#v)=%q 不一致", pair[0], normalize(pair[0]), pair[1], normalize(pair[1]))
		}
	}
	if normalize(nil) == normalize("") {
		t.Fatal("NULL 不能与空字符串折叠成同一个值")
	}
	if normalize([]byte("abc")) != normalize("abc") {
		t.Fatal("[]byte 应与等价字符串归一化一致")
	}
}

// 自然键必须无歧义：("a","bc") 和 ("ab","c") 不能折叠成同一个键，
// 否则会把两行不同数据当成同一行互相覆盖。
func TestNaturalKeyIsUnambiguous(t *testing.T) {
	first := naturalKey(row{"x": "a", "y": "bc"}, []string{"x", "y"})
	second := naturalKey(row{"x": "ab", "y": "c"}, []string{"x", "y"})
	if first == second {
		t.Fatalf("自然键发生折叠: %q", first)
	}
	if naturalKey(row{"x": int32(1)}, []string{"x"}) != naturalKey(row{"x": int64(1)}, []string{"x"}) {
		t.Fatal("同值不同宽度的整数应产生相同自然键")
	}
}

// 旧库为 NULL 时保留新库已有值，不用 NULL 清空；自然键列本身永不参与覆盖。
func TestChangedColumnsPreservesTargetValueWhenSourceIsNull(t *testing.T) {
	source := row{"email": "a@example.com", "nickname": nil, "avatar": "new.png", "same": "x"}
	target := row{"email": "a@example.com", "nickname": "已有昵称", "avatar": "old.png", "same": "x"}
	cols := []string{"avatar", "email", "nickname", "same"}

	diff := changedColumns(source, target, cols, []string{"email"})
	if !reflect.DeepEqual(diff, []string{"avatar"}) {
		t.Fatalf("changedColumns = %v，应仅包含 avatar", diff)
	}
}

// 复制列必须是两侧交集：新库独有列不能被旧库写空，旧库独有列直接忽略。
func TestDifferenceSplitsSourceAndTargetOnlyColumns(t *testing.T) {
	source := map[string]bool{"id": true, "title": true, "legacy_only": true}
	target := map[string]bool{"id": true, "title": true, "media_unit_id": true}

	if got := difference(source, target); !reflect.DeepEqual(got, []string{"legacy_only"}) {
		t.Fatalf("仅旧库列 = %v", got)
	}
	if got := difference(target, source); !reflect.DeepEqual(got, []string{"media_unit_id"}) {
		t.Fatalf("仅新库列 = %v", got)
	}
}

// 标识符必须被引号包裹并转义，避免列名撞上保留字或注入。
func TestQuoteEscapesIdentifiers(t *testing.T) {
	if quote("year") != `"year"` {
		t.Fatalf("quote = %s", quote("year"))
	}
	if quote(`a"b`) != `"a""b"` {
		t.Fatalf("quote 未转义内嵌双引号: %s", quote(`a"b`))
	}
}

// 白名单只覆盖旧库业务数据；media 等最终表由回填推导，任务记录则不跨系统迁移。
func TestDefaultTablesExcludeDerivedAndOperationalTables(t *testing.T) {
	excluded := map[string]bool{
		"media": true, "media_units": true, "media_aliases": true,
		"media_external_ids": true, "resource_media_links": true,
		"douban_sync_jobs": true, "worker_jobs": true,
	}
	seen := make(map[string]bool)
	for _, spec := range DefaultTables {
		if excluded[spec.Table] {
			t.Fatalf("白名单不应直接包含派生或任务表 %s", spec.Table)
		}
		if seen[spec.Table] {
			t.Fatalf("白名单存在重复表 %s", spec.Table)
		}
		seen[spec.Table] = true
		if len(spec.Keys) == 0 {
			t.Fatalf("表 %s 缺少自然键", spec.Table)
		}
	}
}

func TestTablesExceptRemovesOnlyNamedTables(t *testing.T) {
	kept := TablesExcept([]string{"vod_items", "search_logs"})
	if len(kept) != len(DefaultTables)-2 {
		t.Fatalf("kept = %d, want %d", len(kept), len(DefaultTables)-2)
	}
	for _, spec := range kept {
		if spec.Table == "vod_items" || spec.Table == "search_logs" {
			t.Fatalf("%s 仍在清单中", spec.Table)
		}
	}
	if len(TablesExcept(nil)) != len(DefaultTables) {
		t.Fatal("空排除列表必须返回完整清单")
	}
	if len(TablesExcept([]string{"not_a_table"})) != len(DefaultTables) {
		t.Fatal("未知表名不应删除任何条目")
	}
}

// 假 Querier，只记录最后一次 Exec 的语句和参数。
type recordingQuerier struct {
	statement string
	values    []any
}

func (q *recordingQuerier) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("not used")
}
func (q *recordingQuerier) QueryRow(context.Context, string, ...any) pgx.Row { return nil }
func (q *recordingQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	q.statement, q.values = sql, args
	return pgconn.CommandTag{}, nil
}

// 旧库可空、新库 NOT NULL DEFAULT 的列（如 users.douban_user_id）必须被跳过，
// 让目标库默认值生效；原样插入 NULL 会违反非空约束导致整个迁移事务回滚。
func TestInsertRowOmitsNullColumnsSoTargetDefaultsApply(t *testing.T) {
	querier := &recordingQuerier{}
	current := row{"email": "a@example.com", "douban_user_id": nil, "nickname": "阿三"}
	cols := []string{"douban_user_id", "email", "nickname"}

	if err := insertRow(t.Context(), querier, "public", "users", current, cols); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(querier.statement, "douban_user_id") {
		t.Fatalf("NULL 列不应出现在 INSERT 中: %s", querier.statement)
	}
	for _, want := range []string{`"email"`, `"nickname"`, "$1", "$2"} {
		if !strings.Contains(querier.statement, want) {
			t.Fatalf("语句缺少 %q: %s", want, querier.statement)
		}
	}
	if len(querier.values) != 2 {
		t.Fatalf("参数个数 = %d，应为 2", len(querier.values))
	}
}

// 最终删表 migration 只接受迁移工具写入的完成标记，标记本身不得包含凭据或动态内容。
func TestRecordCanonicalCutoverReadyWritesFixedMarker(t *testing.T) {
	querier := &recordingQuerier{}
	if err := recordCanonicalCutoverReady(t.Context(), querier, "public"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(querier.statement, `"public".schema_migrations`) ||
		!reflect.DeepEqual(querier.values, []any{canonicalCutoverMarker}) {
		t.Fatalf("marker statement/values = %s / %#v", querier.statement, querier.values)
	}
}
