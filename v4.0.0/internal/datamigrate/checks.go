package datamigrate

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// enumConstraint 是目标表上形如 CHECK (col IN ('a','b')) 的约束。
// PostgreSQL 会把它规范化成 ((col)::text = ANY ((ARRAY['a'::character varying, ...])::text[]))，
// 因此按规范化后的形式解析，而不是按建表语句的写法。
type enumConstraint struct {
	Name    string
	Column  string
	Allowed map[string]bool
}

var (
	// 匹配 ((col)::text = ANY  或  (col = ANY，列名可能带引号。
	enumColumnPattern = regexp.MustCompile(`\(?"?([a-zA-Z_][a-zA-Z0-9_]*)"?\)?(?:::[a-z ]+)?\s*=\s*ANY`)
	// 匹配 ARRAY[...] 里的单引号字面量，允许 '' 转义。
	enumArrayPattern   = regexp.MustCompile(`ARRAY\[(.*?)\]`)
	enumLiteralPattern = regexp.MustCompile(`'((?:[^']|'')*)'`)
)

// parseEnumConstraint 解析单条 CHECK 定义。无法识别为枚举时返回 false，
// 调用方会把这类约束标记为「无法预检」而不是假装通过。
func parseEnumConstraint(name, definition string) (enumConstraint, bool) {
	columnMatch := enumColumnPattern.FindStringSubmatch(definition)
	arrayMatch := enumArrayPattern.FindStringSubmatch(definition)
	if columnMatch == nil || arrayMatch == nil {
		return enumConstraint{}, false
	}
	allowed := make(map[string]bool)
	for _, literal := range enumLiteralPattern.FindAllStringSubmatch(arrayMatch[1], -1) {
		allowed[strings.ReplaceAll(literal[1], "''", "'")] = true
	}
	if len(allowed) == 0 {
		return enumConstraint{}, false
	}
	return enumConstraint{Name: name, Column: columnMatch[1], Allowed: allowed}, true
}

// enumConstraints 读取目标表上所有 CHECK 约束并尽力解析成枚举集合。
// 第二个返回值是无法解析的约束名，用于提示这些约束不在预检范围内。
func enumConstraints(ctx context.Context, target Querier, schema, table string) ([]enumConstraint, []string, error) {
	rows, err := target.Query(ctx, `
		SELECT conname, pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE contype = 'c' AND conrelid = to_regclass($1)`, schema+"."+table)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	parsed := make([]enumConstraint, 0)
	unparsed := make([]string, 0)
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return nil, nil, err
		}
		if constraint, ok := parseEnumConstraint(name, definition); ok {
			parsed = append(parsed, constraint)
			continue
		}
		unparsed = append(unparsed, name)
	}
	return parsed, unparsed, rows.Err()
}

// checkEnumViolations 把源库该列的 distinct 值和目标库允许集合比对，
// 返回人类可读的违规描述。NULL 不参与：插入时会被跳过，由目标默认值处理。
func checkEnumViolations(ctx context.Context, source Querier, schema, table string,
	constraints []enumConstraint, sourceColumns map[string]bool) ([]string, error) {
	violations := make([]string, 0)
	for _, constraint := range constraints {
		if !sourceColumns[constraint.Column] {
			continue
		}
		rows, err := source.Query(ctx, fmt.Sprintf(
			`SELECT DISTINCT %s FROM %s.%s WHERE %s IS NOT NULL`,
			quote(constraint.Column), quote(schema), quote(table), quote(constraint.Column)))
		if err != nil {
			return nil, err
		}
		offending := make([]string, 0)
		for rows.Next() {
			var value any
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return nil, err
			}
			if text := normalize(value); !constraint.Allowed[text] {
				offending = append(offending, text)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(offending) == 0 {
			continue
		}
		sort.Strings(offending)
		allowed := sortedKeys(constraint.Allowed)
		violations = append(violations, fmt.Sprintf(
			"%s: 列 %s 的值 [%s] 不在允许集合 [%s] 内",
			constraint.Name, constraint.Column,
			strings.Join(offending, ", "), strings.Join(allowed, ", ")))
	}
	sort.Strings(violations)
	return violations, nil
}
