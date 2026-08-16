package datamigrate

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// StreamResult 汇总一次流式迁移的写入量。
type StreamResult struct {
	TableRows          map[string]int64
	CopiedRows         int64
	FavoriteInserts    int
	CanonicalMutations int
	SequencesReset     int
}

// StreamImport 用 PostgreSQL 的 COPY 协议把旧库数据搬进新库，再执行 favorites
// 转换、canonical 回填和序列重置。
//
// 与 Importer.Apply 的区别只有一个：数据不经过 Go 的内存。Apply 会把两侧整表
// 读成 map[string]any 再逐行 INSERT，几百万行时既吃光内存又要几百万次网络往返；
// 这里源库直接 COPY TO STDOUT、目标库 COPY FROM STDIN，中间是一个 io.Pipe，
// 常驻内存只有管道缓冲。代价是 COPY 无法做逐行 diff，因此要求目标表为空。
//
// 全部写入在调用方给的目标事务里完成，任一步失败整体回滚，重跑不会留下半份数据。
func StreamImport(ctx context.Context, source, target *pgx.Conn, sourceTx, targetTx Querier,
	schema string, specs []TableSpec, progress func(string, int64)) (StreamResult, error) {

	result := StreamResult{TableRows: make(map[string]int64, len(specs))}
	importer := Importer{Schema: schema, Source: sourceTx, Target: targetTx, Specs: specs}

	for _, spec := range specs {
		copied, err := copyTable(ctx, source, target, sourceTx, targetTx, schema, spec.Table)
		if err != nil {
			return result, fmt.Errorf("copy %s: %w", spec.Table, err)
		}
		result.TableRows[spec.Table] = copied
		result.CopiedRows += copied
		if progress != nil {
			progress(spec.Table, copied)
		}
	}

	favorites, err := importer.applyFavorites(ctx, targetTx, schema)
	if err != nil {
		return result, fmt.Errorf("convert favorites: %w", err)
	}
	result.FavoriteInserts = favorites

	canonical, err := importer.CanonicalBackfill(ctx, targetTx, schema)
	if err != nil {
		return result, fmt.Errorf("canonical backfill: %w", err)
	}
	result.CanonicalMutations = canonical

	sequences, err := resetSequences(ctx, targetTx, schema)
	if err != nil {
		return result, fmt.Errorf("reset sequences: %w", err)
	}
	result.SequencesReset = sequences

	// 0031 会删除过渡表。只有完整转换与序列重置均成功后才写入完成标记，
	// 防止 Web 的自动 migration 在尚未运行数据迁移时误删旧结构。
	if err := recordCanonicalCutoverReady(ctx, targetTx, schema); err != nil {
		return result, fmt.Errorf("record canonical cutover readiness: %w", err)
	}
	return result, nil
}

