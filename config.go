package main

import (
	"fmt"
	"os"

	"github.com/kmlixh/consulTool"
	"github.com/kmlixh/dollarYaml"
)

// LocalConfig 本地 config.yaml(只有 consul 连接信息,启动时读)
type LocalConfig struct {
	Server struct {
		Port int    `yaml:"port"`
		Name string `yaml:"name"`
	} `yaml:"server"`

	Consul struct {
		Address    string `yaml:"address"`
		Token      string `yaml:"token"`
		ConfigPath string `yaml:"config_path"`
		Enabled    bool   `yaml:"enabled"`
	} `yaml:"consul"`
}

// Config 从 consul KV 拉取的应用配置(application.yml)
type Config struct {
	Server struct {
		Port int    `yaml:"port"`
		Name string `yaml:"name"`
	} `yaml:"server"`

	Docker struct {
		// unix:///var/run/docker.sock 或 tcp://host:port
		Endpoint       string `yaml:"endpoint"`
		TimeoutSeconds int    `yaml:"timeout_seconds"`
	} `yaml:"docker"`

	Hosts struct {
		File                 string `yaml:"file"`
		BeginMarker          string `yaml:"begin_marker"`
		EndMarker            string `yaml:"end_marker"`
		ReconcileIntervalSec int    `yaml:"reconcile_interval_sec"`
	} `yaml:"hosts"`

	Database struct {
		Host     string `yaml:"host"`
		Port     int    `yaml:"port"`
		User     string `yaml:"user"`
		Password string `yaml:"password"`
		DBName   string `yaml:"dbname"`
		SSLMode  string `yaml:"sslmode"`
	} `yaml:"database"`

	OAuth struct {
		// authing JWKS issuer URL,例如 https://auth.janyee.com
		Issuer string `yaml:"issuer"`
	} `yaml:"oauth"`

	Audit struct {
		LogFile string `yaml:"log_file"`
	} `yaml:"audit"`
}

// GetDSN 拼 PostgreSQL DSN
func (c *Config) GetDSN() string {
	ssl := c.Database.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Database.Host, c.Database.Port,
		c.Database.User, c.Database.Password,
		c.Database.DBName, ssl,
	)
}

// LoadLocalConfig 读 config.yaml + env 变量覆盖
func LoadLocalConfig(path string) (*LocalConfig, error) {
	var cfg LocalConfig
	profile := dollarYaml.New()
	if err := profile.ReadFromPath(path); err != nil {
		return nil, fmt.Errorf("read local config: %w", err)
	}
	if err := profile.UnmarshalTo(&cfg); err != nil {
		return nil, fmt.Errorf("parse local config: %w", err)
	}
	// 显式 env 覆盖(dollarYaml 已经处理 ${VAR:default} 形式,这里给 CONSUL_* 兜底)
	if v := os.Getenv("CONSUL_ADDRESS"); v != "" {
		cfg.Consul.Address = v
	}
	if v := os.Getenv("CONSUL_TOKEN"); v != "" {
		cfg.Consul.Token = v
	}
	if v := os.Getenv("CONSUL_CONFIG_PATH"); v != "" {
		cfg.Consul.ConfigPath = v
	}
	return &cfg, nil
}

// consul 注册 + 配置拉取

var serviceRegistrant *consulTool.ServiceRegistrant

type ConsulTool struct {
	config     *consulTool.Config
	agent      *consulTool.Agent
	configPath string
}

func NewConsulTool(address, token, configPath string) (*ConsulTool, error) {
	cfg := consulTool.NewConfigWithAddress(address)
	if token != "" {
		cfg.SetToken(token)
	}
	return &ConsulTool{
		config:     cfg,
		agent:      consulTool.NewAgent(cfg),
		configPath: configPath,
	}, nil
}

func (c *ConsulTool) LoadConfig() (*Config, error) {
	kv, err := c.agent.GetKV(c.configPath)
	if err != nil {
		return nil, fmt.Errorf("get KV %q: %w", c.configPath, err)
	}
	if kv == nil || kv.Value == nil {
		return nil, fmt.Errorf("consul KV %q empty", c.configPath)
	}
	profile := dollarYaml.New()
	if err := profile.Read(kv.Value); err != nil {
		return nil, fmt.Errorf("parse KV yaml: %w", err)
	}
	var cfg Config
	if err := profile.UnmarshalTo(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal Config: %w", err)
	}
	return &cfg, nil
}

// RegisterService 把自己注册到 consul。
// 每台主机一个实例,用 hostname 区分 service ID + tag,让 gateway / 服务发现知道
// 当前 instance 对应哪台 host。
func RegisterService(local *LocalConfig, address, token string) error {
	if !local.Consul.Enabled {
		return nil
	}
	cfg := consulTool.NewConfigWithAddress(address)
	if token != "" {
		cfg.SetToken(token)
	}
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	serviceName := local.Server.Name
	if serviceName == "" {
		serviceName = "docker-host-master"
	}
	// service ID 带 hostname,多台主机不会撞
	serviceID := fmt.Sprintf("%s-%s-%d", serviceName, hostname, local.Server.Port)
	builder := consulTool.NewServiceRegistrantBuilder(cfg)
	r, err := builder.
		WithID(serviceID).
		WithName(serviceName).
		WithPort(local.Server.Port).
		WithTags([]string{"host", "docker", "host:" + hostname}).
		WithHealthCheckPath("/health").
		WithHttpSchema("http").
		WithInterval("10s").
		WithTimeout("5s").
		Build()
	if err != nil {
		return fmt.Errorf("build registrant: %w", err)
	}
	if err := r.RegisterService(); err != nil {
		return fmt.Errorf("register service: %w", err)
	}
	serviceRegistrant = r
	return nil
}

func DeregisterService() error {
	if serviceRegistrant == nil {
		return nil
	}
	return serviceRegistrant.DeRegisterService()
}
