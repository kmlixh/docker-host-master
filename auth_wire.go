package main

import (
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/kmlixh/authing"
)

// authTool 验 /admin/* 的 admin token。
// 走的是 adminBackend 自己的逻辑 — 共享 Redis (db=3) 查 user_id_<token>。
//
// adminBackend 流程:
//   adminLogin 2FA 验过后 → GenTokenForUser → authing.SetToken(token, ...) → 写 Redis db=3
// 这里:
//   收 Token 头 → authing.ValidateToken → 查 Redis db=3 → 拿 (user_id, user_type, tenant_id)
//
// 不用 JWKS / OAuth issuer。完全跟 userLogin/admin_auth_wire.go 同款。
var authTool *authing.AuthTool

// InitAuthing 初始化 authing,只配 Redis(opaque token 路径)。
// 失败不致命,只 log warn,此时 /admin/* 全部 503。
func InitAuthing(cfg *Config) {
	if cfg.Redis.Addr == "" {
		log.Println("WARN: REDIS_ADDR 未设,/admin/* 路由将拒绝所有请求")
		return
	}
	t, err := authing.NewAuthTool(&authing.Config{
		RedisAddr:     cfg.Redis.Addr,
		RedisPassword: cfg.Redis.Password,
		RedisDB:       cfg.Redis.DB,
		// 故意不设 DBDriver/DBDSN — authing.NewAuthTool 已经按 RedisAddr 单独
		// 初始化(我们之前那个 commit 9bd89b0 让 Redis 跟 DB 都可选,只要有
		// RedisAddr 或 OAuthIssuer 至少一个就行)。
	})
	if err != nil {
		log.Printf("WARN: authing init failed: %v (/admin/* 全部 503)", err)
		return
	}
	authTool = t
	log.Printf("authing 初始化完成(Redis %s db=%d,验 adminBackend opaque admin token)",
		cfg.Redis.Addr, cfg.Redis.DB)
}

// adminAuthMiddleware /admin/* 鉴权
// 流程跟 userLogin/admin_auth_wire.go 的 adminAuthGuard 完全一致:
//   1. authTool 没初始化 → 503
//   2. 读 Token 头(也兼容 Authorization: Bearer xxx)
//   3. authing.ValidateToken → Redis 查 user_id_<token> 等键
//   4. tenant_id 必须 == "admin"(adminBackend 写 token 时设的 tenant)
//   5. 写 Locals 给 handler 用
func adminAuthMiddleware(c *fiber.Ctx) error {
	if authTool == nil {
		return c.Status(503).JSON(fiber.Map{
			"code": 503,
			"msg":  "authing not configured (REDIS_ADDR empty)",
		})
	}
	token := c.Get("Token")
	if token == "" {
		if v := c.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
			token = strings.TrimPrefix(v, "Bearer ")
		}
	}
	if token == "" {
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "missing Token header"})
	}
	uid, utype, tid, err := authTool.ValidateToken(c.Context(), token)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "invalid token: " + err.Error()})
	}
	if tid != "admin" {
		return c.Status(403).JSON(fiber.Map{
			"code": 403,
			"msg":  "admin tenant required (got tenant_id=" + tid + ")",
		})
	}
	c.Locals("user_id", uid)
	c.Locals("user_type", utype)
	c.Locals("tenant_id", tid)
	return c.Next()
}

// externalAuthMiddleware /external/* 鉴权 — 本地 token store + bcrypt
func externalAuthMiddleware(c *fiber.Ctx) error {
	if tokenStore == nil {
		return c.Status(503).JSON(fiber.Map{
			"code": 503,
			"msg":  "token store not initialized",
		})
	}
	tok := c.Get("X-Access-Token")
	if tok == "" {
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "missing X-Access-Token header"})
	}
	at, err := tokenStore.Verify(tok)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "invalid or expired access token"})
	}
	c.Locals("access_token_id", at.ID)
	c.Locals("access_token_name", at.Name)
	return c.Next()
}
