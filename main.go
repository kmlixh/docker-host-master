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

// 全局,handler/middleware 用
var (
	appCfg     *Config
	dockerCli  *DockerClient
	hostsMgr   *HostsManager
	hostDaemon *Daemon
	tokenStore *TokenStore
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("=== docker-host-master starting ===")

	// 1. 加载配置 — 纯 env vars,合理默认。无 Consul 依赖。
	appCfg = LoadFromEnv()
	log.Printf("config: port=%d docker=%s hosts=%s db=%s@%s:%d/%s",
		appCfg.Server.Port,
		appCfg.Docker.Endpoint,
		appCfg.Hosts.File,
		appCfg.Database.User, appCfg.Database.Host, appCfg.Database.Port, appCfg.Database.DBName,
	)
	for _, w := range appCfg.Warnings() {
		log.Printf("WARN: %s", w)
	}

	// 2. Docker client + 验 daemon 可达(失败 warn,daemon goroutine 还会自动重试)
	cli, err := NewDockerClient(appCfg.Docker.Endpoint, appCfg.Docker.TimeoutSeconds)
	if err != nil {
		log.Fatalf("docker client init: %v", err)
	}
	dockerCli = cli
	defer dockerCli.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if ver, perr := dockerCli.Ping(pingCtx); perr != nil {
		log.Printf("WARN: docker daemon ping failed: %v (daemon goroutine 仍会启动,会自动重试)", perr)
	} else {
		log.Printf("docker daemon OK: api=%s", ver)
	}
	pingCancel()

	// 3. /etc/hosts manager + daemon goroutine
	hostsMgr = NewHostsManager(appCfg.Hosts.File, appCfg.Hosts.BeginMarker, appCfg.Hosts.EndMarker)
	daemonCtx, daemonCancel := context.WithCancel(context.Background())
	hostDaemon = NewDaemon(dockerCli, hostsMgr, appCfg)
	hostDaemon.Start(daemonCtx)
	defer func() {
		log.Println("stopping daemon goroutine...")
		daemonCancel()
		hostDaemon.Wait()
	}()

	// 4. Token store (DB 没配就跳过,/admin/tokens + /external/* 会 503)
	if appCfg.Database.Password != "" {
		ts, err := NewTokenStore(appCfg)
		if err != nil {
			log.Printf("WARN: token store init failed: %v (/admin/tokens + /external/* 会 503)", err)
		} else {
			tokenStore = ts
			log.Println("token store ready")
		}
	} else {
		log.Println("token store skipped (DB_PASSWORD 未设)")
	}

	// 5. authing JWT validator (issuer 没配就 nil,/admin/* 会 503)
	InitAuthing(appCfg)

	// 6. Audit log
	InitAuditLog(appCfg)
	defer CloseAuditLog()

	// 7. Fiber HTTP server
	app := fiber.New(fiber.Config{
		ErrorHandler: customErrorHandler,
	})
	app.Use(recover.New())
	app.Use(cors.New())
	app.Use(logger.New())

	SetupRouter(app)

	// 8. 信号 + graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	addr := fmt.Sprintf(":%d", appCfg.Server.Port)
	go func() {
		log.Printf("HTTP server listening on %s", addr)
		if err := app.Listen(addr); err != nil {
			log.Printf("server stopped: %v", err)
		}
	}()

	sig := <-sigCh
	log.Printf("received signal %s, shutting down...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	log.Println("=== docker-host-master stopped ===")
}

// customErrorHandler 标准 JSON 错误响应
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
