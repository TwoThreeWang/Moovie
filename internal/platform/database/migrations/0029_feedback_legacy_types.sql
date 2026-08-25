-- 旧库 feedbacks.type 存在新库枚举未覆盖的历史取值：'error' 与 '播放失败反馈'。
-- 数据迁移必须保留原始语义，因此扩展允许集合，而不是把旧值改写成近似值。
-- 与 0011 相同的做法：先摘掉旧约束再重建，避免重复执行时冲突。
ALTER TABLE feedbacks DROP CONSTRAINT IF EXISTS feedbacks_type_check;

ALTER TABLE feedbacks ADD CONSTRAINT feedbacks_type_check
CHECK (type IN ('bug', 'request', 'suggestion', 'dmca', '系统告警', 'error', '播放失败反馈'));
