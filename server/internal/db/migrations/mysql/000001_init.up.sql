-- Chat2DB metadata schema v1 (MySQL).
-- Mirrors server/internal/model/model.go. Changes here MUST be paired with
-- a matching PostgreSQL migration under migrations/postgres.
--
-- Notes:
--   * utf8mb4 + utf8mb4_bin to preserve email case sensitivity (matches PG/SQLite).
--   * DATETIME(6) for microsecond precision so audit log ordering is stable.
--   * InnoDB row format DYNAMIC to support long index prefixes.

CREATE TABLE IF NOT EXISTS `users` (
    `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `email`           VARCHAR(191) NOT NULL,
    `name`            VARCHAR(64)  NOT NULL,
    `password_hash`   VARCHAR(255) NOT NULL,
    `llm_endpoint`    VARCHAR(255) NOT NULL DEFAULT '',
    `llm_model`       VARCHAR(128) NOT NULL DEFAULT '',
    `llm_api_key_enc` VARCHAR(1024) NOT NULL DEFAULT '',
    `created_at`      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at`      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `deleted_at`      DATETIME(6) NULL,
    PRIMARY KEY (`id`),
    KEY `idx_users_deleted_at` (`deleted_at`),
    -- MySQL cannot express partial unique indexes; we keep a plain unique on
    -- email and rely on the service layer using `lower(email)` + blocking
    -- creation of new accounts with the same email when another is
    -- soft-deleted. See docs for the migration pre-flight.
    UNIQUE KEY `uq_users_email` (`email`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS `groups` (
    `id`          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `name`        VARCHAR(128) NOT NULL,
    `description` VARCHAR(512) NOT NULL DEFAULT '',
    `owner_id`    BIGINT UNSIGNED NOT NULL,
    `share_llm`   TINYINT(1)  NOT NULL DEFAULT 0,
    `created_at`  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at`  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `deleted_at`  DATETIME(6) NULL,
    PRIMARY KEY (`id`),
    KEY `idx_groups_owner_id`   (`owner_id`),
    KEY `idx_groups_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS `group_members` (
    `id`         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `group_id`   BIGINT UNSIGNED NOT NULL,
    `user_id`    BIGINT UNSIGNED NOT NULL,
    `role`       VARCHAR(16) NOT NULL,
    `created_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at` DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    UNIQUE KEY `idx_group_user` (`group_id`, `user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS `connections` (
    `id`                  BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `group_id`            BIGINT UNSIGNED NOT NULL,
    `name`                VARCHAR(128) NOT NULL,
    `driver`              VARCHAR(32)  NOT NULL,
    `host`                VARCHAR(255) NOT NULL,
    `port`                INT          NOT NULL,
    `database`            VARCHAR(128) NOT NULL,
    `username`            VARCHAR(128) NOT NULL,
    `password_enc`        VARCHAR(1024) NOT NULL,
    `ssl_mode`            VARCHAR(32)  NOT NULL DEFAULT 'disable',
    `sslca_cert_enc`      LONGTEXT     NOT NULL,
    `ssl_client_cert_enc` LONGTEXT     NOT NULL,
    `ssl_client_key_enc`  LONGTEXT     NOT NULL,
    `ssh_enabled`         TINYINT(1)   NOT NULL DEFAULT 0,
    `ssh_host`            VARCHAR(255) NOT NULL DEFAULT '',
    `ssh_port`            INT          NOT NULL DEFAULT 22,
    `ssh_user`            VARCHAR(128) NOT NULL DEFAULT '',
    `ssh_auth_method`     VARCHAR(32)  NOT NULL DEFAULT '',
    `ssh_password_enc`    VARCHAR(1024) NOT NULL DEFAULT '',
    `ssh_private_key_enc` LONGTEXT     NOT NULL,
    `ssh_passphrase_enc`  VARCHAR(1024) NOT NULL DEFAULT '',
    `created_by_id`       BIGINT UNSIGNED NOT NULL,
    `created_at`          DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at`          DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `deleted_at`          DATETIME(6)  NULL,
    PRIMARY KEY (`id`),
    KEY `idx_connections_group_id`   (`group_id`),
    KEY `idx_connections_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;

CREATE TABLE IF NOT EXISTS `saved_queries` (
    `id`            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `group_id`      BIGINT UNSIGNED NOT NULL,
    `connection_id` BIGINT UNSIGNED NOT NULL,
    `title`         VARCHAR(255) NOT NULL,
    `description`   VARCHAR(1024) NOT NULL DEFAULT '',
    `sql`           LONGTEXT NOT NULL,
    `created_by_id` BIGINT UNSIGNED NOT NULL,
    `created_at`    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `updated_at`    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    `deleted_at`    DATETIME(6) NULL,
    PRIMARY KEY (`id`),
    KEY `idx_saved_queries_group_id`      (`group_id`),
    KEY `idx_saved_queries_connection_id` (`connection_id`),
    KEY `idx_saved_queries_created_by_id` (`created_by_id`),
    KEY `idx_saved_queries_deleted_at`    (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin ROW_FORMAT=DYNAMIC;
