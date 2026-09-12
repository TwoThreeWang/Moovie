package search

import (
	"context"
	"encoding/json"
	"html"
	"regexp"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/mediatitle"
	"github.com/TwoThreeWang/Moovie/new/internal/mediatype"
	"github.com/TwoThreeWang/Moovie/new/internal/mediaunits"
)

// RefreshResourceMedia 供关联确认复用，资料补齐与选集可用性在同一事务中完成。
func (store *PostgresStore) RefreshResourceMedia(ctx context.Context, source, vodID string) error {
	item, err := store.FindBySourceID(ctx, source, vodID)
	if err != nil {
		return err
	}
	if item == nil {
		return nil
	}
	if err := store.fillResourceMedia(ctx, *item, true); err != nil {
		return err
	}
	return mediaunits.ReconcileResource(ctx, store.database, source, vodID)
}

// 位数必须与 catalog.validDoubanID（6~9 位）一致，否则这里放行的 ID 到了 worker
// 会被判成 invalid Douban ID 永久失败，而快照永远没有成功记录，任务被反复重排。
var resourceDoubanID = regexp.MustCompile(`^[1-9][0-9]{5,8}$`)
var resourceHTML = regexp.MustCompile(`<[^>]*>`)

// fillResourceMedia 只补空字段，并立即记录低优先级来源，不能误标为豆瓣资料。
// 调用方处于资源写入事务内；作品行锁也保护随后到达的高优先级合并。
func (store *PostgresStore) fillResourceMedia(ctx context.Context, item VodItem, queueMetadata bool) error {
	var mediaID int
	if err := store.database.QueryRow(ctx, `SELECT COALESCE((SELECT media_id FROM resource_media_links WHERE source_key=$1 AND vod_id=$2),0)`, item.SourceKey, item.VodId).Scan(&mediaID); err != nil {
		return err
	}
	if mediaID == 0 && resourceDoubanID.MatchString(item.VodDoubanId) {
		var existingTitle string
		if err := store.database.QueryRow(ctx, `SELECT COALESCE((SELECT title FROM media WHERE douban_id=$1),'')`, item.VodDoubanId).Scan(&existingTitle); err != nil {
			return err
		}
		// 已有豆瓣作品与资源站片名明显不符时，交给原有匹配/复核流程，不自动污染资料。
		if existingTitle != "" && mediatitle.Normalize(existingTitle) != mediatitle.Normalize(item.VodName) {
			return nil
		}
		if err := store.database.QueryRow(ctx, `INSERT INTO media(douban_id,media_type,metadata_status)
VALUES($1,'','partial') ON CONFLICT(douban_id) WHERE douban_id<>'' DO UPDATE SET douban_id=EXCLUDED.douban_id
RETURNING id`, item.VodDoubanId).Scan(&mediaID); err != nil {
			return err
		}
		if _, err := store.database.Exec(ctx, `INSERT INTO resource_media_links(source_key,vod_id,media_id,confidence,matched_by)
VALUES($1,$2,$3,1,'douban_id') ON CONFLICT(source_key,vod_id) DO NOTHING`, item.SourceKey, item.VodId, mediaID); err != nil {
			return err
		}
	}
	if mediaID == 0 {
		return nil
	}
	if _, err := store.database.Exec(ctx, `SELECT id FROM media WHERE id=$1 FOR UPDATE`, mediaID); err != nil {
		return err
	}
	people := func(names []string) string {
		if len(names) == 0 {
			return ""
		}
		values := make([]map[string]string, 0, len(names))
		for _, name := range names {
			values = append(values, map[string]string{"name": name})
		}
		data, _ := json.Marshal(values)
		return string(data)
	}
	clean := func(s string) string {
		return strings.Join(strings.Fields(html.UnescapeString(resourceHTML.ReplaceAllString(s, ""))), " ")
	}
	fields := []struct{ name, value string }{
		{"title", clean(item.VodName)}, {"poster", strings.TrimSpace(item.VodPic)}, {"year", strings.TrimSpace(item.VodYear)},
		{"summary", clean(item.VodContent)}, {"directors", people(item.GetDirectors())}, {"actors", people(item.GetActors())},
		{"genres", item.VodClass}, {"countries", item.VodArea}, {"duration", item.VodDuration},
		{"media_type", mediatype.Normalize(item.TypeName)},
	}
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		// 列名来自上面的固定列表，字段值始终使用参数。
		_, err := store.database.Exec(ctx, `WITH filled AS (
UPDATE media SET `+field.name+`=$2,updated_at=NOW() WHERE id=$1 AND `+field.name+`=''
AND NOT EXISTS(SELECT 1 FROM media_field_sources WHERE media_id=$1 AND field_name=$3 AND priority>10)
RETURNING id)
INSERT INTO media_field_sources(media_id,field_name,provider,priority,value_hash,merge_rule_version,observed_at)
SELECT id,$3,'resource',10,md5($2),2,NOW() FROM filled
ON CONFLICT(media_id,field_name) DO UPDATE SET provider='resource',priority=10,value_hash=EXCLUDED.value_hash,observed_at=NOW()`, mediaID, field.value, field.name)
		if err != nil {
			return err
		}
	}
	if queueMetadata {
		// 资源补齐不更新 last_metadata_sync_at；资料看起来完整也不能跳过豆瓣采集。
		_, err := store.database.Exec(ctx, `INSERT INTO worker_jobs(task_type,subject_key,payload,reason,status,available_at)
SELECT 'douban_metadata',douban_id,jsonb_build_object('douban_id',douban_id),'resource_placeholder','pending',NOW()
FROM media WHERE id=$1 AND douban_id ~ '^[0-9]{6,9}$' AND NOT EXISTS(SELECT 1 FROM media_source_snapshots WHERE media_id=media.id AND provider='douban' AND last_success_at IS NOT NULL)
ON CONFLICT(task_type,subject_key) WHERE status IN ('pending','running') DO NOTHING`, mediaID)
		if err != nil {
			return err
		}
	}
	if queueMetadata {
		return store.correctResourceType(ctx, mediaID)
	}
	return nil
}

