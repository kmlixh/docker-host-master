package main

import (
	"os"
	"strconv"
)

// Config 是服务的全部运行时配置 — 纯环境变量 + 合理默认。
// 无 Consul、无 postgres、无 yaml。
//
// 鉴权拓扑:
//   /admin/*    走 adminBackend 颁发的 admin opaque token(共享 Redis 查 user_id_<t>)
//   /external/* 走本地 JSON 文件存的 access_token(bcrypt 哈希)
//
// 必填:
//   REDIS_ADDR     — admin token 验证靠这个,空了 /admin/* 全部 503
//
// (注:adminBackend / userLogin 的 admin 是写 Redis db=3,这里 REDIS_DB 必须对齐 = 3)
//
// 以下是硬编码默认,不开 env vars(运维无理由改,只会引入版本不一致 bug):
//   - HOSTS 块的 BEGIN/END marker(必须配对,可读性强)
//   - HOSTS 全量 reconcile 间隔 5min(够兜底丢事件)
//   - TOKEN_STORE 文件路径(named volume 内固定位置)
//   - AUDIT_LOG 路径(named volume + stdout 双写,运维如果要 file 长期保留就再挂一个 volume)
const (
	defaultHostsBeginMarker          = "# BEGIN docker-host-master (DO NOT EDIT)"
	defaultHostsEndMarker            = "# END docker-host-master"
	defaultHostsReconcileIntervalSec = 300
	defaultTokenStoreFile            = "/var/lib/docker-host-master/tokens.json"
	defaultAuditLogFile              = "/var/log/docker-host-master/audit.log"
)

type Config struct {
	Server struct {
		Port int
		Name string
	}

	Docker struct {
		Endpoint       string
		TimeoutSeconds int
	}

	Hosts struct {
		File                 string
		BeginMarker          string // 硬编码默认,见 defaultHostsBeginMarker
		EndMarker            string // 硬编码默认,见 defaultHostsEndMarker
		ReconcileIntervalSec int    // 硬编码默认 300s
	}

	// Redis — 跟 adminBackend.authing.redis 对齐,共享 admin token store
	Redis struct {
		Addr     string // 例如 172.17.0.1:6379;空 = /admin/* 全 503
		Password string
		DB       int // 跟 adminBackend.authing.redis.database 对齐 (db=3)
	}

	// 本地 token store(JSON 文件)— /external/* 的 access_token 存这
	// 路径硬编码,运维不要改 — 跟 Dockerfile / docker run 的 named volume mount path 强绑定
	TokenStore struct {
		File string
	}

	Audit struct {
		LogFile string // 硬编码,运维只能通过加 named volume 把整个目录挂出来
	}
}

func LoadFromEnv() *Config {
	c := &Config{}

	c.Server.Port = envInt("SERVER_PORT", 8090)
	c.Server.Name = envStr("SERVICE_NAME", "docker-host-master")

	c.Docker.Endpoint = envStr("DOCKER_ENDPOINT", "unix:///var/run/docker.sock")
	c.Docker.TimeoutSeconds = envInt("DOCKER_TIMEOUT_SEC", 30)

	// HOSTS 文件路径还是允许覆盖(测试环境/特殊 mount 用),但 marker + 兜底间隔写死
	c.Hosts.File = envStr("HOSTS_FILE", "/etc/hosts")
	c.Hosts.BeginMarker = defaultHostsBeginMarker
	c.Hosts.EndMarker = defaultHostsEndMarker
	c.Hosts.ReconcileIntervalSec = defaultHostsReconcileIntervalSec

	c.Redis.Addr = envStr("REDIS_ADDR", "") // 必填
	c.Redis.Password = envStr("REDIS_PASSWORD", "")
	c.Redis.DB = envInt("REDIS_DB", 3) // 默认 3 跟 adminBackend.authing.redis.database 对齐

	c.TokenStore.File = defaultTokenStoreFile
	c.Audit.LogFile = defaultAuditLogFile

	return c
}

// Warnings 启动时 log 出来让运维知道哪些功能会因为缺配被禁
func (c *Config) Warnings() []string {
	var w []string
	if c.Redis.Addr == "" {
		w = append(w, "REDIS_ADDR 未设 → /admin/* 路由会拒绝所有请求(adminBackend admin token 验不了)")
	}
	return w
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
