-- 为数据同步任务增加失败/跳过行数统计。

ALTER TABLE tasks ADD COLUMN failed_rows BIGINT NOT NULL DEFAULT 0;
