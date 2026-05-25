ALTER TABLE `connections`
  ADD COLUMN `proxy_enabled`      TINYINT(1)    NOT NULL DEFAULT 0,
  ADD COLUMN `proxy_type`         VARCHAR(16)   NOT NULL DEFAULT '',
  ADD COLUMN `proxy_host`         VARCHAR(255)  NOT NULL DEFAULT '',
  ADD COLUMN `proxy_port`         INT           NOT NULL DEFAULT 0,
  ADD COLUMN `proxy_username`     VARCHAR(128)  NOT NULL DEFAULT '',
  ADD COLUMN `proxy_password_enc` VARCHAR(1024) NOT NULL DEFAULT '';
