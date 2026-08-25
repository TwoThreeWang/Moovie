ALTER TABLE feedbacks DROP CONSTRAINT IF EXISTS feedbacks_type_check;

ALTER TABLE feedbacks ADD CONSTRAINT feedbacks_type_check
CHECK (type IN ('bug', 'request', 'suggestion', 'dmca', '系统告警'));
