package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TwoThreeWang/Moovie/new/internal/mediaidentity"
)

func TestSyncCanonicalWritesMediaExternalIDsAndSnapshot(t *testing.T) {
	writer := &canonicalWriterStub{}
	_, err := syncCanonical(t.Context(), writer, Movie{
		DoubanID: "1292052", Title: "肖申克的救赎", OriginalTitle: "The Shawshank Redemption",
		Year: "1994", Rating: 9.7, Poster: "poster", Summary: "简介",
	}, "movie", "douban", map[string]any{"id": "1292052"}, mediaidentity.ExternalID{Provider: "douban", ExternalID: "1292052", IsPrimary: true})
	if err != nil {
		t.Fatal(err)
	}
	if writer.media.ID != 42 || writer.media.DoubanID != "1292052" || writer.media.RatingDouban != 9.7 {
		t.Fatalf("canonical media = %+v", writer.media)
	}
	if len(writer.externalIDs) != 1 || writer.externalIDs[0].MediaID != 42 || writer.externalIDs[0].Provider != "douban" || writer.externalIDs[0].ExternalType != "movie" {
		t.Fatalf("external IDs = %+v", writer.externalIDs)
	}
	var payload map[string]any
	if err := json.Unmarshal(writer.snapshot, &payload); err != nil || payload["id"] != "1292052" || writer.snapshotProvider != "douban" {
		t.Fatalf("snapshot = %s/%s", writer.snapshotProvider, writer.snapshot)
	}
}

type canonicalWriterStub struct {
	media            mediaidentity.Media
	externalIDs      []mediaidentity.ExternalID
	aliases          []mediaidentity.Alias
	snapshotProvider string
	snapshot         []byte
}

func (writer *canonicalWriterStub) UpsertAlias(_ context.Context, alias mediaidentity.Alias) error {
	writer.aliases = append(writer.aliases, alias)
	return nil
}

func (writer *canonicalWriterStub) Upsert(_ context.Context, media mediaidentity.Media) (mediaidentity.Media, error) {
	media.ID = 42
	writer.media = media
	return media, nil
}

func (writer *canonicalWriterStub) UpsertExternalID(_ context.Context, external mediaidentity.ExternalID) error {
	writer.externalIDs = append(writer.externalIDs, external)
	return nil
}

func (writer *canonicalWriterStub) WriteSourceSnapshot(_ context.Context, _ int, provider string, payload []byte, _ bool, _ string) error {
	writer.snapshotProvider = provider
	writer.snapshot = append([]byte(nil), payload...)
	return nil
}
