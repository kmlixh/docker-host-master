package main

import (
	"os"

	"github.com/gofiber/fiber/v2"
)

// SetupRouter — Phase A 只挂 /health。后续 phase 在这里加路由组:
//   /admin/* (authing IdentityMiddleware)
//   /external/* (access_token middleware)
func SetupRouter(app *fiber.App) {
	app.Get("/health", healthHandler)

	// Phase B/C/D 后续:
	// admin := app.Group("/admin", IdentityMiddleware())
	// admin.Get("/containers", listContainersHandler)
	// admin.Post("/containers/:id/start", startContainerHandler)
	// ...
	// external := app.Group("/external", AccessTokenMiddleware())
	// external.Post("/containers/:id/start", externalStartHandler)
}

func healthHandler(c *fiber.Ctx) error {
	hostname, _ := os.Hostname()
	return c.JSON(fiber.Map{
		"status":   "ok",
		"service":  "docker-host-master",
		"hostname": hostname,
		// 后续 Phase B 加 docker_version 字段
	})
}
