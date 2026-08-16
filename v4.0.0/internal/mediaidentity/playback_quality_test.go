package mediaidentity

import (
	"strings"
	"testing"
)

func TestRecordPlaybackEventIsCandidateBoundAndIdempotent(t *testing.T) {
	executor := &identityFoundationExecutor{}
	store := NewPostgresStore(executor)
	accepted, err := store.RecordPlaybackEvent(t.Context(), PlaybackAttemptEvent{
		AttemptID: "attempt-123456", CandidateSessionID: "session-123456", EventType: "fatal_error", CandidateID: 71,
		MediaUnitID: 51, SourceKey: "source", VodID: "42", ElapsedMs: 30000, Reason: "manifest timeout",
	})
	if err != nil || !accepted {
		t.Fatalf("accepted/error = %v/%v", accepted, err)
	}
	query := executor.execQueries[0]
	for _, expected := range []string{
		"candidate.id = $3 AND candidate.media_unit_id = $4",
		"line.source_key = $5 AND line.vod_id = $6",
		"ON CONFLICT (attempt_id, event_type) DO NOTHING",
		"INSERT INTO playback_quality_rollups",
		"candidate_session_id",
		"UPDATE vod_items resource",
		"last_played_at = CASE WHEN inserted.event_type IN ('played_10s', 'ended')",
		"resource.resource_status = 'cold' THEN 'active'",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("event query missing %q: %s", expected, query)
		}
	}
	arguments := executor.execArguments[0]
	if len(arguments) != 10 || arguments[0] != "attempt-123456" || arguments[8] != "timeout" || arguments[9] != "session-123456" {
		t.Fatalf("event arguments = %#v", arguments)
	}
}

func TestRecordPlaybackEventRejectsUnboundIdentity(t *testing.T) {
	store := NewPostgresStore(&identityFoundationExecutor{})
	if _, err := store.RecordPlaybackEvent(t.Context(), PlaybackAttemptEvent{AttemptID: "short"}); err == nil {
		t.Fatal("invalid playback event was accepted")
	}
	for input, expected := range map[string]string{
		"segment timeout": "timeout", "manifest fetch": "network", "codec decode": "decode",
		"HTTP 403": "http", "other": "unknown",
	} {
		if actual := playbackFailureClass(input); actual != expected {
			t.Errorf("playbackFailureClass(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestResourceCandidateQueryUsesExactCandidateRollups(t *testing.T) {
	for _, expected := range []string{
		"SELECT candidate.id, line.id",
		"playback_quality_rollups",
		"WHERE candidate_id = candidate.id",
		"INTERVAL '7 days'",
		"resource_media_links",
	} {
		if !strings.Contains(resourceCandidateSelect, expected) {
			t.Fatalf("candidate query missing %q: %s", expected, resourceCandidateSelect)
		}
	}
}
