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
	TokenHash   string     `json:"token_hash"` // bcrypt hash,JSON 文件里也是哈希,不是 plain
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Hostname    string     `json:"hostname,omitempty"`
	Enabled     bool       `json:"enabled"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}
