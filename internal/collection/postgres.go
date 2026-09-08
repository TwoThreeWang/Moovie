package collection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/jackc/pgx/v5"
)

// coverLimit 是列表页每个片单取几张海报拼封面。
const coverLimit = 4

var ErrIncompletePublished = errors.New("发布前请填写导语，并为每部影片填写推荐理由")
var ErrInvalidRelated = errors.New("相关片单最多选择三个，不能重复或选择自身；新增关联请选择已发布且非空的片单")

var ErrEmptyPublished = errors.New("没有有效影片，不能发布空片单")

// PostgresStore 是片单的 PostgreSQL 实现。
type PostgresStore struct{ database database.Executor }

// NewPostgresStore 创建存储实现。
func NewPostgresStore(executor database.Executor) *PostgresStore {
	return &PostgresStore{database: executor}
}

// collectionColumns 是片单列表共用的字段。条目数和封面都是现算的：
// 存一个 item_count 冗余列迟早会和实际条目对不上，这个量级不值得为它做同步。
const collectionColumns = `c.id, COALESCE(c.owner_user_id, 0), c.slug, c.title, c.description, c.featured, c.updated_at,
(SELECT COUNT(*) FROM collection_items i WHERE i.collection_id = c.id)::int,
COALESCE((SELECT ARRAY_AGG(cover.poster ORDER BY cover.position)
  FROM (SELECT m.poster, i.position FROM collection_items i JOIN media m ON m.id = i.media_id
        WHERE i.collection_id = c.id AND m.poster <> '' ORDER BY i.position LIMIT ` + "4" + `) cover), '{}')`

// ListFeatured 列出进入发现流的片单，空片单不展示。
func (store *PostgresStore) ListFeatured(ctx context.Context, limit, offset int) ([]Collection, error) {
	rows, err := store.database.Query(ctx, `SELECT `+collectionColumns+` FROM collections c
WHERE c.featured AND EXISTS (SELECT 1 FROM collection_items i WHERE i.collection_id = c.id)
ORDER BY c.updated_at DESC, c.id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list featured collections: %w", err)
	}
	defer rows.Close()
	return scanCollections(rows)
}

// CountFeatured 统计发现流里的片单数量，用于分页。
func (store *PostgresStore) CountFeatured(ctx context.Context) (int, error) {
	var count int
	err := store.database.QueryRow(ctx, `SELECT COUNT(*)::int FROM collections c
WHERE c.featured AND EXISTS (SELECT 1 FROM collection_items i WHERE i.collection_id = c.id)`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count featured collections: %w", err)
	}
	return count, nil
}

// GetBySlug 按地址取片单，找不到返回 nil。
func (store *PostgresStore) GetBySlug(ctx context.Context, slug string) (*Collection, error) {
	rows, err := store.database.Query(ctx, `SELECT `+collectionColumns+` FROM collections c WHERE c.slug = $1`, slug)
	if err != nil {
		return nil, fmt.Errorf("get collection: %w", err)
	}
	defer rows.Close()
	collections, err := scanCollections(rows)
	if err != nil {
		return nil, err
	}
	if len(collections) == 0 {
		return nil, nil
	}
	return &collections[0], nil
}

// ListItems 按顺序列出片单条目。
func (store *PostgresStore) ListItems(ctx context.Context, collectionID int) ([]Item, error) {
	rows, err := store.database.Query(ctx, `SELECT i.media_id, m.douban_id, m.title, m.poster, m.year, i.position, i.note
FROM collection_items i JOIN media m ON m.id = i.media_id
WHERE i.collection_id = $1 ORDER BY i.position`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list collection items: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.MediaID, &item.DoubanID, &item.Title, &item.Poster,
			&item.Year, &item.Position, &item.Note); err != nil {
			return nil, fmt.Errorf("scan collection item: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// FeaturedForSitemap 返回所有要收录的片单地址和更新时间。
func (store *PostgresStore) FeaturedForSitemap(ctx context.Context) ([]Collection, error) {
	rows, err := store.database.Query(ctx, `SELECT c.slug, c.updated_at FROM collections c
WHERE c.featured AND EXISTS (SELECT 1 FROM collection_items i WHERE i.collection_id = c.id)
ORDER BY c.updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list sitemap collections: %w", err)
	}
	defer rows.Close()
	collections := make([]Collection, 0)
	for rows.Next() {
		var collection Collection
		if err := rows.Scan(&collection.Slug, &collection.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan sitemap collection: %w", err)
		}
		collections = append(collections, collection)
	}
	return collections, rows.Err()
}

