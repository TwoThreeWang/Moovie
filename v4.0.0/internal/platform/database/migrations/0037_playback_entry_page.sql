ALTER TABLE playback_positions
    ADD COLUMN IF NOT EXISTS entry_page TEXT NOT NULL DEFAULT 'play';

ALTER TABLE playback_positions
    DROP CONSTRAINT IF EXISTS playback_positions_entry_page_check;

ALTER TABLE playback_positions
    ADD CONSTRAINT playback_positions_entry_page_check
    CHECK (entry_page IN ('play', 'watch'));
