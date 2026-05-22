package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TokenStore = AccessToken 的持久化层
type TokenStore struct {
	db *gorm.DB
}

func NewTokenStore(cfg *Config) (*TokenStore, error) {
	gcfg := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	}
	db, err := gorm.Open(postgres.Open(cfg.GetDSN()), gcfg)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// 启动自动迁移(等价于跑 migrations/001_access_tokens.sql)
	// prod 推荐手动跑 SQL,这里只在表不存在时建,生产已存在的表不影响。
	if err := db.AutoMigrate(&AccessToken{}); err != nil {
		return nil, fmt.Errorf("automigrate: %w", err)
	}
	return &TokenStore{db: db}, nil
}

// Create 颁发一个新 token。返回 (plain_token, AccessToken_record, error)。
// plain 只在这次出现,DB 里只存 bcrypt hash。
func (s *TokenStore) Create(name, description, hostname string, expiresAt *time.Time) (string, *AccessToken, error) {
	plain, err := generateRandomToken(32)
	if err != nil {
		return "", nil, fmt.Errorf("gen token: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("bcrypt: %w", err)
	}
	row := &AccessToken{
		TokenHash:   string(hash),
		Name:        name,
		Description: description,
		Hostname:    hostname,
		Enabled:     true,
		ExpiresAt:   expiresAt,
	}
	if err := s.db.Create(row).Error; err != nil {
		return "", nil, err
	}
	return plain, row, nil
}

// Regenerate 给已有 token 重生 plain(老的立刻失效)
func (s *TokenStore) Regenerate(id uint) (string, error) {
	plain, err := generateRandomToken(32)
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	res := s.db.Model(&AccessToken{}).Where("id = ?", id).Updates(map[string]any{
		"token_hash": string(hash),
		"updated_at": time.Now(),
	})
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", errors.New("token not found")
	}
	return plain, nil
}

// List 列出所有 token (不带 token_hash,前端展示用)
func (s *TokenStore) List() ([]AccessToken, error) {
	var rows []AccessToken
	err := s.db.Order("created_at DESC").Find(&rows).Error
	return rows, err
}

// SetEnabled 启停
func (s *TokenStore) SetEnabled(id uint, enabled bool) error {
	res := s.db.Model(&AccessToken{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("token not found")
	}
	return nil
}

// Delete 删除
func (s *TokenStore) Delete(id uint) error {
	res := s.db.Delete(&AccessToken{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("token not found")
	}
	return nil
}

// Verify 校验外部请求带来的明文 token
//   - 遍历 enabled + 未过期 + hostname 匹配的所有 token,bcrypt CompareHashAndPassword
//   - 任一匹配返回该 AccessToken,顺便更新 last_used_at
//   - O(N) bcrypt,N 通常 < 10,~5ms × N,可接受
func (s *TokenStore) Verify(plain string) (*AccessToken, error) {
	if plain == "" {
		return nil, errors.New("empty token")
	}
	hostname, _ := os.Hostname()

	var candidates []AccessToken
	q := s.db.Where("enabled = ? AND (expires_at IS NULL OR expires_at > NOW())", true)
	// hostname scope:本机 hostname 或 全集群(hostname='')
	q = q.Where("hostname = ? OR hostname = ?", "", hostname)
	if err := q.Find(&candidates).Error; err != nil {
		return nil, err
	}

	for i := range candidates {
		if err := bcrypt.CompareHashAndPassword([]byte(candidates[i].TokenHash), []byte(plain)); err == nil {
			// 命中。异步更新 last_used_at,不阻塞响应
			id := candidates[i].ID
			go func() {
				now := time.Now()
				s.db.Model(&AccessToken{}).Where("id = ?", id).Update("last_used_at", now)
			}()
			return &candidates[i], nil
		}
	}
	return nil, errors.New("invalid token")
}

// generateRandomToken 出 N 字节随机 → base64url 编码
func generateRandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
