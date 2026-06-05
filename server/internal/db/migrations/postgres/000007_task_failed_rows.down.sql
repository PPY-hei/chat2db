-- 回滚数据同步任务失败/跳过行数统计字段。

ALTER TABLE tasks DROP COLUMN IF EXISTS failed_rows;
