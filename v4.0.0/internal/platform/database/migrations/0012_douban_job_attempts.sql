ALTER TABLE douban_sync_jobs
ADD COLUMN attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0);
