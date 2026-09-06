package collection

import (
	"reflect"
	"testing"
)

func TestParseItemsHandlesSeparatorsBlanksAndDuplicates(t *testing.T) {
	got := ParseItems("1292052 | 希望是好东西\n\n1291546｜哥哥\n 1292720 \n1292052 | 重复的会被丢掉\n|只有推荐语没有ID\n")
	want := []ItemInput{
		{DoubanID: "1292052", Note: "希望是好东西"},
		{DoubanID: "1291546", Note: "哥哥"}, // 全角竖线不能把中文切碎
		{DoubanID: "1292720", Note: ""},
		{DoubanID: "1292052", Note: "重复的会被丢掉"},
	}
	want = want[:3] // 重复的豆瓣 ID 只保留第一次出现
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseItems() = %#v, want %#v", got, want)
	}
}

func TestValidSlugRejectsUnsafeAddresses(t *testing.T) {
	for _, slug := range []string{"best-mystery-2024", "a1", "x"} {
		if !ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = false, 应当合法", slug)
		}
	}
	for _, slug := range []string{"", "-lead", "trail-", "Upper", "with space", "斜杠/在里面", "with_underscore"} {
		if ValidSlug(slug) {
			t.Errorf("ValidSlug(%q) = true, 应当拒绝", slug)
		}
	}
}
