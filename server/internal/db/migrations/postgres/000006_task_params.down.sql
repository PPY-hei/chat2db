-- 回滚任务扩展参数字段。

ALTER TABLE tasks DROP COLUMN IF EXISTS params;
