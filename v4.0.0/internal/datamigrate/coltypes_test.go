package datamigrate

import "testing"

func ptr(v int32) *int32 { return &v }

// 只比对 data_type 会漏掉精度差异：numeric 和 numeric(3,1) 的 data_type 相同，
// 但同一个值会以不同的 Int/Exp 返回，导致 dry-run 永远报出假 update。
func TestFormatColumnTypeIncludesPrecisionAndScale(t *testing.T) {
	cases := []struct {
		dataType                                     string
		precision, scale, datetimePrecision, charLen *int32
		want                                         string
	}{
		{"numeric", ptr(3), ptr(1), nil, nil, "numeric(3,1)"},
		{"numeric", nil, nil, nil, nil, "numeric"},
		{"timestamp with time zone", nil, nil, ptr(6), nil, "timestamp(6) with time zone"},
		{"timestamp without time zone", nil, nil, ptr(0), nil, "timestamp(0) without time zone"},
		{"character varying", nil, nil, nil, ptr(32), "character varying(32)"},
		{"bigint", nil, nil, nil, nil, "bigint"},
		{"double precision", nil, nil, nil, nil, "double precision"},
		// 数组和自定义类型无法可靠重建类型名，必须返回空串表示「不要转换」。
		{"ARRAY", nil, nil, nil, nil, ""},
		{"USER-DEFINED", nil, nil, nil, nil, ""},
	}
	for _, testCase := range cases {
		got := formatColumnType(testCase.dataType, testCase.precision, testCase.scale, testCase.datetimePrecision, testCase.charLen)
		if got != testCase.want {
			t.Fatalf("formatColumnType(%q) = %q，应为 %q", testCase.dataType, got, testCase.want)
		}
	}
}

// 类型相同时不加 CAST；不同时按目标类型转换；无法重建类型时保持原样。
func TestSelectExpressionCastsOnlyWhenTypesDiffer(t *testing.T) {
	same := selectExpression("rating", columnType{SQL: "numeric(3,1)"}, columnType{SQL: "numeric(3,1)"})
	if same != `"rating"` {
		t.Fatalf("类型相同不应加 CAST: %s", same)
	}
	differ := selectExpression("rating", columnType{SQL: "numeric"}, columnType{SQL: "numeric(3,1)"})
	if differ != `CAST("rating" AS numeric(3,1))` {
		t.Fatalf("类型不同应转换为目标类型: %s", differ)
	}
	unknown := selectExpression("embedding", columnType{SQL: "USER-DEFINED"}, columnType{SQL: ""})
	if unknown != `"embedding"` {
		t.Fatalf("目标类型不可重建时应保持原样: %s", unknown)
	}
}

func TestTypeMismatchesListsOnlyRealDifferences(t *testing.T) {
	source := map[string]columnType{"rating": {SQL: "numeric"}, "title": {SQL: "text"}, "embedding": {SQL: ""}}
	target := map[string]columnType{"rating": {SQL: "numeric(3,1)"}, "title": {SQL: "text"}, "embedding": {SQL: ""}}
	got := typeMismatches([]string{"embedding", "rating", "title"}, source, target)
	if len(got) != 1 || got[0] != "rating: 旧库 numeric -> 新库 numeric(3,1)" {
		t.Fatalf("类型差异 = %v", got)
	}
}
