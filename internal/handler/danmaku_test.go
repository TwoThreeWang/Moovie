package handler

import (
	"testing"
	"time"
)

func TestCnNumToInt(t *testing.T) {
	cases := map[string]int{
		"三": 3, "十": 10, "十二": 12, "二十三": 23, "一百零五": 105,
		"1": 1, "03": 3, "百": 100, "": 0, "abc": 0,
	}
	for in, want := range cases {
		if got := cnNumToInt(in); got != want {
			t.Errorf("cnNumToInt(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseEpisodeNumber(t *testing.T) {
	// 采集站的集名格式很杂，认不出来必须返回 0（按电影处理），不能瞎猜
	cases := map[string]int{
		"第3集": 3, "第03集": 3, "第十二集": 12, "第二十三集": 23, "第100集": 100,
		"第1话": 1, "第1話": 1, "第1期": 1,
		"03": 3, "1": 1, "EP3": 3, "E05": 5, "ep.12": 12,
		"正片": 0, "HD": 0, "HD国语": 0, "蓝光": 0, "": 0, "   ": 0,
	}
	for in, want := range cases {
		if got := parseEpisodeNumber(in); got != want {
			t.Errorf("parseEpisodeNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSplitSeason(t *testing.T) {
	cases := []struct {
		in     string
		season int
		title  string
	}{
		{"庆余年第二季", 2, "庆余年"},
		{"庆余年 第二季", 2, "庆余年"},
		{"庆余年 第2季", 2, "庆余年"},
		{"庆余年第二季（真彩）1080P", 2, "庆余年"},
		{"三体 Season 2", 2, "三体"},
		{"Stranger Things S04", 4, "Stranger Things"},
		{"庆余年", 1, "庆余年"},
		// 下面这些不能被季数正则误伤
		{"漫长的季节", 1, "漫长的季节"},
		{"长安十二时辰", 1, "长安十二时辰"},
		{"第二十条", 1, "第二十条"},
	}
	for _, c := range cases {
		season, title := splitSeason(c.in)
		if season != c.season || title != c.title {
			t.Errorf("splitSeason(%q) = (%d, %q), want (%d, %q)", c.in, season, title, c.season, c.title)
		}
	}
}

func TestToArtplayerDanmaku(t *testing.T) {
	// 重点验证模式映射：弹弹play 1/4/5 → Artplayer 0/2/1
	in := []danmuComment{
		{P: "0.00,1,16777215,[qq]", M: "滚动白字"},
		{P: "12.5,5,16711680,[qq]", M: "顶部红字"},
		{P: "30,4,255,[bili]", M: "底部蓝字"},
		{P: "bad", M: "字段不足应丢弃"},
		{P: "1,1,16777215,[qq]", M: "   "}, // 空文本应丢弃
		{P: "-1,1,16777215,[qq]", M: "负时间应丢弃"},
	}
	got := toArtplayerDanmaku(in)
	if len(got) != 3 {
		t.Fatalf("转换后应剩 3 条，实际 %d 条", len(got))
	}
	want := []DanmakuItem{
		{Text: "滚动白字", Time: 0, Mode: 0, Color: "#FFFFFF"},
		{Text: "顶部红字", Time: 12.5, Mode: 1, Color: "#FF0000"},
		{Text: "底部蓝字", Time: 30, Mode: 2, Color: "#0000FF"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条 = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSampleDanmaku(t *testing.T) {
	mk := func(n int) []DanmakuItem {
		s := make([]DanmakuItem, n)
		for i := range s {
			s[i] = DanmakuItem{Time: float64(i)}
		}
		return s
	}
	if got := len(sampleDanmaku(mk(10), 4000)); got != 10 {
		t.Errorf("不足上限时不应采样，得到 %d", got)
	}
	if got := len(sampleDanmaku(mk(20732), 4000)); got != 4000 {
		t.Errorf("超出上限应采样到 4000，得到 %d", got)
	}
	// 采样必须覆盖到整集，而不是只截前面一段
	s := sampleDanmaku(mk(20732), 4000)
	if s[len(s)-1].Time < 20000 {
		t.Errorf("采样应等间隔覆盖全集，末条 time=%v", s[len(s)-1].Time)
	}
}

func TestBuildVodKey(t *testing.T) {
	// 不同写法的同一集必须落到同一个 key，否则站内弹幕会被切成好几堆
	a := func(raw, ep string) string {
		season, title := splitSeason(raw)
		return buildVodKey(title, season, parseEpisodeNumber(ep))
	}
	if a("庆余年第二季", "第3集") != a("庆余年 第2季", "03") {
		t.Error("同一集的不同写法应归一到同一个 vodKey")
	}
	if a("庆余年第二季", "第3集") == a("庆余年第一季", "第3集") {
		t.Error("不同季不应共用 vodKey")
	}
}

func TestSanitizeDanmakuText(t *testing.T) {
	// 用显式转义写测试数据，避免源码里混进看不见的字符
	cases := map[string]string{
		"正常弹幕":                        "正常弹幕",
		"  前后空格  ":                    "前后空格",
		"多   个    空格":                 "多 个 空格",
		"换行\n注入\r回车":                 "换行 注入 回车",
		"零宽\u200b字符\u200d绕过":          "零宽字符绕过", // 零宽字符是绕关键词过滤的常用手法
		"\u202e方向控制符":                 "方向控制符",  // RLO 能让弹幕反向渲染
		"\x00\x1f控制字符":                "控制字符",
		"   ":                         "",
		"\u200b\u200d\u200c":           "", // 纯零宽字符等于空弹幕，清空后会被上层挡掉
	}
	for in, want := range cases {
		if got := sanitizeDanmakuText(in); got != want {
			t.Errorf("sanitizeDanmakuText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHexColorPattern(t *testing.T) {
	ok := []string{"#FFFFFF", "#000000", "#FF0000", "#1A2B3C"}
	bad := []string{"#fff", "FFFFFF", "#GGGGGG", "red", "", "#FFFFFF;background:url(x)"}
	for _, s := range ok {
		if !reHexColor.MatchString(s) {
			t.Errorf("%q 应通过颜色校验", s)
		}
	}
	for _, s := range bad {
		if reHexColor.MatchString(s) {
			t.Errorf("%q 不应通过颜色校验", s)
		}
	}
}

func TestIPLimiter(t *testing.T) {
	l := newIPLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("第 %d 次不该被限流", i+1)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Error("超出配额后应被限流")
	}
	if !l.Allow("5.6.7.8") {
		t.Error("不同 IP 之间不应互相影响")
	}
}
