ALTER TABLE connections ADD COLUMN proxy_enabled      BOOLEAN       NOT NULL DEFAULT FALSE;
ALTER TABLE connections ADD COLUMN proxy_type         VARCHAR(16)   NOT NULL DEFAULT '';
ALTER TABLE connections ADD COLUMN proxy_host         VARCHAR(255)  NOT NULL DEFAULT '';
ALTER TABLE connections ADD COLUMN proxy_port         INTEGER       NOT NULL DEFAULT 0;
ALTER TABLE connections ADD COLUMN proxy_username     VARCHAR(128)  NOT NULL DEFAULT '';
ALTER TABLE connections ADD COLUMN proxy_password_enc VARCHAR(1024) NOT NULL DEFAULT '';
