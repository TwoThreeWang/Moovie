package catalog

import "testing"

func TestTitleFinderBridgesCatalogToTVBoxFallback(t *testing.T) {
	store := NewMemoryStore()
	_ = store.Upsert(t.Context(), Movie{DoubanID: "1292052", Title: "肖申克的救赎"})
	title, err := NewTitleFinder(store).FindTitleByDoubanID(t.Context(), "1292052")
	if err != nil || title != "肖申克的救赎" {
		t.Fatalf("title/error = %q/%v", title, err)
	}
	missing, err := NewTitleFinder(store).FindTitleByDoubanID(t.Context(), "missing")
	if err != nil || missing != "" {
		t.Fatalf("missing/error = %q/%v", missing, err)
	}
}
