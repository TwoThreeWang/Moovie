-- 上游限流不该消耗任务的重试预算，否则一次 429 风暴会把整批任务判成 failed。
-- 限流重试单独计数，既能退还 attempt，又不至于在上游永久拒绝时无限重排。
ALTER TABLE worker_jobs ADD COLUMN throttle_count INTEGER NOT NULL DEFAULT 0;
