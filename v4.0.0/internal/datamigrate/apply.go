package datamigrate

import (
	"context"
	"fmt"
	"strings"
)

const canonicalCutoverMarker = "data-canonical-cutover-ready"

// Apply 在调用方提供的目标库事务内执行全部写入。
// 任一步返回错误时调用方必须回滚；本函数自身不提交也不回滚。
func (importer Importer) Apply(ctx context.Context, target Querier) (ApplyResult, error) {
	schema := importer.schemaName()
	result := ApplyResult{}

	for _, spec := range importer.Specs {
		plan, pending, existing, err := importer.planTable(ctx, schema, spec)
		if err != nil {
			return result, fmt.Errorf("plan %s: %w", spec.Table, err)
		}
		if !plan.Available || plan.ConflictCount() > 0 {
			continue
		}
		for _, current := range pending {
			key := naturalKey(current, spec.Keys)
			if _, exists := existing[key]; exists {
				if err := updateRow(ctx, target, schema, spec, current, plan.CopiedColumns); err != nil {
					return result, fmt.Errorf("update %s: %w", spec.Table, err)
				}
				result.TableUpdates++
				continue
			}
			if err := insertRow(ctx, target, schema, spec.Table, current, plan.CopiedColumns); err != nil {
				return result, fmt.Errorf("insert %s: %w", spec.Table, err)
			}
			result.TableInserts++
		}
	}

	favorites, err := importer.applyFavorites(ctx, target, schema)
	if err != nil {
		return result, fmt.Errorf("convert favorites: %w", err)
	}
	result.FavoriteInserts = favorites

	canonical, err := importer.CanonicalBackfill(ctx, target, schema)
	if err != nil {
		return result, fmt.Errorf("canonical backfill: %w", err)
	}
	result.CanonicalMutations = canonical

	sequences, err := resetSequences(ctx, target, schema)
	if err != nil {
		return result, fmt.Errorf("reset sequences: %w", err)
	}
	result.SequencesReset = sequences
	// 0031 会删除过渡表。只有完整转换与序列重置均成功后才写入完成标记，
	// 防止 Web 的自动 migration 在尚未运行数据迁移时误删旧结构。
	if err := recordCanonicalCutoverReady(ctx, target, schema); err != nil {
		return result, fmt.Errorf("record canonical cutover readiness: %w", err)
	}
	return result, nil
}

func recordCanonicalCutoverReady(ctx context.Context, target Querier, schema string) error {
	_, err := target.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.schema_migrations (version)
VALUES ($1) ON CONFLICT (version) DO NOTHING`, quote(schema)), canonicalCutoverMarker)
	return err
}

// insertRow 跳过值为 NULL 的列，让目标库的 DEFAULT 生效。
// 旧库可空、新库声明为 NOT NULL 且带默认值的列（例如 users.douban_user_id）很常见，
// 原样插入 NULL 会违反非空约束。这也和 updateRow「旧库 NULL 不覆盖新库值」一致。
func insertRow(ctx context.Context, target Querier, schema, table string, current row, cols []string) error {
	names := make([]string, 0, len(cols))
	placeholders := make([]string, 0, len(cols))
	values := make([]any, 0, len(cols))
	for _, name := range cols {
		if current[name] == nil {
			continue
		}
		values = append(values, current[name])
		names = append(names, name)
		placeholders = append(placeholders, "$"+itoa(len(values)))
	}
	if len(names) == 0 {
		return fmt.Errorf("%s: 整行所有列均为 NULL，无法插入", table)
	}
	statement := fmt.Sprintf("INSERT INTO %s.%s (%s) VALUES (%s)",
		quote(schema), quote(table), strings.Join(quoteAll(names), ", "), strings.Join(placeholders, ", "))
	_, err := target.Exec(ctx, statement, values...)
	return err
}

// updateRow 只更新非键、非 NULL 的列，并用 IS DISTINCT FROM 跳过无变化的写入。
func updateRow(ctx context.Context, target Querier, schema string, spec TableSpec, current row, cols []string) error {
	keySet := make(map[string]bool, len(spec.Keys))
	for _, key := range spec.Keys {
		keySet[key] = true
	}
	assignments := make([]string, 0, len(cols))
	values := make([]any, 0, len(cols))
	for _, name := range cols {
		if keySet[name] || current[name] == nil {
			continue
		}
		values = append(values, current[name])
		assignments = append(assignments, quote(name)+" = $"+itoa(len(values)))
	}
	if len(assignments) == 0 {
		return nil
	}
	conditions := make([]string, 0, len(spec.Keys))
	for _, key := range spec.Keys {
		values = append(values, current[key])
		conditions = append(conditions, quote(key)+" = $"+itoa(len(values)))
	}
	statement := fmt.Sprintf("UPDATE %s.%s SET %s WHERE %s",
		quote(schema), quote(spec.Table), strings.Join(assignments, ", "), strings.Join(conditions, " AND "))
	_, err := target.Exec(ctx, statement, values...)
	return err
}

func itoa(value int) string {
	return fmt.Sprintf("%d", value)
}

// resetSequences 把每张表的 id 序列推到当前 MAX(id)，
// 否则复制了显式 id 之后，新库的下一次 INSERT 会撞上已占用主键。
func resetSequences(ctx context.Context, target Querier, schema string) (int, error) {
	count := 0
	for _, table := range SequenceTables {
		cols, err := columns(ctx, target, schema, table)
		if err != nil {
			return count, err
		}
		if !cols["id"] {
			continue
		}
		// pg_get_serial_sequence 对没有序列的列返回 NULL，setval 会被 COALESCE 短路。
		statement := fmt.Sprintf(`
			SELECT setval(sequence_name, GREATEST(max_id, 1), max_id IS NOT NULL)
			FROM (
				SELECT pg_get_serial_sequence('%s.%s', 'id') AS sequence_name,
				       (SELECT MAX(id) FROM %s.%s) AS max_id
			) info
			WHERE sequence_name IS NOT NULL`,
			schema, table, quote(schema), quote(table))
		if _, err := target.Exec(ctx, statement); err != nil {
			return count, fmt.Errorf("%s: %w", table, err)
		}
		count++
	}
	return count, nil
}
