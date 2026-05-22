package main

import "time"

// AccessToken — /external/* 路由的认证凭证(类似 OAuth client_secret)。
//
// 现在本地 JSON 文件存(无 DB),id 内存自增。颁发流程:
//   1. 服务端 generateRandomToken() 出明文
//   2. bcrypt 哈希后存 token store(写 JSON 文件)
//   3. 明文只返回一次,后续永远拿不到
//   4. 校验时:外部请求带 X-Access-Token: <plain> → 遍历 enabled tokens →
//      bcrypt CompareHashAndPassword → 匹配的就是合法 token
//
// hostname:空 = 全集群通用;非空 = 只在该 host 启用(每实例只看自己 hostname 的)
type AccessToken struct {
	ID          uint       `json:"id"`
	TokenHash   string     `json:"token_hash"`              // bcrypt hash,plain 永远不入库
	TokenPrefix string     `json:"token_prefix,omitempty"`  // plain 的前 8 字符,UI 列表用来区分 token,非敏感
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Hostname    string     `json:"hostname,omitempty"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

// TokenPrefixLen 是 UI 列表展示用的"头部"字符数。
// 8 字符 → ~48 bit 熵,远不够暴力 → 不构成泄露风险(bcrypt full hash 也不能从这反推出 plain)。
// 长度跟 GitHub PAT 显示模式接近。
const TokenPrefixLen = 8

// makeTokenPrefix 从 plain 摘头部
func makeTokenPrefix(plain string) string {
	if len(plain) <= TokenPrefixLen {
		return plain
	}
	return plain[:TokenPrefixLen]
}
