// Package collection 是片单：一组按顺序排列、每条带一句推荐语的影片。
//
// 涉及的表：collections（片单本体）、collection_items（条目）、media（条目指向的影片身份）。
// owner_user_id 为 NULL 的是官方精选，非 NULL 的是用户自建；两者同表，
// 靠 featured 决定进不进发现流和 sitemap，所以开放用户投稿时不用改结构。
package collection

import (
	"regexp"
	"strings"
	"time"
)

// slugPattern 限制片单地址只能用小写字母、数字和连字符，避免 URL 里出现需要转义的字符。
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,58}[a-z0-9])?$`)

// Collection 是一个片单。OwnerUserID 为 0 表示官方精选。
type Collection struct {
	ID          int
	OwnerUserID int
	Slug        string
	Title       string
	Description string
	Featured    bool
	ItemCount   int
	UpdatedAt   time.Time
	Related     []RelatedInput
	Covers      []string // 前几张海报，用于列表页拼封面，不入库
}

// Official 判断是不是官方片单。
func (c Collection) Official() bool { return c.OwnerUserID == 0 }

// RelatedInput 的顺序就是前台推荐顺序，关系只从当前片单指向目标。
type RelatedInput struct {
	ID     int
	Reason string
}

type RelatedCollection struct {
	Collection
	Reason string
}

// Item 是片单里的一条影片，Note 是这一条的推荐语。
type Item struct {
	MediaID  int
	DoubanID string
	Title    string
	Poster   string
	Year     string
	Position int
	Note     string
}

// ItemInput 是保存片单时提交的一条，按豆瓣 ID 定位影片。
type ItemInput struct {
	DoubanID string
	Note     string
}

// ValidSlug 判断片单地址是否合法。
func ValidSlug(slug string) bool { return slugPattern.MatchString(slug) }

// ParseItems 解析后台的条目文本，每行一条：豆瓣ID | 推荐语。
// 用最笨的按行解析而不是动态表单，是因为策展时批量粘贴比逐条点按钮快得多。
// 返回的顺序就是条目顺序；空行和重复的豆瓣 ID 会被跳过。
func ParseItems(text string) []ItemInput {
	items := make([]ItemInput, 0)
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// 先把全角竖线归一成半角：它是多字节字符，按字节索引切会把它切碎。
		line = strings.ReplaceAll(line, "｜", "|")
		doubanID, note := line, ""
		if separator := strings.Index(line, "|"); separator >= 0 {
			doubanID = strings.TrimSpace(line[:separator])
			note = strings.TrimSpace(line[separator+1:])
		}
		if doubanID == "" || seen[doubanID] {
			continue
		}
		seen[doubanID] = true
		items = append(items, ItemInput{DoubanID: doubanID, Note: note})
	}
	return items
}

// FormatItems 把条目还原成后台文本框的内容，供编辑时回填。
func FormatItems(items []Item) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := item.DoubanID
		if item.Note != "" {
			line += " | " + item.Note
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
