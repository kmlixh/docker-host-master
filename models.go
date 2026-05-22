package main

import "time"

// AccessToken 是 /external/* 路由的认证凭证(类似 OAuth client_secret)。
// 颁发流程跟 userLogin/oauth_admin_handlers.go 的 OAuth client 创建一致:
//   1. 服务端 generateRandomToken() 出明文
//   2. bcrypt 哈希后存 DB
//   3. 明文只返回一次,后续永远拿不到
//   4. 校验时:外部请求带 X-Access-Token: <plain> → 遍历 enabled tokens →
//      bcrypt CompareHashAndPassword → 匹配的就是合法 token
//
// hostname 字段是为了支持 "全集群共享一个 DB 但 token 按 host scope" 的部署模式:
//   - 空 = 全 host 通用
//   - 非空 = 只在当前 host 启用(每个 docker-host-master 实例只看自己 hostname 的)
type AccessToken struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	TokenHash  string     `gorm:"size:255;not null" json:"-"` // bcrypt hash,不出口
	Name       string     `gorm:"size:128;not null" json:"name"`
	Description string    `gorm:"size:255" json:"description"`
	Hostname   string     `gorm:"size:128;index" json:"hostname"` // 限定 host,空=全集群
	Enabled    bool       `gorm:"not null;default:true" json:"enabled"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (AccessToken) TableName() string { return "dhm_access_tokens" }
