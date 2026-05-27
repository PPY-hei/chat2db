# 数据库同步功能

## 功能概述

Chat2DB 现在支持两种数据库同步任务：

1. **表结构同步 (schema_sync)**: 对比源表和目标表的结构差异，自动生成并执行 DDL 语句同步字段类型、索引等
2. **表数据同步 (data_sync)**: 从源表读取所有数据，批量插入到目标表（仅同步匹配的列）

## 使用方法

### 前端操作

1. 打开任务列表页面
2. 点击"新建同步"按钮
3. 选择任务类型（表结构同步 / 表数据同步）
4. 选择连接组
5. 配置源连接：
   - 选择源连接
   - 选择源数据库
   - 选择源 Schema
   - 选择源表
6. 配置目标连接：
   - 选择目标连接
   - 选择目标数据库
   - 选择目标 Schema
   - 选择目标表
7. 提交任务

### API 调用

```bash
# 创建表结构同步任务
curl -X POST http://localhost:8080/api/tasks \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "group_id": 1,
    "conn_id": 1,
    "target_conn_id": 2,
    "kind": "schema_sync",
    "scope": "table",
    "target_database": "source_db",
    "target_schema": "public",
    "target_table": "users",
    "dest_database": "target_db",
    "dest_schema": "public",
    "dest_table": "users"
  }'

# 创建表数据同步任务
curl -X POST http://localhost:8080/api/tasks \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "group_id": 1,
    "conn_id": 1,
    "target_conn_id": 2,
    "kind": "data_sync",
    "scope": "table",
    "target_database": "source_db",
    "target_schema": "public",
    "target_table": "users",
    "dest_database": "target_db",
    "dest_schema": "public",
    "dest_table": "users"
  }'
```

## 支持的数据库

- PostgreSQL
- MySQL

## 表结构同步详情

表结构同步会执行以下操作：

1. **表不存在**: 自动创建目标表，包括所有列和主键
2. **列差异**:
   - 添加缺失的列
   - 修改类型不匹配的列
3. **索引差异**:
   - 创建缺失的索引（不包括主键）

### 注意事项

- 不会删除目标表中多余的列或索引
- 列类型比较会忽略长度和精度差异
- 主键通过列定义处理，不单独创建

## 表数据同步详情

表数据同步会执行以下操作：

1. 读取源表的所有列定义
2. 读取目标表的所有列定义
3. 按列名匹配（大小写敏感）
4. 批量读取源表数据（每批 1000 行）
5. 批量插入到目标表

### 注意事项

- 仅同步列名匹配的列
- 不会清空目标表，直接追加数据
- 如果目标表有唯一约束，可能导致插入失败
- 建议在同步前备份目标表数据

## 数据库迁移

首次使用需要运行数据库迁移：

```bash
# 迁移会自动在应用启动时执行
# 或手动执行迁移
cd server
go run cmd/migrate/main.go up
```

迁移文件位置：
- PostgreSQL: `server/internal/db/migrations/postgres/000005_task_sync.up.sql`
- MySQL: `server/internal/db/migrations/mysql/000005_task_sync.up.sql`

## 任务监控

- 任务状态：pending（排队中）、running（执行中）、succeeded（成功）、failed（失败）、canceled（已取消）
- 进度显示：0-100%
- 可以在任务列表中查看详细信息和错误消息
- 支持取消正在执行的任务

## 性能优化

- 数据同步采用批量插入（每批 1000 行）
- 每 500 行检查一次取消信号
- 支持大表同步（无行数限制）

## 故障排查

### 常见错误

1. **"source driver not supported"**: 源数据库驱动不支持，目前仅支持 PostgreSQL 和 MySQL
2. **"no matching columns"**: 源表和目标表没有匹配的列名
3. **"table does not exist"**: 表不存在（表结构同步会自动创建）
4. **"insert batch failed"**: 插入失败，可能是约束冲突或数据类型不兼容

### 日志查看

服务端日志会记录详细的任务执行信息：

```bash
# 查看任务日志
tail -f server/logs/app.log | grep "task"
```

## 限制

- 当前版本仅支持单表同步（scope=table）
- 不支持跨数据库类型同步（如 PostgreSQL → MySQL）
- 不支持视图、存储过程、触发器等对象的同步
- 表结构同步不会删除目标表中多余的列或索引
