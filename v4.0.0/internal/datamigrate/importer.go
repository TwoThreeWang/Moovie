package datamigrate

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier 同时满足 pgx.Tx 和 pgx.Conn，便于测试替换。
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (commandTag pgconn.CommandTag, err error)
}

// Importer 在两个只读快照之间比较，并在 apply 时只写目标库。
type Importer struct {
	Schema string
	Source Querier
	Target Querier
	Specs  []TableSpec
}

// row 是一行数据，键为列名。
type row map[string]any

// columns 读取某张表在指定 schema 下的列名集合。表不存在时返回空集。
func columns(ctx context.Context, querier Querier, schema, table string) (map[string]bool, error) {
	rows, err := querier.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func difference(left, right map[string]bool) []string {
	out := make([]string, 0)
	for key := range left {
		if !right[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// quote 用双引号包裹标识符，防止列名与保留字冲突。
func quote(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for index, name := range names {
		out[index] = quote(name)
	}
	return out
}

// fetch 读取整张表的指定列，按自然键建索引。
// 业务表规模在万行量级，一次性载入换来简单且可验证的比较逻辑。
func fetch(ctx context.Context, querier Querier, schema, table string, cols, exprs, keys []string) (map[string]row, int, int, error) {
	statement := fmt.Sprintf("SELECT %s FROM %s.%s", strings.Join(exprs, ", "), quote(schema), quote(table))
	rows, err := querier.Query(ctx, statement)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	indexed := make(map[string]row)
	total, duplicates := 0, 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, 0, 0, err
		}
		current := make(row, len(cols))
		for index, name := range cols {
			current[name] = values[index]
		}
		total++
		key := naturalKey(current, keys)
		if _, exists := indexed[key]; exists {
			duplicates++
			continue
		}
		indexed[key] = current
	}
	return indexed, total, duplicates, rows.Err()
}

// naturalKey 把自然键各列拼成可比较的字符串。使用 \x00 分隔，
// 避免 ("a","bc") 与 ("ab","c") 折叠成同一个键。
func naturalKey(current row, keys []string) string {
	parts := make([]string, len(keys))
	for index, key := range keys {
		parts[index] = normalize(current[key])
	}
	return strings.Join(parts, "\x00")
}

// normalize 把值归一化成可比较的字符串。
// 数值统一走十进制表示，避免 int32/int64 或 numeric/float 造成假差异。
func normalize(value any) string {
	switch typed := value.(type) {
	case nil:
		return "\x01NULL"
	case int8:
		return strconv.FormatInt(int64(typed), 10)
	case int16:
		return strconv.FormatInt(int64(typed), 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.FormatInt(int64(typed), 10)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 64)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case string:
		return typed
	case []byte:
		return string(typed)
	case time.Time:
		// 统一到 UTC 并去掉单调时钟，避免连接时区或精度差异造成假差异。
		return typed.UTC().Format(time.RFC3339Nano)
	default:
		// pgtype.Numeric 等类型没有稳定的 %v 形式，但两侧已按目标类型
		// 对齐（见 coltypes.go），同一个值会产生一致的表示。
		return fmt.Sprintf("%v", typed)
	}
}

// Inspect 生成完整的只读迁移计划，不写入任何数据。
func (importer Importer) Inspect(ctx context.Context) (Inspection, error) {
	schema := importer.schemaName()
	inspection := Inspection{Tables: make([]TablePlan, 0, len(importer.Specs))}

	for _, spec := range importer.Specs {
		plan, _, _, err := importer.planTable(ctx, schema, spec)
		if err != nil {
			return Inspection{}, fmt.Errorf("inspect %s: %w", spec.Table, err)
		}
		inspection.Tables = append(inspection.Tables, plan)
		inspection.Summary.SourceRows += plan.SourceRows
		inspection.Summary.TargetRows += plan.TargetRows
		inspection.Summary.InsertRows += plan.InsertRows
		inspection.Summary.UpdateRows += plan.UpdateRows
		inspection.Summary.SkipRows += plan.SkipRows
		inspection.Summary.TargetOnlyRows += plan.TargetOnlyRows
		inspection.Conflicts += plan.ConflictCount()
	}
	canonicalPlans, err := importer.inspectCanonical(ctx, schema)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect canonical data: %w", err)
	}
	for _, plan := range canonicalPlans {
		inspection.Tables = append(inspection.Tables, plan)
		inspection.Summary.SourceRows += plan.SourceRows
		inspection.Summary.TargetRows += plan.TargetRows
		inspection.Summary.InsertRows += plan.InsertRows
		inspection.Summary.UpdateRows += plan.UpdateRows
		inspection.Summary.SkipRows += plan.SkipRows
		inspection.Summary.TargetOnlyRows += plan.TargetOnlyRows
		inspection.Conflicts += plan.ConflictCount()
	}

	favorites, err := importer.planFavorites(ctx, schema)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspect favorites: %w", err)
	}
	inspection.Favorites = favorites
	inspection.Conflicts += favorites.ConflictCount()
	return inspection, nil
}

