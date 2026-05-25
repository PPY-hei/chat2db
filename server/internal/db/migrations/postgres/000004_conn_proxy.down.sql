ALTER TABLE connections DROP COLUMN IF EXISTS proxy_password_enc;
ALTER TABLE connections DROP COLUMN IF EXISTS proxy_username;
ALTER TABLE connections DROP COLUMN IF EXISTS proxy_port;
ALTER TABLE connections DROP COLUMN IF EXISTS proxy_host;
ALTER TABLE connections DROP COLUMN IF EXISTS proxy_type;
ALTER TABLE connections DROP COLUMN IF EXISTS proxy_enabled;
