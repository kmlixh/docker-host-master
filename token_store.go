package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TokenStore — access_token 的本地持久化层。
//
// 实现:JSON 文件 + RWMutex。
//   - 启动 LoadTokenStore 读全量到 map[id]AccessToken,id 内存自增计数
//   - 写时(Create/Regenerate/SetEnabled/Delete)全量 dump 到 file (temp+rename atomic)
//   - 读(List/Verify)走内存,零磁盘 IO
//
// 数据量假设 < 100 个 token,全量 dump 性能完全可接受。
// 文件格式:JSON object,key 是 id 字符串,value 是 AccessToken。同时存 next_id。
//
// 比 gorm+postgres 简单两个量级:
//   - 没 schema 迁移
//   - 没数据库连接管理
//   - 没 SQL
//   - 数据可读 (cat tokens.json 就能看)
//   - 备份/恢复 = cp 文件
type TokenStore struct {
	mu     sync.RWMutex
	path   string
	tokens map[uint]*AccessToken
	nextID uint
}

// fileSnapshot 是 dump/load 的中间格式
type fileSnapshot struct {
	NextID uint                     `json:"next_id"`
	Tokens map[string]*AccessToken `json:"tokens"`
}

// LoadTokenStore 启动时调一次,从文件 load 或创建空 store。
// 文件不存在 = 全新部署,空 store。
func LoadTokenStore(path string) (*TokenStore, error) {
	// 确保父目录存在
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("mkdir for token store: %w", err)
	}

	s := &TokenStore{
		path:   path,
		tokens: make(map[uint]*AccessToken),
		nextID: 1,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("token store: %s not found, starting fresh", path)
			return s, nil
		}
		return nil, fmt.Errorf("read token store: %w", err)
	}

	var snap fileSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse token store JSON: %w", err)
	}
	if snap.Tokens == nil {
		snap.Tokens = map[string]*AccessToken{}
	}
	for k, v := range snap.Tokens {
		var id uint
		fmt.Sscanf(k, "%d", &id)
		if id == 0 {
			continue
		}
		s.tokens[id] = v
	}
	if snap.NextID > 0 {
		s.nextID = snap.NextID
	} else {
		// 老数据无 NextID,扫一遍取 max+1
		for id := range s.tokens {
			if id+1 > s.nextID {
				s.nextID = id + 1
			}
		}
	}
	log.Printf("token store: loaded %d tokens from %s (next_id=%d)",
		len(s.tokens), path, s.nextID)
	return s, nil
}

// dump 把当前 in-mem 全量写到 file (atomic via temp+rename)
// 调用方必须已持 s.mu 锁
func (s *TokenStore) dump() error {
	snap := fileSnapshot{
		NextID: s.nextID,
		Tokens: make(map[string]*AccessToken, len(s.tokens)),
	}
	for id, t := range s.tokens {
		snap.Tokens[fmt.Sprintf("%d", id)] = t
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".tokens.tmp.*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("write temp: %w", err)
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Create 颁发新 token,返一次明文 + record
func (s *TokenStore) Create(name, description, hostname string, expiresAt *time.Time) (string, *AccessToken, error) {
	plain, err := generateRandomToken(32)
	if err != nil {
		return "", nil, fmt.Errorf("gen token: %w", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", nil, fmt.Errorf("bcrypt: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	row := &AccessToken{
		ID:          s.nextID,
		TokenHash:   string(hash),
		Name:        name,
		Description: description,
		Hostname:    hostname,
		Enabled:     true,
		ExpiresAt:   expiresAt,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.tokens[s.nextID] = row
	s.nextID++

	if err := s.dump(); err != nil {
		// 回滚 in-mem
		delete(s.tokens, row.ID)
		s.nextID--
		return "", nil, fmt.Errorf("persist: %w", err)
	}
	return plain, row, nil
}

func (s *TokenStore) Regenerate(id uint) (string, error) {
	plain, err := generateRandomToken(32)
	if err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tokens[id]
	if !ok {
		return "", errors.New("token not found")
	}
	oldHash := t.TokenHash
	t.TokenHash = string(hash)
	t.UpdatedAt = time.Now()

	if err := s.dump(); err != nil {
		t.TokenHash = oldHash
		return "", fmt.Errorf("persist: %w", err)
	}
	return plain, nil
}

func (s *TokenStore) List() ([]AccessToken, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]AccessToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		out = append(out, *t) // 值拷贝防 caller 篡改
	}
	// 按 created_at desc 排
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *TokenStore) SetEnabled(id uint, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tokens[id]
	if !ok {
		return errors.New("token not found")
	}
	old := t.Enabled
	t.Enabled = enabled
	t.UpdatedAt = time.Now()
	if err := s.dump(); err != nil {
		t.Enabled = old
		return err
	}
	return nil
}

func (s *TokenStore) Delete(id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.tokens[id]; !ok {
		return errors.New("token not found")
	}
	backup := s.tokens[id]
	delete(s.tokens, id)
	if err := s.dump(); err != nil {
		s.tokens[id] = backup
		return err
	}
	return nil
}

// Verify 校验外部明文 token
//   遍历 enabled + 未过期 + hostname 匹配的所有 → bcrypt 比对
//   命中:更新 last_used_at,返 record
func (s *TokenStore) Verify(plain string) (*AccessToken, error) {
	if plain == "" {
		return nil, errors.New("empty token")
	}
	hostname, _ := os.Hostname()
	now := time.Now()

	s.mu.RLock()
	candidates := make([]*AccessToken, 0, len(s.tokens))
	for _, t := range s.tokens {
		if !t.Enabled {
			continue
		}
		if t.ExpiresAt != nil && t.ExpiresAt.Before(now) {
			continue
		}
		if t.Hostname != "" && t.Hostname != hostname {
			continue
		}
		candidates = append(candidates, t)
	}
	s.mu.RUnlock()

	for _, t := range candidates {
		if err := bcrypt.CompareHashAndPassword([]byte(t.TokenHash), []byte(plain)); err == nil {
			// 异步更新 last_used_at,不阻塞
			id := t.ID
			go func() {
				s.mu.Lock()
				if tt, ok := s.tokens[id]; ok {
					nowTS := time.Now()
					tt.LastUsedAt = &nowTS
					_ = s.dump() // 失败也没关系,下次再更新
				}
				s.mu.Unlock()
			}()
			return t, nil
		}
	}
	return nil, errors.New("invalid token")
}

func generateRandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