func (importer Importer) schemaName() string {
	schema := strings.TrimSpace(importer.Schema)
	if schema == "" {
		return "public"
	}
	return schema
}

// planTable 计算单表计划，同时返回待写入的行，供 apply 复用。
func (importer Importer) planTable(ctx context.Context, schema string, spec TableSpec) (TablePlan, []row, map[string]row, error) {
	plan := TablePlan{Table: spec.Table, Keys: spec.Keys}

	sourceCols, err := columns(ctx, importer.Source, schema, spec.Table)
	if err != nil {
		return plan, nil, nil, err
	}
	targetCols, err := columns(ctx, importer.Target, schema, spec.Table)
	if err != nil {
		return plan, nil, nil, err
	}
	if len(sourceCols) == 0 || len(targetCols) == 0 {
		plan.Note = "表在旧库或新库不存在，已跳过"
		return plan, nil, nil, nil
	}
	plan.Available = true

	// 自然键必须两侧都有，否则无法安全对齐。
	for _, key := range spec.Keys {
		if !sourceCols[key] || !targetCols[key] {
			plan.MissingKeys = append(plan.MissingKeys, key)
		}
	}
	if len(plan.MissingKeys) > 0 {
		return plan, nil, nil, nil
	}

	immutable := make(map[string]bool, len(spec.Immutable))
	for _, name := range spec.Immutable {
		immutable[name] = true
	}
	shared := make(map[string]bool)
	for name := range sourceCols {
		if targetCols[name] && !immutable[name] {
			shared[name] = true
		}
	}
	copied := sortedKeys(shared)
	plan.CopiedColumns = copied
	plan.SourceOnlyCols = difference(sourceCols, targetCols)
	plan.TargetOnlyCols = difference(targetCols, sourceCols)

	// 先对齐两边的列类型再取数。numeric/timestamp 的精度差异会让同一个值
	// 在两侧返回不同的内部表示（如 numeric 的 Int/Exp），不转换就会产生
	// 永远收敛不了的假 update。
	sourceTypes, err := columnTypes(ctx, importer.Source, schema, spec.Table)
	if err != nil {
		return plan, nil, nil, err
	}
	targetTypes, err := columnTypes(ctx, importer.Target, schema, spec.Table)
	if err != nil {
		return plan, nil, nil, err
	}
	plan.TypeMismatches = typeMismatches(copied, sourceTypes, targetTypes)

	sourceExprs := make([]string, len(copied))
	targetExprs := make([]string, len(copied))
	for index, name := range copied {
		sourceExprs[index] = selectExpression(name, sourceTypes[name], targetTypes[name])
		targetExprs[index] = quote(name)
	}

	sourceRows, sourceTotal, sourceDuplicates, err := fetch(ctx, importer.Source, schema, spec.Table, copied, sourceExprs, spec.Keys)
	if err != nil {
		return plan, nil, nil, err
	}
	targetRows, targetTotal, _, err := fetch(ctx, importer.Target, schema, spec.Table, copied, targetExprs, spec.Keys)
	if err != nil {
		return plan, nil, nil, err
	}
	plan.SourceRows, plan.TargetRows = sourceTotal, targetTotal
	plan.DuplicateKeys = sourceDuplicates

	// 预检目标库的 CHECK 约束。旧库允许的枚举值可能不在新库允许集合里
	// （例如 feedbacks.type），不提前发现就只能等 apply 时整体回滚。
	constraints, unchecked, err := enumConstraints(ctx, importer.Target, schema, spec.Table)
	if err != nil {
		return plan, nil, nil, err
	}
	plan.UncheckedConstraints = unchecked
	violations, err := checkEnumViolations(ctx, importer.Source, schema, spec.Table, constraints, sourceCols)
	if err != nil {
		return plan, nil, nil, err
	}
	plan.CheckViolations = violations

	pending := make([]row, 0)
	changed := make(map[string]bool)
	for key, current := range sourceRows {
		existing, exists := targetRows[key]
		if !exists {
			plan.InsertRows++
			pending = append(pending, current)
			continue
		}
		diff := changedColumns(current, existing, copied, spec.Keys)
		if len(diff) == 0 {
			plan.SkipRows++
			continue
		}
		plan.UpdateRows++
		pending = append(pending, current)
		for _, name := range diff {
			changed[name] = true
		}
	}
	for key := range targetRows {
		if _, exists := sourceRows[key]; !exists {
			plan.TargetOnlyRows++
		}
	}
	plan.ChangedColumns = sortedKeys(changed)
	return plan, pending, targetRows, nil
}

// changedColumns 返回会被旧库覆盖的列。
// 旧库为 NULL 而新库有值时保留新库值，不用 NULL 清空既有数据。
func changedColumns(source, target row, cols, keys []string) []string {
	keySet := make(map[string]bool, len(keys))
	for _, key := range keys {
		keySet[key] = true
	}
	diff := make([]string, 0)
	for _, name := range cols {
		if keySet[name] {
			continue
		}
		if source[name] == nil {
			continue
		}
		if normalize(source[name]) != normalize(target[name]) {
			diff = append(diff, name)
		}
	}
	return diff
}
