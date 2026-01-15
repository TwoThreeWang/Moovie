-- 增加用户名列
ALTER TABLE users ADD COLUMN username VARCHAR(255);

-- 为现有用户设置默认用户名（取邮箱前缀）
UPDATE users SET username = split_part(email, '@', 1) WHERE username IS NULL;

-- 设置为非空且创建索引（如果需要）
ALTER TABLE users ALTER COLUMN username SET NOT NULL;
CREATE INDEX idx_users_username ON users(username);
