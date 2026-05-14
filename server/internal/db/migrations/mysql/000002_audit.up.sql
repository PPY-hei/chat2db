-- Audit log table for Chat2DB.
-- Captures business events (SQL execution, member/role changes, connection
-- CRUD, login/logout) for traceability. Retention is handled by a background
-- purger goroutine in the application; not enforced by the database.

CREATE TABLE IF NOT EXISTS `audit_logs` (
    `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `created_at`  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `user_id`     BIGINT UNSIGNED NULL,
    `user_email`  VARCHAR(191) NOT NULL,
    `action`      VARCHAR(64)  NOT NULL,
    `group_id`    BIGINT UNSIGNED NULL,
    `conn_id`     BIGINT UNSIGNED NULL,
    `target`      VARCHAR(255) NOT NULL DEFAULT '',
    `detail`      LONGTEXT     NOT NULL,
    `success`     TINYINT(1)   NOT NULL,
    `duration_ms` BIGINT       NOT NULL DEFAULT 0,
    `error_msg`   VARCHAR(512) NOT NULL DEFAULT '',
    `ip`          VARCHAR(64)  NOT NULL DEFAULT '',
    `user_agent`  VARCHAR(255) NOT NULL DEFAULT '',
    PRIMARY KEY (`id`),
    -- 主查询路径：按 group + 时间窗口倒序拉取 → (group_id, created_at)
    KEY `idx_audit_group_created` (`group_id`, `created_at`),
    -- 指定 action 的过滤路径 → (group_id, action, created_at)
    KEY `idx_audit_group_action_created` (`group_id`, `action`, `created_at`),
    -- 无组事件（auth.*）按时间倒序兜底
    KEY `idx_audit_created_at` (`created_at`),
    -- "我的事件" / 自身 auth.* 事件路径
    KEY `idx_audit_user_id`    (`user_id`),
    -- 单个连接的事件回溯
    KEY `idx_audit_conn_id`    (`conn_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
