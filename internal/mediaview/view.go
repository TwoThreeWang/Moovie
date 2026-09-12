// Package mediaview 定义全站作品资料的记录级优先规则，不负责数据库查询。
package mediaview

type Info struct {
	Title, Poster, Year, Directors, Actors, Summary string
	Rating                                          float64
	Genres, Countries                               []string
}

// Choose 有规范记录就完整使用它，缺字段显示空状态，不混用资源站内容。
func Choose(canonical *Info, resource Info) Info {
	if canonical != nil {
		return *canonical
	}
	return resource
}

// Column 让批量 SQL 与页面组合遵循同一记录级规则。参数均由代码固定，不能传用户输入。
func Column(field, fallback string) string {
	return "CASE WHEN media.id IS NOT NULL THEN media." + field + " ELSE " + fallback + " END"
}

// ResourceJoin 只在规范记录不存在时批量回退到资源资料，最后才使用用户记录快照。
func ResourceJoin(doubanExpression string) string {
	return ` LEFT JOIN LATERAL (SELECT v.vod_name,v.vod_pic,v.vod_year FROM vod_items v
WHERE media.id IS NULL AND v.vod_douban_id=` + doubanExpression + `
ORDER BY v.last_seen_at DESC NULLS LAST,v.source_key,v.vod_id LIMIT 1) display_resource ON TRUE `
}
