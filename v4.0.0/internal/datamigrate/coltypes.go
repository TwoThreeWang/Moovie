package datamigrate

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// columnType 是一列在数据库中的完整类型，含精度与标度。
// 只比对 data_type 不够：numeric 与 numeric(3,1) 的 data_type 都是 "numeric"，
// 但同一个值会以不同的 Int/Exp 组合返回，直接比较会产生永远收敛不了的假差异。
type columnType struct {
	Name string
	// SQL 是可直接用于 CAST 的完整类型，例如 numeric(3,1)、timestamp with time zone。
	SQL string
}

// columnTypes 读取表中每一列的完整类型。表不存在时返回空 map。
func columnTypes(ctx context.Context, querier Querier, schema, table string) (map[string]columnType, error) {
	rows, err := querier.Query(ctx, `
		SELECT column_name, data_type, numeric_precision, numeric_scale, datetime_precision, character_maximum_length
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]columnType)
	for rows.Next() {
		var name, dataType string
		var numericPrecision, numericScale, datetimePrecision, charLength *int32
		if err := rows.Scan(&name, &dataType, &numericPrecision, &numericScale, &datetimePrecision, &charLength); err != nil {
			return nil, err
		}
		result[name] = columnType{Name: name, SQL: formatColumnType(dataType, numericPrecision, numericScale, datetimePrecision, charLength)}
	}
	return result, rows.Err()
}

// formatColumnType 把 information_schema 的分散字段拼回可用于 CAST 的类型串。
func formatColumnType(dataType string, numericPrecision, numericScale, datetimePrecision, charLength *int32) string {
	switch dataType {
	case "numeric", "decimal":
		if numericPrecision != nil && numericScale != nil {
			return fmt.Sprintf("numeric(%d,%d)", *numericPrecision, *numericScale)
		}
		return "numeric"
	case "timestamp with time zone", "timestamp without time zone", "time with time zone", "time without time zone":
		if datetimePrecision != nil {
			// 精度插在类型名的括号里：timestamp(3) with time zone。
			if index := strings.Index(dataType, " with"); index > 0 {
				return fmt.Sprintf("%s(%d)%s", dataType[:index], *datetimePrecision, dataType[index:])
			}
			if index := strings.Index(dataType, " without"); index > 0 {
				return fmt.Sprintf("%s(%d)%s", dataType[:index], *datetimePrecision, dataType[index:])
			}
		}
		return dataType
	case "character varying", "character":
		if charLength != nil {
			return fmt.Sprintf("%s(%d)", dataType, *charLength)
		}
		return dataType
	case "ARRAY", "USER-DEFINED":
		// 数组和自定义类型（如 vector）无法可靠地重建类型名，保持原样不转换。
		return ""
	default:
		return dataType
	}
}

// selectExpression 返回读取某列时使用的表达式。
// 两侧类型不同时把源列转换成目标类型，让同一个逻辑值在两边产生一致的表示。
func selectExpression(column string, sourceType, targetType columnType) string {
	quoted := quote(column)
	if targetType.SQL == "" || sourceType.SQL == "" || sourceType.SQL == targetType.SQL {
		return quoted
	}
	return fmt.Sprintf("CAST(%s AS %s)", quoted, targetType.SQL)
}

// typeMismatches 返回两侧类型不一致的列，便于在报告里可见。
func typeMismatches(cols []string, sourceTypes, targetTypes map[string]columnType) []string {
	out := make([]string, 0)
	for _, name := range cols {
		source, target := sourceTypes[name], targetTypes[name]
		if source.SQL == "" || target.SQL == "" || source.SQL == target.SQL {
			continue
		}
		out = append(out, fmt.Sprintf("%s: 旧库 %s -> 新库 %s", name, source.SQL, target.SQL))
	}
	sort.Strings(out)
	return out
}
