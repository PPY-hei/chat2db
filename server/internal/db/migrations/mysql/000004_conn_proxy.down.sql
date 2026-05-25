ALTER TABLE `connections`
  DROP COLUMN `proxy_password_enc`,
  DROP COLUMN `proxy_username`,
  DROP COLUMN `proxy_port`,
  DROP COLUMN `proxy_host`,
  DROP COLUMN `proxy_type`,
  DROP COLUMN `proxy_enabled`;
