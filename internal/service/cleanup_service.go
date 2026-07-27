package service

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/user/moovie/internal/model"
	"github.com/user/moovie/internal/repository"
	"github.com/user/moovie/internal/utils"
)

const (
	// siteStatRetentionDays 采集健康度统计保留天数
	siteStatRetentionDays = 7
	// siteAlertMinSamples 低于这个采样量不告警，避免单次抖动误报
	siteAlertMinSamples = 5
	// siteAlertCooldown 同一站点两次告警的最小间隔，避免刷屏
	siteAlertCooldown = 24 * time.Hour
	// SiteAlertFeedbackType 告警写入 feedback 表时使用的类型
	SiteAlertFeedbackType = "系统告警"
)

// CleanupService 清理服务
type CleanupService struct {
	repos  *repository.Repositories
	cancel context.CancelFunc

	alertMu   sync.Mutex
	lastAlert map[string]time.Time
}

// NewCleanupService 创建清理服务
func NewCleanupService(repos *repository.Repositories) *CleanupService {
	return &CleanupService{
		repos:     repos,
		lastAlert: make(map[string]time.Time),
	}
}

// Start 启动定时清理任务
func (s *CleanupService) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	ticker := time.NewTicker(24 * time.Hour)
	// 采集健康度检查要比清理频繁得多，否则站点挂了要等一天才知道
	alertTicker := time.NewTicker(time.Hour)

	// 启动时先运行一次
	utils.GoSafe(0, func(ctx context.Context) {
		s.runCleanup()
	})

	go func() {
		defer ticker.Stop()
		defer alertTicker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runCleanup()
			case <-alertTicker.C:
				s.checkSiteHealth()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// Stop 停止定时清理任务
func (s *CleanupService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *CleanupService) runCleanup() {
	log.Println("[CleanupService] 开始清理过期数据...")

	// 1. 清理 10 天内无人访问的视频详情
	affected, err := s.repos.VodItem.DeleteInactive(10)
	if err != nil {
		log.Printf("[CleanupService] 清理视频详情失败: %v", err)
	} else {
		log.Printf("[CleanupService] 已清理 %d 条过期视频详情", affected)
	}

	// 2. 清理超过 30 天未搜索的热搜关键词
	cleanedKeywords, err := s.repos.SearchLog.DeleteOldKeywords(30)
	if err != nil {
		log.Printf("[CleanupService] 清理旧热搜关键词失败: %v", err)
	} else if cleanedKeywords > 0 {
		log.Printf("[CleanupService] 已清理 %d 条超过 30 天未搜索的热搜关键词", cleanedKeywords)
	}

	// 3. 清理超过 30 天的原始搜索日志
	cleanedLogs, err := s.repos.SearchLog.DeleteOldLogs(30)
	if err != nil {
		log.Printf("[CleanupService] 清理旧搜索日志失败: %v", err)
	} else if cleanedLogs > 0 {
		log.Printf("[CleanupService] 已清理 %d 条超过 30 天的原始搜索日志", cleanedLogs)
	}

	// 4. 清理过期的采集健康度统计
	before := time.Now().AddDate(0, 0, -siteStatRetentionDays)
	cleanedStats, err := s.repos.SiteStat.DeleteBefore(before)
	if err != nil {
		log.Printf("[CleanupService] 清理采集统计失败: %v", err)
	} else if cleanedStats > 0 {
		log.Printf("[CleanupService] 已清理 %d 条超过 %d 天的采集统计", cleanedStats, siteStatRetentionDays)
	}
}

// checkSiteHealth 检查各采集站点最近 1 小时的健康度，异常时写一条系统告警到 feedback 表。
// 不依赖任何外部告警系统，管理员在后台反馈页即可看到。
func (s *CleanupService) checkSiteHealth() {
	sites, err := s.repos.Site.ListEnabled()
	if err != nil {
		log.Printf("[CleanupService] 健康检查获取站点失败: %v", err)
		return
	}
	if len(sites) == 0 {
		return
	}

	summaries, err := s.repos.SiteStat.SummarySince(time.Now().Add(-time.Hour))
	if err != nil {
		log.Printf("[CleanupService] 健康检查获取统计失败: %v", err)
		return
	}

	for _, site := range sites {
		summary := summaries[site.Key]
		if summary == nil || summary.Total() < siteAlertMinSamples {
			continue
		}

		var reason string
		switch {
		case summary.EmptyRate() > 98:
			// 空返回率畸高通常意味着对方改了接口结构：HTTP 仍是 200、JSON 也能解析，
			// 但字段名对不上导致解析结果恒为空。只看成功率发现不了这种静默失效。
			reason = fmt.Sprintf("空返回率高达 %.0f%%，接口结构可能已变更", summary.EmptyRate())
		case summary.OKRate() == 0:
			reason = fmt.Sprintf("成功率为 0%%（超时 %d 次、错误 %d 次）",
				summary.TimeoutCount, summary.ErrorCount)
		default:
			continue
		}

		if !s.shouldAlert(site.Key) {
			continue
		}

		content := fmt.Sprintf("采集站点 %s 最近 1 小时异常：%s。样本 %d 次，平均耗时 %dms。",
			site.Key, reason, summary.Total(), summary.AvgMs())

		if err := s.repos.Feedback.Create(&model.Feedback{
			Type:    SiteAlertFeedbackType,
			Content: content,
		}); err != nil {
			log.Printf("[CleanupService] 写入站点告警失败: %v", err)
			continue
		}
		log.Printf("[CleanupService] 已生成站点告警: %s", content)
	}
}

// shouldAlert 冷却判断，同一站点在 siteAlertCooldown 内只告警一次
func (s *CleanupService) shouldAlert(siteKey string) bool {
	s.alertMu.Lock()
	defer s.alertMu.Unlock()

	if last, ok := s.lastAlert[siteKey]; ok && time.Since(last) < siteAlertCooldown {
		return false
	}
	s.lastAlert[siteKey] = time.Now()
	return true
}
