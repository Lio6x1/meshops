-- 个人账号与会话；初始管理员由本机命令建立，迁移不写入密码。
CREATE TABLE accounts (
 id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 tenant_id VARCHAR(64) NOT NULL,
 username VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 display_name VARCHAR(128) NOT NULL,
 role ENUM('admin','operator') NOT NULL,
 enabled BOOLEAN NOT NULL DEFAULT TRUE,
 must_change_password BOOLEAN NOT NULL DEFAULT TRUE,
 password_hash VARCHAR(256) CHARACTER SET ascii NOT NULL,
 auth_version BIGINT NOT NULL DEFAULT 1,
 created_at DATETIME(6) NOT NULL,
 updated_at DATETIME(6) NOT NULL,
 admin_tenant VARCHAR(64) GENERATED ALWAYS AS (IF(role='admin',tenant_id,NULL)) STORED,
 UNIQUE KEY uk_account_username (tenant_id,username),
 UNIQUE KEY uk_account_admin (admin_tenant),
 KEY idx_account_page (tenant_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE account_sessions (
 token_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
 account_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 auth_version BIGINT NOT NULL,
 expires_at DATETIME(6) NOT NULL,
 KEY idx_account_sessions_user (account_id),
 KEY idx_account_sessions_expiry (expires_at),
 CONSTRAINT fk_account_session_user FOREIGN KEY (account_id) REFERENCES accounts(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE TABLE account_audit (
 id BIGINT AUTO_INCREMENT PRIMARY KEY,
 tenant_id VARCHAR(64) NOT NULL,
 actor_id VARCHAR(128) NOT NULL,
 target_id VARCHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
 action VARCHAR(32) NOT NULL,
 created_at DATETIME(6) NOT NULL,
 KEY idx_account_audit_tenant (tenant_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
