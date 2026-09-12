package mediaunits

import (
	"github.com/TwoThreeWang/Moovie/new/internal/platform/database/testdb"
	"testing"
)

func TestListKeepsMissingLegacyEpisodeInSequence(t *testing.T) {
	pool := testdb.Pool(t)
	testdb.Media(t, pool, 7)
	_, err := pool.Exec(t.Context(), `INSERT INTO media_units(media_id,unit_type,season_number,episode_key,episode_number,has_resource) VALUES (7,'episode',1,'S01E01',1,true),(7,'episode',1,'S01E03',3,true),(7,'episode',1,'S01E02',NULL,false)`)
	if err != nil {
		t.Fatal(err)
	}
	units, err := List(t.Context(), pool, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 3 || units[1].EpisodeKey != "S01E02" || units[1].HasResource {
		t.Fatalf("units=%+v", units)
	}
}
