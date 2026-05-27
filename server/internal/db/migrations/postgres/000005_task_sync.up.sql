-- 为任务表添加同步任务支持（表结构同步 / 数据同步）

-- 添加目标连接字段
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS target_conn_id BIGINT NOT NULL DEFAULT 0;

-- 添加目标数据库/schema/表字段（用于同步任务）
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS dest_database VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS dest_schema VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS dest_table VARCHAR(128) NOT NULL DEFAULT '';

-- 为目标连接添加索引
CREATE INDEX IF NOT EXISTS idx_tasks_target_conn_id ON tasks(target_conn_id);
