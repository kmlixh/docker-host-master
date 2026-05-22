package main

import (
	"errors"
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/kmlixh/authing"
)

// authTool 是全局 authing.AuthTool 实例,验 admin 路由的 ES256 JWT。
// nil 表示 OAuthIssuer 没配 → /admin/* 路由会被锁住(开发模式可手动设特殊 issuer)
var authTool *authing.AuthTool

// InitAuthing 初始化 authing JWT validator。失败不致命,只 log warn,
// 此时 /admin/* 会变成"全部拒绝",外部 /external/* 不影响。
func InitAuthing(cfg *Config) {
	if cfg.OAuth.Issuer == "" {
		log.Println("WARN: OAuth.Issuer 没配,/admin/* 路由将拒绝所有请求")
		return
	}
	t, err := authing.NewJWTValidator(cfg.OAuth.Issuer)
	if err != nil {
		log.Printf("WARN: authing init failed (%v), /admin/* 将拒绝所有请求", err)
		return
	}
	authTool = t
	log.Printf("authing JWT validator 初始化完成 (issuer=%s)", cfg.OAuth.Issuer)
}

// adminAuthMiddleware 是 /admin/* 路由的认证中间件。
// 流程:
//  1. authTool 没初始化 → 503(IdP issuer 没配)
//  2. 读 Token 头(adminFront / adminBackend 标准),空 → 401
//  3. authing.ValidateToken → 失败 → 401
//  4. 检查 tenant_id == "admin"(只有 admin tenant 的人能管 docker)→ 失败 403
//  5. 写 Locals 给下游 handler 用
//
// Token 来源:adminBackend 登录后颁发的 admin token,共享 Redis(db=3),
// 跟 userLogin/admin_auth_wire.go 的 adminAuthGuard 是同一套机制。
func adminAuthMiddleware(c *fiber.Ctx) error {
	if authTool == nil {
		return c.Status(503).JSON(fiber.Map{
			"code": 503,
			"msg":  "authing not configured (OAuth.Issuer empty in consul config)",
		})
	}

	token := c.Get("Token")
	if token == "" {
		// 也接受 Authorization: Bearer <token>
		if v := c.Get("Authorization"); strings.HasPrefix(v, "Bearer ") {
			token = strings.TrimPrefix(v, "Bearer ")
		}
	}
	if token == "" {
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "missing Token header"})
	}

	uid, utype, tid, err := authTool.ValidateToken(c.Context(), token)
	if err != nil {
		return c.Status(401).JSON(fiber.Map{
			"code": 401,
			"msg":  "invalid token: " + err.Error(),
		})
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

// externalAuthMiddleware 是 /external/* 路由的认证中间件。
// 头 X-Access-Token 必填;tokenStore.Verify 走 bcrypt 比对。
// 通过后 c.Locals("access_token_name", ...) 给审计 log 用。
func externalAuthMiddleware(c *fiber.Ctx) error {
	if tokenStore == nil {
		return c.Status(503).JSON(fiber.Map{
			"code": 503,
			"msg":  "token store not initialized (DB 没连上 — 见启动日志)",
		})
	}
	tok := c.Get("X-Access-Token")
	if tok == "" {
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "missing X-Access-Token header"})
	}
	at, err := tokenStore.Verify(tok)
	if err != nil {
		// 不暴露具体原因,统一 401
		return c.Status(401).JSON(fiber.Map{"code": 401, "msg": "invalid or expired access token"})
	}
	c.Locals("access_token_id", at.ID)
	c.Locals("access_token_name", at.Name)
	return c.Next()
}

var _ = errors.New // 占位防 import 误删