// correctResourceType 只采纳至少两个站点的一致意见；有歧义或人工字段时不覆盖。
func (store *PostgresStore) correctResourceType(ctx context.Context, mediaID int) error {
	rows, err := store.database.Query(ctx, `SELECT DISTINCT r.source_key,r.type_name FROM resource_media_links l
JOIN vod_items r USING(source_key,vod_id) JOIN sites s ON s.key=r.source_key AND s.enabled
WHERE l.media_id=$1 AND l.confidence>=0.88 AND r.resource_status NOT IN('removed','retired','deleted') AND r.vod_play_url<>''`, mediaID)
	if err != nil {
		return err
	}
	votes := map[string]string{}
	conflict := false
	for rows.Next() {
		var site, label string
		if err := rows.Scan(&site, &label); err != nil {
			rows.Close()
			return err
		}
		kind := mediatype.Normalize(label)
		if kind == "" {
			continue
		}
		if prev := votes[site]; prev != "" && prev != kind {
			conflict = true
		}
		votes[site] = kind
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if conflict || len(votes) < 2 {
		return nil
	}
	kind := ""
	for _, vote := range votes {
		if kind != "" && kind != vote {
			return nil
		}
		kind = vote
	}
	_, err = store.database.Exec(ctx, `WITH corrected AS (
UPDATE media SET media_type=$2,updated_at=NOW() WHERE id=$1 AND media_type IS DISTINCT FROM $2
AND NOT EXISTS(SELECT 1 FROM media_field_sources WHERE media_id=$1 AND field_name='media_type' AND provider='manual')
RETURNING id)
INSERT INTO media_field_sources(media_id,field_name,provider,priority,value_hash,merge_rule_version,observed_at)
SELECT id,'media_type','resource_consensus',110,md5($2),2,NOW() FROM corrected
ON CONFLICT(media_id,field_name) DO UPDATE SET provider='resource_consensus',priority=110,value_hash=EXCLUDED.value_hash,observed_at=NOW()`, mediaID, kind)
	return err
}
