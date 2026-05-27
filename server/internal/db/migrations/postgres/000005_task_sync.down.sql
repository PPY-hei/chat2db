-- 回滚同步任务字段

DROP INDEX IF EXISTS idx_tasks_target_conn_id;

ALTER TABLE tasks DROP COLUMN IF EXISTS dest_table;
ALTER TABLE tasks DROP COLUMN IF EXISTS dest_schema;
ALTER TABLE tasks DROP COLUMN IF EXISTS dest_database;
ALTER TABLE tasks DROP COLUMN IF EXISTS target_conn_id;
