-- ============================================================================
-- 001 — dhm_access_tokens 表(/external/* 认证用)
-- ============================================================================
--
-- 部署前手动:psql -h <db-host> -U postgres -d docker_host_master \
--             -f migrations/001_access_tokens.sql
--
-- 也可以让服务启动时 AutoMigrate(代码里默认开了),但 prod 建议手动 SQL 控制。

CREATE TABLE IF NOT EXISTS dhm_access_tokens (
    id            BIGSERIAL PRIMARY KEY,
    token_hash    VARCHAR(255) NOT NULL,         -- bcrypt hash, 明文不入库
    name          VARCHAR(128) NOT NULL,
    description   VARCHAR(255),
    hostname      VARCHAR(128),                  -- 空 = 全集群通用;非空 = 只对该 host 实例生效
    enabled       BOOLEAN NOT NULL DEFAULT true,
    expires_at    TIMESTAMP WITH TIME ZONE,      -- NULL = 永不过期
    created_at    TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at    TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    last_used_at  TIMESTAMP WITH TIME ZONE
);

CREATE INDEX IF NOT EXISTS idx_dhm_tokens_hostname ON dhm_access_tokens(hostname);
CREATE INDEX IF NOT EXISTS idx_dhm_tokens_enabled  ON dhm_access_tokens(enabled);

COMMENT ON TABLE  dhm_access_tokens IS 'docker-host-master external API 访问凭证(bcrypt 哈希存)';
COMMENT ON COLUMN dhm_access_tokens.token_hash IS 'bcrypt(plain),plain 只在创建时返回一次';
COMMENT ON COLUMN dhm_access_tokens.hostname   IS 'NULL/空=全集群通用;非空=只对该 host 实例生效';
