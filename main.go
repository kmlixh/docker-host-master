package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// 全局应用配置(consul 加载后填),给各 handler/middleware 用。
// daemon goroutine 也读它(docker endpoint / hosts file path)
var appCfg *Config

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== docker-host-master starting ===")

	// 1. 加载本地 consul 地址配置
	local, err := LoadLocalConfig("config.yaml")
	if err != nil {
		log.Fatalf("load local config: %v", err)
	}
	log.Printf("local config: consul=%s path=%s", local.Consul.Address, local.Consul.ConfigPath)

	// 2. 注册到 consul(每台 host 各自实例,service ID 带 hostname)
	if err := RegisterService(local, local.Consul.Address, local.Consul.Token); err != nil {
		log.Printf("WARN: consul register failed: %v (服务继续启动)", err)
	}

	// 3. 拉 application.yml from consul KV
	ct, err := NewConsulTool(local.Consul.Address, local.Consul.Token, local.Consul.ConfigPath)
	if err != nil {
		log.Fatalf("consul tool: %v", err)
	}
	cfg, err := ct.LoadConfig()
	if err != nil {
		log.Fatalf("load app config from consul: %v", err)
	}
	appCfg = cfg
	log.Printf("app config loaded: docker=%s hosts=%s", cfg.Docker.Endpoint, cfg.Hosts.File)

	// 4. TODO Phase B: 初始化 Docker client + /etc/hosts manager + 启动 daemon goroutine
	// 4. TODO Phase C: 初始化数据库 + AccessToken store
	// 4. TODO Phase C/D: 初始化 authing JWT validator

	// 5. Fiber HTTP server
	app := fiber.New(fiber.Config{
		ErrorHandler: customErrorHandler,
	})
	app.Use(recover.New())
	app.Use(cors.New())
	app.Use(logger.New())

	// 路由 — Phase A 只挂 /health
	SetupRouter(app)

	// 6. 信号 + graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	go func() {
		log.Printf("HTTP server listening on %s", addr)
		if err := app.Listen(addr); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	sig := <-sigCh
	log.Printf("received signal %s, shutting down...", sig)

	// 优雅关闭
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	if err := DeregisterService(); err != nil {
		log.Printf("consul deregister: %v", err)
	}
	log.Println("=== docker-host-master stopped ===")
}

// 标准 JSON 错误响应,跟 monorepo 其他服务对齐
func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	msg := "请求处理失败"
	switch code {
	case fiber.StatusNotFound:
		msg = "资源不存在"
	case fiber.StatusUnauthorized:
		msg = "未授权"
	case fiber.StatusForbidden:
		msg = "禁止访问"
	case fiber.StatusBadRequest:
		msg = "请求参数错误"
	case fiber.StatusInternalServerError:
		msg = "服务器内部错误"
	}
	if err != nil && err.Error() != "" {
		msg = msg + ": " + err.Error()
	}
	return c.Status(code).JSON(fiber.Map{
		"code": code,
		"msg":  msg,
		"data": nil,
	})
}
