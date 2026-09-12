package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaunits"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/playurl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PlaybackReconcileOptions 控制单进程串行回填。完成标记跟业务修改一起提交，重启不重复已完成工作。
type PlaybackReconcileOptions struct {
	Phase       string // online = resources + units；history 只能在停止进度写入后显式运行。
	Apply       bool
	Maintenance bool
	Limit       int
	Delay       time.Duration
	LockTimeout time.Duration
	ItemTimeout time.Duration
	Progress    func(PlaybackReconcileReport)
}

type PlaybackReconcileReport struct {
	Phase                                string
	Checked, Changed, Completed, Skipped int
	Remaining, Limited                   bool
}

type playbackReconcileKey struct {
	Source, Vod string
	Media       int
}

// ReconcilePlayback 先清洗资源，再按作品重算一次。繁忙记录保持未完成，下一次运行自动补跑。
// 检查模式不加行锁、不写完成标记；在线阶段不合并历史，也不批量排队元数据采集。
func (store *PostgresStore) ReconcilePlayback(ctx context.Context, opts PlaybackReconcileOptions) (report PlaybackReconcileReport, err error) {
	// 任何提前返回都没走完本轮；未知状态按"还有待办"报告，重跑继续即可。
	defer func() {
		if err != nil {
			report.Remaining = true
		}
	}()
	if opts.Phase == "" {
		opts.Phase = "online"
	}
	phases := []string{opts.Phase}
	switch opts.Phase {
	case "online":
		phases = []string{"resources", "units"}
	case "resources", "units":
	case "history":
		if opts.Apply && !opts.Maintenance {
			return report, errors.New("history 阶段须停止进度写入后提供 -maintenance")
		}
	default:
		return report, fmt.Errorf("未知阶段 %q", opts.Phase)
	}
	if opts.Limit < 0 || opts.Delay < 0 {
		return report, errors.New("limit 和 delay 不能为负数")
	}
	if opts.LockTimeout == 0 {
		opts.LockTimeout = 100 * time.Millisecond
	}
	if opts.ItemTimeout == 0 {
		opts.ItemTimeout = 3 * time.Second
	}
	if opts.LockTimeout < time.Millisecond || opts.ItemTimeout <= opts.LockTimeout {
		return report, errors.New("锁等待至少 1ms，单条超时必须大于锁等待")
	}
	for _, phase := range phases {
		report.Phase = phase
		cursor := playbackReconcileKey{}
		for {
			var keys []playbackReconcileKey
			keys, err = store.playbackReconcileKeys(ctx, phase, cursor)
			if err != nil {
				return report, err
			}
			if len(keys) == 0 {
				break
			}
			for _, key := range keys {
				if opts.Limit > 0 && report.Checked >= opts.Limit {
					report.Limited = true
					break
				}
				var changed bool
				changed, err = store.reconcilePlaybackItem(ctx, phase, key, opts)
				if err != nil && !isPlaybackReconcileBusy(ctx, err) {
					return report, fmt.Errorf("阶段 %s 资源 %s/%s 作品 %d: %w", phase, key.Source, key.Vod, key.Media, err)
				}
				report.Checked++
				if err != nil {
					report.Skipped++
				} else {
					if changed {
						report.Changed++
					}
					if opts.Apply {
						report.Completed++
					}
				}
				cursor = key
				if opts.Progress != nil {
					opts.Progress(report)
				}
				if opts.Delay > 0 {
					timer := time.NewTimer(opts.Delay)
					select {
					case <-ctx.Done():
						timer.Stop()
						return report, ctx.Err()
					case <-timer.C:
					}
				}
			}
			if report.Limited {
				break
			}
		}
		if report.Limited {
			break
		}
	}
	// 不做全表 COUNT；只报告是否还存在未完成记录，包括繁忙跳过和在线新增的工作。
	for _, phase := range phases {
		var pending bool
		query := "SELECT EXISTS(SELECT 1 FROM vod_items WHERE NOT playback_cleaned)"
		if phase == "units" {
			query = "SELECT EXISTS(SELECT 1 FROM media WHERE playback_reconciled_at IS NULL)"
		}
		if phase == "history" {
			query = "SELECT EXISTS(SELECT 1 FROM media WHERE NOT playback_history_repaired)"
		}
		if err = store.database.QueryRow(ctx, query).Scan(&pending); err != nil {
			return report, err
		}
		report.Remaining = report.Remaining || pending
	}
	return report, nil
}

