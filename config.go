package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config 是服务的全部运行时配置。
// 来源:纯环境变量 + 合理默认。不依赖 Consul、不读配置文件。
//
// 部署:docker run 时把要改的字段以 -e KEY=VALUE 形式传。
// 也可以放 docker-compose.yml 的 environment 块里。
//
// 必填字段(没默认值,空就拒绝启动):
//   DB_PASSWORD  — token store 用,空了 /admin/tokens + /external/* 全 503
//   OAUTH_ISSUER — admin 鉴权用,空了 /admin/* 全 503
//
// 其它字段都有合理默认,大多场景不用改。
type Config struct {
	Server struct {
		Port int
		Name string
	}

	Docker struct {
		Endpoint       string // 默认 unix:///var/run/docker.sock
		TimeoutSeconds int    // 默认 30
	}

	Hosts struct {
		File                 string // 默认 /etc/hosts
		BeginMarker          string // 默认 "# BEGIN docker-host-master (DO NOT EDIT)"
		EndMarker            string // 默认 "# END docker-host-master"
		ReconcileIntervalSec int    // 默认 300 (5min)
	}

	Database struct {
		Host     string // 默认 172.17.0.1 (docker bridge gateway,容器内访问宿主)
		Port     int    // 默认 5432
		User     string // 默认 postgres
		Password string // 必填(空 = token store 跳过初始化,/admin/tokens + /external/* 503)
		DBName   string // 默认 docker_host_master
		SSLMode  string // 默认 disable
	}

	OAuth struct {
		Issuer string // 例如 https://auth.janyee.com,空 = /admin/* 全 503
	}

	Audit struct {
		LogFile string // 默认 /var/log/docker-host-master/audit.log
	}
}

// GetDSN 拼 PostgreSQL DSN
func (c *Config) GetDSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Host, c.Database.Port,
		c.Database.User, c.Database.Password,
		c.Database.DBName, c.Database.SSLMode,
	)
}

// LoadFromEnv 从环境变量读出全部配置,缺省值兜底。
//
// 每台 host 部署时只需要在 docker run 里塞:
//   -e DB_PASSWORD=xxx
//   -e DB_HOST=<pg-host>   (默认 172.17.0.1,共享宿主 pg 时常用;远程 pg 改这里)
//   -e OAUTH_ISSUER=https://auth.janyee.com
// 就够了,其它都能用默认。
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

	c.Database.Host = envStr("DB_HOST", "172.17.0.1")
	c.Database.Port = envInt("DB_PORT", 5432)
	c.Database.User = envStr("DB_USER", "postgres")
	c.Database.Password = envStr("DB_PASSWORD", "") // 必填,无默认
	c.Database.DBName = envStr("DB_NAME", "docker_host_master")
	c.Database.SSLMode = envStr("DB_SSLMODE", "disable")

	c.OAuth.Issuer = envStr("OAUTH_ISSUER", "") // 必填,无默认

	c.Audit.LogFile = envStr("AUDIT_LOG", "/var/log/docker-host-master/audit.log")

	return c
}

// Warnings 返回配置完整性 warning 列表(启动时打,让运维知道哪些功能会被禁掉)
func (c *Config) Warnings() []string {
	var w []string
	if c.Database.Password == "" {
		w = append(w, "DB_PASSWORD 未设 → token store 跳过初始化 → /admin/tokens + /external/* 路由会 503")
	}
	if c.OAuth.Issuer == "" {
		w = append(w, "OAUTH_ISSUER 未设 → /admin/* 路由会拒绝所有请求(只剩 /health 可用)")
	}
	return w
}

// 内部 helper

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

// duration helper(目前没用,但留着方便扩展)
func _envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