// ListAll 是后台列表，包含还没发布的片单。limit=0 用于完整的关联选择列表。
func (store *PostgresStore) ListAll(ctx context.Context, limit, offset int) ([]Collection, error) {
	rows, err := store.database.Query(ctx, `SELECT `+collectionColumns+` FROM collections c
ORDER BY c.updated_at DESC, c.id DESC LIMIT NULLIF($1, 0) OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()
	return scanCollections(rows)
}

// Save 新建或更新片单并整体替换条目，全部在一个事务里完成。
// 认不出的豆瓣 ID 通过 unknown 返回给调用方展示，而不是静默丢弃：
// 策展时粘错一个 ID 却没有任何提示，是最难发现的一类错误。
func (store *PostgresStore) Save(ctx context.Context, collection Collection, items []ItemInput) (int, []string, error) {
	if collection.Featured {
		if strings.TrimSpace(collection.Description) == "" {
			return 0, nil, ErrIncompletePublished
		}
		for _, item := range items {
			if strings.TrimSpace(item.Note) == "" {
				return 0, nil, ErrIncompletePublished
			}
		}
	}
	if len(collection.Related) > 3 {
		return 0, nil, ErrInvalidRelated
	}
	seen := map[int]bool{}
	for _, related := range collection.Related {
		if related.ID <= 0 || related.ID == collection.ID || seen[related.ID] {
			return 0, nil, ErrInvalidRelated
		}
		seen[related.ID] = true
	}
	pool, ok := store.database.(interface {
		Begin(ctx context.Context) (database.Transaction, error)
	})
	if !ok {
		return 0, nil, errors.New("collection store requires a transactional database")
	}
	transaction, err := pool.Begin(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("begin collection save: %w", err)
	}
	defer func() { _ = transaction.Rollback(ctx) }()

	id := collection.ID
	if id == 0 {
		if err := transaction.QueryRow(ctx, `INSERT INTO collections (owner_user_id, slug, title, description, featured)
VALUES (NULLIF($1, 0), $2, $3, $4, $5) RETURNING id`,
			collection.OwnerUserID, collection.Slug, collection.Title, collection.Description, collection.Featured).Scan(&id); err != nil {
			return 0, nil, fmt.Errorf("insert collection: %w", err)
		}
	} else if _, err := transaction.Exec(ctx, `UPDATE collections
SET slug = $2, title = $3, description = $4, featured = $5, updated_at = NOW() WHERE id = $1`,
		id, collection.Slug, collection.Title, collection.Description, collection.Featured); err != nil {
		return 0, nil, fmt.Errorf("update collection: %w", err)
	}

	if _, err := transaction.Exec(ctx, `DELETE FROM collection_items WHERE collection_id = $1`, id); err != nil {
		return 0, nil, fmt.Errorf("clear collection items: %w", err)
	}
	unknown := make([]string, 0)
	position := 0
	for _, item := range items {
		var mediaID int
		err := transaction.QueryRow(ctx, `SELECT id FROM media WHERE douban_id = $1`, item.DoubanID).Scan(&mediaID)
		if errors.Is(err, pgx.ErrNoRows) {
			unknown = append(unknown, item.DoubanID)
			continue
		}
		if err != nil {
			return 0, nil, fmt.Errorf("find collection item %s: %w", item.DoubanID, err)
		}
		position++
		if _, err := transaction.Exec(ctx, `INSERT INTO collection_items (collection_id, media_id, position, note)
VALUES ($1, $2, $3, $4)`, id, mediaID, position, item.Note); err != nil {
			return 0, nil, fmt.Errorf("insert collection item %s: %w", item.DoubanID, err)
		}
	}
	if collection.Featured && position == 0 {
		return 0, unknown, ErrEmptyPublished
	}
	// 保留已配置但已下架的关联，前台读取时过滤；新增关联必须可公开访问。
	for _, related := range collection.Related {
		if related.ID == id {
			return 0, nil, ErrInvalidRelated
		}
		var valid bool
		if err := transaction.QueryRow(ctx, `SELECT EXISTS (
            SELECT 1 FROM collections c WHERE c.id=$2 AND (
                (c.featured AND EXISTS (SELECT 1 FROM collection_items i WHERE i.collection_id=c.id))
                OR EXISTS (SELECT 1 FROM collection_relations r WHERE r.collection_id=$1 AND r.related_id=c.id)))`, id, related.ID).Scan(&valid); err != nil {
			return 0, nil, err
		}
		if !valid {
			return 0, nil, ErrInvalidRelated
		}
	}
	if _, err := transaction.Exec(ctx, `DELETE FROM collection_relations WHERE collection_id=$1`, id); err != nil {
		return 0, nil, err
	}
	for position, related := range collection.Related {
		if _, err := transaction.Exec(ctx, `INSERT INTO collection_relations(collection_id,related_id,position,reason) VALUES($1,$2,$3,$4)`, id, related.ID, position+1, strings.TrimSpace(related.Reason)); err != nil {
			return 0, nil, err
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return 0, nil, fmt.Errorf("commit collection save: %w", err)
	}
	return id, unknown, nil
}

// ListRelated 保留编辑顺序，公开读取时过滤已下架或空的目标。
func (store *PostgresStore) ListRelated(ctx context.Context, collectionID int, publicOnly bool) ([]RelatedCollection, error) {
	rows, err := store.database.Query(ctx, `SELECT `+collectionColumns+`, r.reason
        FROM collection_relations r JOIN collections c ON c.id=r.related_id
        WHERE r.collection_id=$1 AND (NOT $2 OR (c.featured AND EXISTS
            (SELECT 1 FROM collection_items i WHERE i.collection_id=c.id))) ORDER BY r.position`, collectionID, publicOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RelatedCollection, 0)
	for rows.Next() {
		var item RelatedCollection
		if err := rows.Scan(&item.ID, &item.OwnerUserID, &item.Slug, &item.Title, &item.Description, &item.Featured, &item.UpdatedAt, &item.ItemCount, &item.Covers, &item.Reason); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

// Delete 删除片单，条目随外键级联删除。
func (store *PostgresStore) Delete(ctx context.Context, id int) error {
	if _, err := store.database.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	return nil
}

// scanCollections 读取片单列表共用的字段。
func scanCollections(rows database.Rows) ([]Collection, error) {
	collections := make([]Collection, 0)
	for rows.Next() {
		var collection Collection
		if err := rows.Scan(&collection.ID, &collection.OwnerUserID, &collection.Slug, &collection.Title,
			&collection.Description, &collection.Featured, &collection.UpdatedAt,
			&collection.ItemCount, &collection.Covers); err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		collections = append(collections, collection)
	}
	return collections, rows.Err()
}

// slugFromTitle 在后台没填地址时，用标题生成一个保守的备用地址。
func slugFromTitle(title string) string {
	lowered := strings.ToLower(strings.TrimSpace(title))
	var builder strings.Builder
	for _, character := range lowered {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			builder.WriteRune(character)
		case character == ' ' || character == '-' || character == '_':
			builder.WriteByte('-')
		}
	}
	return strings.Trim(builder.String(), "-")
}
