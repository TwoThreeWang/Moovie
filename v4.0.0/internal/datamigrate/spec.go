// Package datamigrate 把旧库 moovie 的业务数据一次性复制到新库 moovie_v2。
//
// 设计要点：
//   - 列不写死。运行时从 information_schema 读取两侧列，只复制交集；
//     新库独有列（如 media_unit_id、resource_status）永远不会被旧库覆盖成空值。
//   - 以自然键而不是代理 id 对齐两侧，避免序列错位导致张冠李戴。
//   - 旧库连接始终是只读事务，工具没有任何写旧库的代码路径。
//   - 目标库独有的行永远保留，没有 DELETE 路径。
package datamigrate

// TableSpec 描述一张需要迁移的表。Keys 是用于对齐新旧两侧的自然键，
// 必须在两侧都存在且组合唯一；为空表示该表以 id 对齐。
type TableSpec struct {
	Table string
	Keys  []string
	// Immutable 列即使两侧不同也不覆盖，用于保护新库自己维护的状态。
	Immutable []string
}

// DefaultTables 是白名单。不在此列表中的表不会被工具触碰，
// movies 与 watch_histories 不会进入目标库；规范转换器会把它们直接写入
// media 与 playback_positions。
var DefaultTables = []TableSpec{
	{Table: "sites", Keys: []string{"key"}},
	{Table: "copyright_filters", Keys: []string{"keyword"}},
	{Table: "category_filters", Keys: []string{"keyword"}},
	{Table: "users", Keys: []string{"email"}},
	{Table: "vod_items", Keys: []string{"source_key", "vod_id"}},
	{Table: "search_logs", Keys: []string{"id"}},
	{Table: "trending_keywords", Keys: []string{"keyword"}},
	{Table: "site_stats", Keys: []string{"site_key", "bucket"}},
	{Table: "user_movies", Keys: []string{"user_id", "movie_id"}},
	{Table: "monthly_reports", Keys: []string{"user_id", "year_month"}},
	{Table: "comment_likes", Keys: []string{"user_movie_id", "user_id"}},
	{Table: "comment_replies", Keys: []string{"id"}},
	{Table: "feedbacks", Keys: []string{"id"}},
	{Table: "danmakus", Keys: []string{"id"}},
}

// SequenceTables 列出复制了显式 id 的表。复制后必须把序列推到 MAX(id)，
// 否则新库后续 INSERT 会撞上已占用的主键。
var SequenceTables = []string{
	"users", "vod_items", "search_logs", "trending_keywords",
	"site_stats", "user_movies",
	"monthly_reports", "comment_likes", "comment_replies", "feedbacks", "danmakus",
	"sites", "copyright_filters", "category_filters",
}

// TablePlan 是单表的迁移计划。Insert/Update/Skip 以自然键为边界。
type TablePlan struct {
	Table          string   `json:"table"`
	Keys           []string `json:"keys"`
	Available      bool     `json:"available"`
	SourceRows     int      `json:"source_rows"`
	TargetRows     int      `json:"target_rows"`
	InsertRows     int      `json:"insert_rows"`
	UpdateRows     int      `json:"update_rows"`
	SkipRows       int      `json:"skip_rows"`
	TargetOnlyRows int      `json:"target_only_rows"`
	CopiedColumns  []string `json:"copied_columns,omitempty"`
	SourceOnlyCols []string `json:"source_only_columns,omitempty"`
	TargetOnlyCols []string `json:"target_only_columns,omitempty"`
	ChangedColumns []string `json:"changed_columns,omitempty"`
	DuplicateKeys  int      `json:"duplicate_source_keys,omitempty"`
	MissingKeys    []string `json:"natural_key_missing,omitempty"`
	// CheckViolations 是源库取值违反目标库 CHECK 约束的情况，属于必须人工修的冲突。
	CheckViolations []string `json:"check_violations,omitempty"`
	// UncheckedConstraints 列出无法自动解析的 CHECK 约束，提示它们不在预检范围内。
	UncheckedConstraints []string `json:"unchecked_constraints,omitempty"`
	// TypeMismatches 是两侧类型不一致、已按目标类型转换的列，仅作提示不算冲突。
	TypeMismatches []string `json:"type_mismatches,omitempty"`
	Note           string   `json:"note,omitempty"`
}

// ConflictCount 汇总必须人工修复、不能用参数绕过的问题。
func (plan TablePlan) ConflictCount() int {
	return len(plan.MissingKeys) + plan.DuplicateKeys + len(plan.CheckViolations)
}

// FavoritePlan 描述旧 favorites 表向 user_movies(status=wish) 的一次性转换。
type FavoritePlan struct {
	Available     bool `json:"available"`
	SourceRows    int  `json:"source_rows"`
	WouldInsert   int  `json:"would_insert"`
	AlreadyExists int  `json:"already_exists"`
	MissingUsers  int  `json:"missing_users,omitempty"`
	MissingMovies int  `json:"missing_movies,omitempty"`
}

// ConflictCount 缺失父记录会导致外键失败，必须先修数据。
func (plan FavoritePlan) ConflictCount() int { return plan.MissingUsers + plan.MissingMovies }

// Summary 汇总所有白名单表。
type Summary struct {
	SourceRows     int `json:"source_rows"`
	TargetRows     int `json:"target_rows"`
	InsertRows     int `json:"insert_rows"`
	UpdateRows     int `json:"update_rows"`
	SkipRows       int `json:"skip_rows"`
	TargetOnlyRows int `json:"target_only_rows"`
}

// Inspection 是一次只读快照生成的完整迁移计划。
type Inspection struct {
	Tables    []TablePlan  `json:"tables"`
	Favorites FavoritePlan `json:"favorites"`
	Summary   Summary      `json:"summary"`
	Conflicts int          `json:"conflicts"`
}

// ApplyResult 区分表复制、favorites 转换和新架构回填，便于迁移后核对。
type ApplyResult struct {
	TableInserts       int `json:"table_inserts"`
	TableUpdates       int `json:"table_updates"`
	FavoriteInserts    int `json:"favorite_inserts"`
	CanonicalMutations int `json:"canonical_mutations"`
	SequencesReset     int `json:"sequences_reset"`
}

// TotalMutations 是本次写入影响的总行数。
func (result ApplyResult) TotalMutations() int {
	return result.TableInserts + result.TableUpdates + result.FavoriteInserts + result.CanonicalMutations
}
