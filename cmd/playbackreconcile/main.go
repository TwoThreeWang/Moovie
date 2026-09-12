// playbackreconcile 默认检查；-apply 在线回填，历史合并需要单独维护阶段。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TwoThreeWang/Moovie/new/internal/platform/config"
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database"
	"github.com/TwoThreeWang/Moovie/new/internal/search"
)

func main() {
	target := flag.String("target-env", "", "目标数据库配置文件")
	phase := flag.String("phase", "online", "online、resources、units 或 history")
	apply := flag.Bool("apply", false, "写入；默认在线模式不合并历史、不批量排队豆瓣采集")
	maintenance := flag.Bool("maintenance", false, "确认已经停止进度写入，仅 history -apply 需要")
	limit := flag.Int("limit", 0, "本次最多处理多少条资源/作品；0 不限，重跑自动跳过已完成记录")
	delay := flag.Duration("delay", 100*time.Millisecond, "每条处理后的休息时间")
	lockTimeout := flag.Duration("lock-timeout", 100*time.Millisecond, "单次锁等待上限；繁忙记录留待下次")
	itemTimeout := flag.Duration("item-timeout", 3*time.Second, "单条事务总超时")
	timeout := flag.Duration("timeout", time.Hour, "本次总超时；已提交进度保存在数据库")
	flag.Parse()
	if *target == "" {
		fatal(fmt.Errorf("必须提供 -target-env"))
	}
	if *phase == "history" && *apply && !*maintenance {
		fatal(fmt.Errorf("history 写入须在停止进度写入后提供 -maintenance"))
	}
	cfg, err := config.DatabaseConfigFromDotEnv(*target)
	if err != nil {
		fatal(err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, *timeout)
	defer cancel()
	pool, err := database.Connect(ctx, cfg.DSN(), 1)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()
	fmt.Printf("数据库=%s 阶段=%s 写入=%t 每条休息=%s 单条超时=%s\n", cfg.Name, *phase, *apply, *delay, *itemTimeout)
	lastPrint := time.Now()
	progress := func(r search.PlaybackReconcileReport) {
		if time.Since(lastPrint) >= 5*time.Second {
			printReport(r)
			lastPrint = time.Now()
		}
	}
	report, err := search.NewPostgresStore(pool).ReconcilePlayback(ctx, search.PlaybackReconcileOptions{
		Phase: *phase, Apply: *apply, Maintenance: *maintenance, Limit: *limit, Delay: *delay, LockTimeout: *lockTimeout, ItemTimeout: *itemTimeout, Progress: progress,
	})
	printReport(report)
	// 到时和停止信号是在线回填的正常收尾，不是失败；已提交的批次不会回滚。
	if err != nil && ctx.Err() == nil {
		fatal(err)
	}
	switch {
	case ctx.Err() != nil:
		fmt.Println("到达总超时或收到停止信号；已提交进度留在数据库，重跑相同命令继续。")
	case report.Remaining && *apply:
		fmt.Println("仍有未完成记录（限量、繁忙或在线新增）；重跑相同命令继续，无需重置进度。")
	}
}
func printReport(r search.PlaybackReconcileReport) {
	fmt.Printf("阶段=%s 检查=%d 列表变化=%d 已提交=%d 跳过=%d 限量暂停=%t 仍有待办=%t\n", r.Phase, r.Checked, r.Changed, r.Completed, r.Skipped, r.Limited, r.Remaining)
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