// 只批量读取主键，不使用 OFFSET；即使繁忙记录被跳过，本轮也能继续向后推进。
func (store *PostgresStore) playbackReconcileKeys(ctx context.Context, phase string, cursor playbackReconcileKey) ([]playbackReconcileKey, error) {
	query := `SELECT source_key,vod_id FROM vod_items WHERE NOT playback_cleaned AND (source_key,vod_id)>($1,$2) ORDER BY source_key,vod_id LIMIT 200`
	args := []any{cursor.Source, cursor.Vod}
	if phase != "resources" {
		predicate := "playback_reconciled_at IS NULL"
		if phase == "history" {
			predicate = "NOT playback_history_repaired"
		}
		query = `SELECT id FROM media WHERE ` + predicate + ` AND id>$1 ORDER BY id LIMIT 200`
		args = []any{cursor.Media}
	}
	rows, err := store.database.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []playbackReconcileKey
	for rows.Next() {
		var key playbackReconcileKey
		if phase == "resources" {
			err = rows.Scan(&key.Source, &key.Vod)
		} else {
			err = rows.Scan(&key.Media)
		}
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (store *PostgresStore) reconcilePlaybackItem(ctx context.Context, phase string, key playbackReconcileKey, opts PlaybackReconcileOptions) (bool, error) {
	itemCtx, cancel := context.WithTimeout(ctx, opts.ItemTimeout)
	defer cancel()
	if !opts.Apply {
		if phase != "resources" {
			return false, nil
		}
		var raw, remarks string
		err := store.database.QueryRow(itemCtx, `SELECT vod_play_url,vod_remarks FROM vod_items WHERE source_key=$1 AND vod_id=$2`, key.Source, key.Vod).Scan(&raw, &remarks)
		return raw != playurl.Clean(raw, remarks), err
	}
	changed := false
	err := database.InTransaction(itemCtx, store.database, func(db database.Executor) error {
		if _, err := db.Exec(itemCtx, `SELECT set_config('lock_timeout',$1,true),set_config('statement_timeout',$2,true)`, fmt.Sprintf("%dms", opts.LockTimeout.Milliseconds()), fmt.Sprintf("%dms", opts.ItemTimeout.Milliseconds())); err != nil {
			return err
		}
		if phase == "resources" {
			var raw, remarks string
			if err := db.QueryRow(itemCtx, `SELECT vod_play_url,vod_remarks FROM vod_items WHERE source_key=$1 AND vod_id=$2 AND NOT playback_cleaned FOR UPDATE SKIP LOCKED`, key.Source, key.Vod).Scan(&raw, &remarks); err != nil {
				return err
			}
			clean := playurl.Clean(raw, remarks)
			changed = raw != clean
			if changed {
				if _, err := db.Exec(itemCtx, `UPDATE vod_items SET vod_play_url=$3,total_load_ms=0,success_count=0,failure_count=0,
resource_status=CASE WHEN $3='' THEN 'removed' ELSE resource_status END WHERE source_key=$1 AND vod_id=$2`, key.Source, key.Vod, clean); err != nil {
					return err
				}
			}
			scoped := NewPostgresStore(db)
			item, err := scoped.FindBySourceID(itemCtx, key.Source, key.Vod)
			if err != nil {
				return err
			}
			if item != nil {
				if err := scoped.fillResourceMedia(itemCtx, *item, false); err != nil {
					return err
				}
			}
			// 资料或列表变化只标脏，不在每条资源后重复解析整部作品；读取侧在回填前即时计算。
			if _, err := db.Exec(itemCtx, `UPDATE media SET playback_reconciled_at=NULL WHERE id=(SELECT media_id FROM resource_media_links WHERE source_key=$1 AND vod_id=$2)`, key.Source, key.Vod); err != nil {
				return err
			}
			_, err = db.Exec(itemCtx, `UPDATE vod_items SET playback_cleaned=TRUE WHERE source_key=$1 AND vod_id=$2`, key.Source, key.Vod)
			return err
		}
		predicate := "playback_reconciled_at IS NULL"
		if phase == "history" {
			predicate = "NOT playback_history_repaired"
		}
		var id int
		if err := db.QueryRow(itemCtx, `SELECT id FROM media WHERE id=$1 AND `+predicate+` FOR UPDATE SKIP LOCKED`, key.Media).Scan(&id); err != nil {
			return err
		}
		if phase == "units" {
			if err := NewPostgresStore(db).correctResourceType(itemCtx, id); err != nil {
				return err
			}
			return mediaunits.Reconcile(itemCtx, db, id)
		}
		// 历史阶段显式执行；在线模式永远不删除进度或旧单元。
		if err := mediaunits.Reconcile(itemCtx, db, id); err != nil {
			return err
		}
		if err := mediaunits.RepairFeatureReferences(itemCtx, db, id); err != nil {
			return err
		}
		_, err := db.Exec(itemCtx, `UPDATE media SET playback_history_repaired=TRUE WHERE id=$1`, id)
		return err
	})
	return changed, err
}

func isPlaybackReconcileBusy(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "55P03" || pgErr.Code == "40P01" || pgErr.Code == "57014")
}
