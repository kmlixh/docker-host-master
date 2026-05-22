package main

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

// admin_handlers.go — /admin/* 的所有 handler。
// 已被 adminAuthMiddleware 保护(authing JWT + tenant_id=="admin")。

// ============================================================================
// /admin/containers/*
// ============================================================================

// GET /admin/containers — 列本机所有容器(含已停止)
func adminListContainers(c *fiber.Ctx) error {
	if dockerCli == nil {
		return c.Status(503).JSON(fiber.Map{"code": 503, "msg": "docker client not initialized"})
	}
	containers, err := dockerCli.ListContainers(c.Context(), true)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": "list: " + err.Error()})
	}
	return c.JSON(fiber.Map{"code": 0, "data": containers})
}

// GET /admin/containers/:id — inspect 详情
func adminInspectContainer(c *fiber.Ctx) error {
	info, err := dockerCli.Inspect(c.Context(), c.Params("id"))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"code": 0, "data": info})
}

// POST /admin/containers/:id/start|stop|restart|pause|unpause
func adminContainerAction(action string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Params("id")
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
		case "unpause":
			err = dockerCli.Unpause(c.Context(), id)
		default:
			return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "unknown action: " + action})
		}
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
		}
		return c.JSON(fiber.Map{"code": 0, "msg": "ok", "action": action, "container": id})
	}
}

// DELETE /admin/containers/:id?force=1
func adminRemoveContainer(c *fiber.Ctx) error {
	force := c.Query("force") == "1" || c.Query("force") == "true"
	if err := dockerCli.Remove(c.Context(), c.Params("id"), force); err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"code": 0, "msg": "removed", "container": c.Params("id")})
}

// GET /admin/containers/:id/logs?tail=100
//
// 这里返一次性 string(简单实现)。WebSocket 流式版本以后用
// github.com/gofiber/contrib/websocket 包,Phase B+。
func adminContainerLogs(c *fiber.Ctx) error {
	tail := 100
	if v := c.Query("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tail = n
		}
	}
	r, err := dockerCli.Logs(c.Context(), c.Params("id"), tail, false)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	defer r.Close()
	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 4096)
	for {
		n, rerr := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if rerr != nil {
			break
		}
		if len(buf) > 1024*1024 {
			// 防爆,1MB 截断
			break
		}
	}
	c.Set("Content-Type", "text/plain; charset=utf-8")
	return c.Send(buf)
}

// ============================================================================
// /admin/hosts — 当前 docker-host-master 维护的 /etc/hosts managed 块
// ============================================================================

// GET /admin/hosts — 返回当前 managed block 里的所有 HostEntry
func adminGetHosts(c *fiber.Ctx) error {
	if hostsMgr == nil {
		return c.Status(503).JSON(fiber.Map{"code": 503, "msg": "hosts manager not initialized"})
	}
	entries, err := hostsMgr.ReadManaged()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"code": 0, "data": entries, "count": len(entries)})
}

// ============================================================================
// /admin/tokens/* — access_token 颁发管理(类似 OAuth client_secret)
// ============================================================================

type createTokenReq struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Hostname    string  `json:"hostname"`        // 空 = 全集群通用
	ExpiresAt   *string `json:"expires_at"`      // ISO8601,空 = 永不过期
}

// POST /admin/tokens — 创建新 token,返一次明文
func adminCreateToken(c *fiber.Ctx) error {
	if tokenStore == nil {
		return c.Status(503).JSON(fiber.Map{"code": 503, "msg": "token store not initialized"})
	}
	var req createTokenReq
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "bad json: " + err.Error()})
	}
	if req.Name == "" {
		return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "name required"})
	}
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "invalid expires_at (need RFC3339): " + err.Error()})
		}
		expiresAt = &t
	}
	plain, row, err := tokenStore.Create(req.Name, req.Description, req.Hostname, expiresAt)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{
		"code": 0,
		"data": fiber.Map{
			"id":          row.ID,
			"name":        row.Name,
			"description": row.Description,
			"hostname":    row.Hostname,
			"enabled":     row.Enabled,
			"expires_at":  row.ExpiresAt,
			"created_at":  row.CreatedAt,
			"token":       plain, // ⚠ 只这次返回,客户端必须保存好
		},
		"msg": "access token created — token plaintext is shown only once",
	})
}

// GET /admin/tokens — 列出所有 token (无 plain)
func adminListTokens(c *fiber.Ctx) error {
	if tokenStore == nil {
		return c.Status(503).JSON(fiber.Map{"code": 503, "msg": "token store not initialized"})
	}
	rows, err := tokenStore.List()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"code": 0, "data": rows})
}

// POST /admin/tokens/:id/regenerate — 重生 plain,老的立刻失效
func adminRegenerateToken(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "bad id"})
	}
	plain, err := tokenStore.Regenerate(uint(id))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{
		"code": 0,
		"data": fiber.Map{"id": id, "token": plain},
		"msg":  "regenerated — old token immediately invalid",
	})
}

// POST /admin/tokens/:id/enable | /disable
func adminSetTokenEnabled(enabled bool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		id, err := strconv.ParseUint(c.Params("id"), 10, 64)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "bad id"})
		}
		if err := tokenStore.SetEnabled(uint(id), enabled); err != nil {
			return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
		}
		return c.JSON(fiber.Map{"code": 0, "msg": "ok", "enabled": enabled})
	}
}

// DELETE /admin/tokens/:id
func adminDeleteToken(c *fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"code": 400, "msg": "bad id"})
	}
	if err := tokenStore.Delete(uint(id)); err != nil {
		return c.Status(500).JSON(fiber.Map{"code": 500, "msg": err.Error()})
	}
	return c.JSON(fiber.Map{"code": 0, "msg": "deleted"})
}
