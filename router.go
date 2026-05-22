package main

import (
	"os"

	"github.com/gofiber/fiber/v2"
)

// SetupRouter 挂全部路由。
//
// 三组:
//   /health             — 公开,健康检查
//   /admin/*            — adminAuthMiddleware (authing JWT + tenant=="admin")
//   /external/*         — externalAuthMiddleware (X-Access-Token bcrypt 比对)
func SetupRouter(app *fiber.App) {
	// 公开
	app.Get("/health", healthHandler)

	// Admin — authing 保护
	admin := app.Group("/admin", adminAuthMiddleware)
	// containers
	admin.Get("/containers", adminListContainers)
	admin.Get("/containers/:id", adminInspectContainer)
	admin.Post("/containers/:id/start", adminContainerAction("start"))
	admin.Post("/containers/:id/stop", adminContainerAction("stop"))
	admin.Post("/containers/:id/restart", adminContainerAction("restart"))
	admin.Post("/containers/:id/pause", adminContainerAction("pause"))
	admin.Post("/containers/:id/unpause", adminContainerAction("unpause"))
	admin.Delete("/containers/:id", adminRemoveContainer)
	admin.Get("/containers/:id/logs", adminContainerLogs)
	// hosts
	admin.Get("/hosts", adminGetHosts)
	// tokens
	admin.Get("/tokens", adminListTokens)
	admin.Post("/tokens", adminCreateToken)
	admin.Post("/tokens/:id/regenerate", adminRegenerateToken)
	admin.Post("/tokens/:id/enable", adminSetTokenEnabled(true))
	admin.Post("/tokens/:id/disable", adminSetTokenEnabled(false))
	admin.Delete("/tokens/:id", adminDeleteToken)

	// External — access_token 保护
	external := app.Group("/external", externalAuthMiddleware)
	external.Post("/containers/:id/start", externalContainerAction("start"))
	external.Post("/containers/:id/stop", externalContainerAction("stop"))
	external.Post("/containers/:id/restart", externalContainerAction("restart"))
	external.Post("/containers/:id/pause", externalContainerAction("pause"))
}

func healthHandler(c *fiber.Ctx) error {
	hostname, _ := os.Hostname()
	body := fiber.Map{
		"status":   "ok",
		"service":  "docker-host-master",
		"hostname": hostname,
	}
	// 顺手 ping docker 看 daemon 是否在
	if dockerCli != nil {
		if ver, err := dockerCli.Ping(c.Context()); err == nil {
			body["docker_api"] = ver
		}
	}
	return c.JSON(body)
}
