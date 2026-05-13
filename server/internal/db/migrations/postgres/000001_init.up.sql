-- Chat2DB metadata schema v1 (PostgreSQL).
-- Mirrors server/internal/model/model.go. Changes here MUST be paired with
-- a matching MySQL migration under migrations/mysql.

CREATE TABLE IF NOT EXISTS users (
    id               BIGSERIAL PRIMARY KEY,
    email            VARCHAR(191) NOT NULL,
    name             VARCHAR(64)  NOT NULL,
    password_hash    VARCHAR(255) NOT NULL,
    llm_endpoint     VARCHAR(255) NOT NULL DEFAULT '',
    llm_model        VARCHAR(128) NOT NULL DEFAULT '',
    llm_api_key_enc  VARCHAR(1024) NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at       TIMESTAMPTZ
);
-- Email is unique only among non-deleted rows; see pre-flight notes.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_active
    ON users (email) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_users_deleted_at ON users (deleted_at);

CREATE TABLE IF NOT EXISTS groups (
    id          BIGSERIAL PRIMARY KEY,
    name        VARCHAR(128) NOT NULL,
    description VARCHAR(512) NOT NULL DEFAULT '',
    owner_id    BIGINT       NOT NULL,
    share_llm   BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_groups_owner_id   ON groups (owner_id);
CREATE INDEX IF NOT EXISTS idx_groups_deleted_at ON groups (deleted_at);

CREATE TABLE IF NOT EXISTS group_members (
    id         BIGSERIAL PRIMARY KEY,
    group_id   BIGINT       NOT NULL,
    user_id    BIGINT       NOT NULL,
    role       VARCHAR(16)  NOT NULL,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
-- Unique per (group, user). group_members is currently hard-deleted in service layer;
-- if soft delete is introduced later, migrate to a partial unique index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_user
    ON group_members (group_id, user_id);

CREATE TABLE IF NOT EXISTS connections (
    id                   BIGSERIAL PRIMARY KEY,
    group_id             BIGINT       NOT NULL,
    name                 VARCHAR(128) NOT NULL,
    driver               VARCHAR(32)  NOT NULL,
    host                 VARCHAR(255) NOT NULL,
    port                 INTEGER      NOT NULL,
    database             VARCHAR(128) NOT NULL,
    username             VARCHAR(128) NOT NULL,
    password_enc         VARCHAR(1024) NOT NULL,
    ssl_mode             VARCHAR(32)  NOT NULL DEFAULT 'disable',
    sslca_cert_enc       TEXT         NOT NULL DEFAULT '',
    ssl_client_cert_enc  TEXT         NOT NULL DEFAULT '',
    ssl_client_key_enc   TEXT         NOT NULL DEFAULT '',
    ssh_enabled          BOOLEAN      NOT NULL DEFAULT FALSE,
    ssh_host             VARCHAR(255) NOT NULL DEFAULT '',
    ssh_port             INTEGER      NOT NULL DEFAULT 22,
    ssh_user             VARCHAR(128) NOT NULL DEFAULT '',
    ssh_auth_method      VARCHAR(32)  NOT NULL DEFAULT '',
    ssh_password_enc     VARCHAR(1024) NOT NULL DEFAULT '',
    ssh_private_key_enc  TEXT         NOT NULL DEFAULT '',
    ssh_passphrase_enc   VARCHAR(1024) NOT NULL DEFAULT '',
    created_by_id        BIGINT       NOT NULL,
    created_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_connections_group_id   ON connections (group_id);
CREATE INDEX IF NOT EXISTS idx_connections_deleted_at ON connections (deleted_at);

CREATE TABLE IF NOT EXISTS saved_queries (
    id             BIGSERIAL PRIMARY KEY,
    group_id       BIGINT       NOT NULL,
    connection_id  BIGINT       NOT NULL,
    title          VARCHAR(255) NOT NULL,
    description    VARCHAR(1024) NOT NULL DEFAULT '',
    sql            TEXT         NOT NULL,
    created_by_id  BIGINT       NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    deleted_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_saved_queries_group_id      ON saved_queries (group_id);
CREATE INDEX IF NOT EXISTS idx_saved_queries_connection_id ON saved_queries (connection_id);
CREATE INDEX IF NOT EXISTS idx_saved_queries_created_by_id ON saved_queries (created_by_id);
CREATE INDEX IF NOT EXISTS idx_saved_queries_deleted_at    ON saved_queries (deleted_at);
