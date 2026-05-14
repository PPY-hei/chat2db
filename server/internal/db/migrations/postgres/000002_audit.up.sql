-- Audit log table for Chat2DB.
-- Captures business events (SQL execution, member/role changes, connection
-- CRUD, login/logout) for traceability. Retention is handled by a background
-- purger goroutine in the application; not enforced by the database.

CREATE TABLE IF NOT EXISTS audit_logs (
    id          BIGSERIAL PRIMARY KEY,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    user_id     BIGINT       NULL,
    user_email  VARCHAR(191) NOT NULL,
    action      VARCHAR(64)  NOT NULL,
    group_id    BIGINT       NULL,
    conn_id     BIGINT       NULL,
    target      VARCHAR(255) NOT NULL DEFAULT '',
    detail      TEXT         NOT NULL,
    success     BOOLEAN      NOT NULL,
    duration_ms BIGINT       NOT NULL DEFAULT 0,
    error_msg   VARCHAR(512) NOT NULL DEFAULT '',
    ip          VARCHAR(64)  NOT NULL DEFAULT '',
    user_agent  VARCHAR(255) NOT NULL DEFAULT ''
);

-- 主查询路径：按 group + 时间窗口倒序拉取
CREATE INDEX IF NOT EXISTS idx_audit_group_created        ON audit_logs(group_id, created_at);
-- 指定 action 的过滤路径
CREATE INDEX IF NOT EXISTS idx_audit_group_action_created ON audit_logs(group_id, action, created_at);
-- 无组事件（auth.*）按时间倒序兜底
CREATE INDEX IF NOT EXISTS idx_audit_created_at           ON audit_logs(created_at);
-- "我的事件" / 自身 auth.* 事件路径
CREATE INDEX IF NOT EXISTS idx_audit_user_id              ON audit_logs(user_id);
-- 单个连接的事件回溯
CREATE INDEX IF NOT EXISTS idx_audit_conn_id              ON audit_logs(conn_id);
