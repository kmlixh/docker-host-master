package main

import (
	"fmt"
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
		BeginMarker          string
		EndMarker            string
		ReconcileIntervalSec int
	}

	// Redis — 跟 adminBackend.authing.redis 对齐,共享 admin token store
	Redis struct {
		Addr     string // 例如 172.17.0.1:6379;空 = /admin/* 全 503
		Password string
		DB       int // 跟 adminBackend.authing.redis.database 对齐 (db=3)
	}

	// 本地 token store(JSON 文件)— /external/* 的 access_token 存这
	TokenStore struct {
		File string // 默认 /var/lib/docker-host-master/tokens.json
	}

	Audit struct {
		LogFile string
	}
}

func LoadFromEnv() *Config {
	c := &Config{}

	c.Server.Port = envInt("SERVER_PORT", 8090)
	c.Server.Name = envStr("SERVICE_NAME", "docker-host-master")

	c.Docker.Endpoint = envStr("DOCKER_ENDPOINT", "unix:///var/run/docker.sock")
	c.Docker.TimeoutSeconds = envInt("DOCKER_TIMEOUT_SEC", 30)

	c.Hosts.File = envStr("HOSTS_FILE", "/etc/hosts")
	c.Hosts.BeginMarker = envStr("HOSTS_BEGIN_MARKER", "# BEGIN docker-host-master (DO NOT EDIT)")
	c.Hosts.EndMarker = envStr("HOSTS_END_MARKER", "# END docker-host-master")
	c.Hosts.ReconcileIntervalSec = envInt("HOSTS_RECONCILE_INTERVAL_SEC", 300)

	c.Redis.Addr = envStr("REDIS_ADDR", "") // 必填
	c.Redis.Password = envStr("REDIS_PASSWORD", "")
	c.Redis.DB = envInt("REDIS_DB", 3) // 默认 3 跟 adminBackend.authing.redis.database 对齐

	c.TokenStore.File = envStr("TOKEN_STORE_FILE", "/var/lib/docker-host-master/tokens.json")

	c.Audit.LogFile = envStr("AUDIT_LOG", "/var/log/docker-host-master/audit.log")

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

// 占位,避免 import "fmt" 被 tidy 删
var _ = fmt.Sprintf