// copyTable 搬运单张表。列取两侧交集，新库独有列保持自身默认值。
func copyTable(ctx context.Context, source, target *pgx.Conn, sourceTx, targetTx Querier,
	schema, table string) (int64, error) {

	sourceCols, err := columns(ctx, sourceTx, schema, table)
	if err != nil {
		return 0, err
	}
	if len(sourceCols) == 0 {
		return 0, nil // 旧库没有这张表，跳过。
	}
	targetCols, err := columns(ctx, targetTx, schema, table)
	if err != nil {
		return 0, err
	}
	if len(targetCols) == 0 {
		return 0, fmt.Errorf("目标库缺少表 %s.%s；请先执行结构迁移", schema, table)
	}

	shared := make([]string, 0, len(sourceCols))
	for name := range sourceCols {
		if targetCols[name] {
			shared = append(shared, name)
		}
	}
	if len(shared) == 0 {
		return 0, fmt.Errorf("%s: 两侧没有共同列", table)
	}
	sort.Strings(shared)

	selectExprs, err := sourceSelectExpressions(ctx, sourceTx, targetTx, schema, table, shared)
	if err != nil {
		return 0, err
	}

	var existing int64
	if err := targetTx.QueryRow(ctx,
		fmt.Sprintf("SELECT count(*) FROM %s.%s", quote(schema), quote(table))).Scan(&existing); err != nil {
		return 0, err
	}
	if existing > 0 {
		return 0, fmt.Errorf("%s: 目标表已有 %d 行；COPY 无法去重，请先清空新库再重跑", table, existing)
	}

	// 两个列表顺序必须一致：SELECT 的第 n 个表达式对应 COPY 列清单的第 n 列。
	selectList := strings.Join(selectExprs, ", ")
	list := strings.Join(quoteAll(shared), ", ")
	reader, writer := io.Pipe()
	copyErr := make(chan error, 1)
	go func() {
		_, err := source.PgConn().CopyTo(ctx, writer,
			fmt.Sprintf("COPY (SELECT %s FROM %s.%s) TO STDOUT", selectList, quote(schema), quote(table)))
		// 必须 CloseWithError：否则读端在源库出错时会一直阻塞等待 EOF。
		writer.CloseWithError(err)
		copyErr <- err
	}()

	tag, writeErr := target.PgConn().CopyFrom(ctx, reader,
		fmt.Sprintf("COPY %s.%s (%s) FROM STDIN", quote(schema), quote(table), list))
	// 目标库中途报错（例如撞上约束）会停止读取管道，此时写端还阻塞在 Write 上。
	// 必须先关掉读端让它拿到错误返回，否则下面的 <-copyErr 会永远等下去。
	reader.CloseWithError(writeErr)
	readErr := <-copyErr

	if writeErr != nil {
		return 0, fmt.Errorf("写入目标库: %w", writeErr)
	}
	if readErr != nil {
		return 0, fmt.Errorf("读取源库: %w", readErr)
	}
	return tag.RowsAffected(), nil
}

// sourceSelectExpressions 生成源库 SELECT 的表达式列表，顺序与 shared 一致。
//
// 关键差异：逐行 INSERT 可以省略值为 NULL 的列，让目标库的 DEFAULT 生效；COPY
// 是整表固定列集，给了 NULL 就原样写入 NULL。旧库可空、新库 NOT NULL 的列很常见
// （例如 users.douban_user_id），所以这里在源端就用目标库的默认值把 NULL 补掉。
func sourceSelectExpressions(ctx context.Context, sourceTx, targetTx Querier,
	schema, table string, shared []string) ([]string, error) {

	rows, err := targetTx.Query(ctx, `
		SELECT column_name, COALESCE(column_default, '')
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND is_nullable = 'NO'`, schema, table)
	if err != nil {
		return nil, err
	}
	notNull := make(map[string]string)
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			rows.Close()
			return nil, err
		}
		notNull[name] = def
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	exprs := make([]string, 0, len(shared))
	for _, name := range shared {
		def, required := notNull[name]
		if !required {
			exprs = append(exprs, quote(name))
			continue
		}
		var nulls int64
		statement := fmt.Sprintf("SELECT count(*) FROM %s.%s WHERE %s IS NULL",
			quote(schema), quote(table), quote(name))
		if err := sourceTx.QueryRow(ctx, statement).Scan(&nulls); err != nil {
			return nil, err
		}
		if nulls == 0 {
			exprs = append(exprs, quote(name))
			continue
		}
		if def == "" {
			return nil, fmt.Errorf("%s.%s: 源库有 %d 行为 NULL，新库该列 NOT NULL 且无默认值；请先修数据",
				table, name, nulls)
		}
		// nextval 会消耗序列，在源库上求值等于污染旧库的序列状态，不能这么补。
		if strings.Contains(strings.ToLower(def), "nextval") {
			return nil, fmt.Errorf("%s.%s: 源库有 %d 行为 NULL，而新库默认值是序列（%s），无法自动补齐",
				table, name, nulls, def)
		}
		exprs = append(exprs, fmt.Sprintf("COALESCE(%s, %s)", quote(name), def))
	}
	return exprs, nil
}
