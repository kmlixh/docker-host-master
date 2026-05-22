package main

import (
	"github.com/gofiber/fiber/v2"
)

// external_handlers.go — /external/* 的 handler。
// 由 externalAuthMiddleware (X-Access-Token bcrypt 比对) 保护。
//
// 比 admin handler 更窄一点 — 只暴露安全相关操作(启停、重启),
// 不暴露 inspect/delete/logs。后者敏感性高,留给 admin。

// POST /external/containers/:id/start
func externalContainerAction(action string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Params("id")
		tokenName, _ := c.Locals("access_token_name").(string)

		var err error
		switch action {
		case "start":
			err = dockerCli.Start(c.Context(), id)
		case "stop":
			err = dockerCli.Stop(c.Context(), id)
		case "restart":
			err = dockerCli.Restart(c.Context(), id)
		case "pause":
			err = dockerCli.Pause(c.Context(), id)
		default:
			return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "unknown action: " + action})
		}
		result := "ok"
		if err != nil {
			result = "err"
		}
		// 审计 log
		AuditLog(tokenName, action, id, result, errMsg(err))

		if err != nil {
			return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
		}
		return c.JSON(fiber.Map{"code": 0, "msg": "ok", "action": action, "container": id})
	}
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
