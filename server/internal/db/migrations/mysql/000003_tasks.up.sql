-- 异步任务（导入 / 导出）。
-- 用 application-level 异步 worker 异步执行，本表仅做状态/进度跟踪与可追溯。

CREATE TABLE IF NOT EXISTS `tasks` (
    `id`               BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `created_at`       DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at`       DATETIME(6)     NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),

    `group_id`         BIGINT UNSIGNED NOT NULL,
    `conn_id`          BIGINT UNSIGNED NOT NULL,

    `kind`             VARCHAR(16)     NOT NULL,
    `scope`            VARCHAR(16)     NOT NULL,

    `target_database`  VARCHAR(128)    NOT NULL DEFAULT '',
    `target_schema`    VARCHAR(128)    NOT NULL DEFAULT '',
    `target_table`     VARCHAR(128)    NOT NULL DEFAULT '',

    `status`           VARCHAR(16)     NOT NULL,
    `progress`         INT             NOT NULL DEFAULT 0,

    `processed_rows`   BIGINT          NOT NULL DEFAULT 0,
    `total_rows`       BIGINT          NOT NULL DEFAULT 0,
    `total_tables`     INT             NOT NULL DEFAULT 0,
    `done_tables`      INT             NOT NULL DEFAULT 0,

    `file_path`        VARCHAR(512)    NOT NULL DEFAULT '',
    `file_size`        BIGINT          NOT NULL DEFAULT 0,

    `error_msg`        VARCHAR(1024)   NOT NULL DEFAULT '',
    `cancel_requested` TINYINT(1)      NOT NULL DEFAULT 0,

    `created_by_id`    BIGINT UNSIGNED NOT NULL,
    `creator_name`     VARCHAR(128)    NOT NULL DEFAULT '',

    `started_at`       DATETIME(6)     NULL,
    `finished_at`      DATETIME(6)     NULL,

    PRIMARY KEY (`id`),
    KEY `idx_tasks_group_created`   (`group_id`, `created_at`),
    KEY `idx_tasks_creator_created` (`created_by_id`, `created_at`),
    KEY `idx_tasks_status`          (`status`),
    KEY `idx_tasks_kind`            (`kind`),
    KEY `idx_tasks_conn_id`         (`conn_id`),
    KEY `idx_tasks_created_at`      (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
