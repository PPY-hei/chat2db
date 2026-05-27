-- 回滚同步任务字段

DROP INDEX idx_tasks_target_conn_id ON tasks;

ALTER TABLE tasks DROP COLUMN dest_table;
ALTER TABLE tasks DROP COLUMN dest_schema;
ALTER TABLE tasks DROP COLUMN dest_database;
ALTER TABLE tasks DROP COLUMN target_conn_id;
