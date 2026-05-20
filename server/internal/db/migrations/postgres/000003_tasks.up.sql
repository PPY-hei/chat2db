-- 异步任务（导入 / 导出）。
-- 用 application-level 异步 worker 异步执行，本表仅做状态/进度跟踪与可追溯。

CREATE TABLE IF NOT EXISTS tasks (
    id               BIGSERIAL    PRIMARY KEY,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    group_id         BIGINT       NOT NULL,
    conn_id          BIGINT       NOT NULL,

    kind             VARCHAR(16)  NOT NULL,
    scope            VARCHAR(16)  NOT NULL,

    target_database  VARCHAR(128) NOT NULL DEFAULT '',
    target_schema    VARCHAR(128) NOT NULL DEFAULT '',
    target_table     VARCHAR(128) NOT NULL DEFAULT '',

    status           VARCHAR(16)  NOT NULL,
    progress         INTEGER      NOT NULL DEFAULT 0,

    processed_rows   BIGINT       NOT NULL DEFAULT 0,
    total_rows       BIGINT       NOT NULL DEFAULT 0,
    total_tables     INTEGER      NOT NULL DEFAULT 0,
    done_tables      INTEGER      NOT NULL DEFAULT 0,

    file_path        VARCHAR(512) NOT NULL DEFAULT '',
    file_size        BIGINT       NOT NULL DEFAULT 0,

    error_msg        VARCHAR(1024) NOT NULL DEFAULT '',
    cancel_requested BOOLEAN      NOT NULL DEFAULT FALSE,

    created_by_id    BIGINT       NOT NULL,
    creator_name     VARCHAR(128) NOT NULL DEFAULT '',

    started_at       TIMESTAMPTZ  NULL,
    finished_at      TIMESTAMPTZ  NULL
);

-- 主查询路径：按组列任务
CREATE INDEX IF NOT EXISTS idx_tasks_group_created    ON tasks(group_id, created_at);
-- 按创建人查
CREATE INDEX IF NOT EXISTS idx_tasks_creator_created  ON tasks(created_by_id, created_at);
-- 状态筛选
CREATE INDEX IF NOT EXISTS idx_tasks_status           ON tasks(status);
-- 类型筛选
CREATE INDEX IF NOT EXISTS idx_tasks_kind             ON tasks(kind);
-- 单连接定位
CREATE INDEX IF NOT EXISTS idx_tasks_conn_id          ON tasks(conn_id);
-- 兜底时间倒序
CREATE INDEX IF NOT EXISTS idx_tasks_created_at       ON tasks(created_at);
