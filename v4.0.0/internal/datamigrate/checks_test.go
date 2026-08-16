package datamigrate

import "testing"

// PostgreSQL 把 CHECK (col IN (...)) 规范化成 = ANY (ARRAY[...])，
// 解析必须针对规范化后的形式，而不是建表语句里的写法。
func TestParseEnumConstraintHandlesNormalizedForms(t *testing.T) {
	cases := []struct {
		name       string
		definition string
		column     string
		allowed    []string
	}{
		{
			name:       "varchar 列（feedbacks.type 的实际形式）",
			definition: `CHECK (((type)::text = ANY ((ARRAY['bug'::character varying, 'request'::character varying, 'suggestion'::character varying, 'dmca'::character varying, '系统告警'::character varying])::text[])))`,
			column:     "type",
			allowed:    []string{"bug", "request", "suggestion", "dmca", "系统告警"},
		},
		{
			name:       "text 列",
			definition: `CHECK ((status = ANY (ARRAY['wish'::text, 'watched'::text])))`,
			column:     "status",
			allowed:    []string{"wish", "watched"},
		},
		{
			name:       "带引号的列名",
			definition: `CHECK ((("unit_type")::text = ANY ((ARRAY['feature'::character varying, 'episode'::character varying])::text[])))`,
			column:     "unit_type",
			allowed:    []string{"feature", "episode"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			constraint, ok := parseEnumConstraint("c", testCase.definition)
			if !ok {
				t.Fatalf("未能解析: %s", testCase.definition)
			}
			if constraint.Column != testCase.column {
				t.Fatalf("列 = %q，应为 %q", constraint.Column, testCase.column)
			}
			if len(constraint.Allowed) != len(testCase.allowed) {
				t.Fatalf("允许值个数 = %d，应为 %d：%v", len(constraint.Allowed), len(testCase.allowed), constraint.Allowed)
			}
			for _, value := range testCase.allowed {
				if !constraint.Allowed[value] {
					t.Fatalf("允许集合缺少 %q：%v", value, constraint.Allowed)
				}
			}
		})
	}
}

// 非枚举 CHECK 必须被明确标记为无法解析，不能假装通过预检。
func TestParseEnumConstraintRejectsNonEnumChecks(t *testing.T) {
	for _, definition := range []string{
		`CHECK ((rating >= 0))`,
		`CHECK ((char_length(title) < 200))`,
		`CHECK (((expires_at IS NULL) OR (expires_at > created_at)))`,
	} {
		if _, ok := parseEnumConstraint("c", definition); ok {
			t.Fatalf("不应把非枚举约束解析为枚举: %s", definition)
		}
	}
}
