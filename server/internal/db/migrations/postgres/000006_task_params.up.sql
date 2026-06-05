-- 为异步任务添加扩展参数字段，承载导出格式、筛选条件等任务特有选项。

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS params TEXT;
