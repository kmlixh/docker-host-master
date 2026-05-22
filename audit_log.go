package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// audit_log.go — 简单单文件追加 audit log。
// 外部调用必须留痕:谁(token name)、何时、对哪个容器、做什么、结果。
// 同时 stdout 也打印一份(便于 docker logs 直接看)。
//
// 文件不滚转,文件锁防并发(单实例进程内基本不会冲突,但 flock 防 docker logrotate 之类外部进程)。

var (
	auditFile *os.File
	auditMu   sync.Mutex
)

// InitAuditLog 启动时打开 audit log 文件(append mode)。
// 失败不致命,只 log warn,后续 AuditLog 只 stdout。
func InitAuditLog(cfg *Config) {
	path := cfg.Audit.LogFile
	if path == "" {
		path = "/var/log/docker-host-master/audit.log"
	}
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		log.Printf("WARN: audit dir %s: %v (只 stdout)", filepath.Dir(path), err)
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		log.Printf("WARN: audit log %s open: %v (只 stdout)", path, err)
		return
	}
	auditFile = f
	log.Printf("audit log: %s", path)
}

// CloseAuditLog 关闭 audit file(graceful shutdown 调)
func CloseAuditLog() {
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditFile != nil {
		auditFile.Close()
		auditFile = nil
	}
}

// AuditLog 写一行 audit。
//   tokenName: access_token 的 name(从 Locals 拿,匿名调用为 "")
//   action:    start/stop/restart/pause/...
//   container: 容器 id 或 name
//   result:    "ok" 或 "err"
//   errMsg:    err 描述,无 err 时空
func AuditLog(tokenName, action, container, result, errMsg string) {
	line := fmt.Sprintf("[%s] token=%q action=%s container=%s result=%s err=%q\n",
		time.Now().Format(time.RFC3339),
		tokenName, action, container, result, errMsg,
	)
	// stdout 永远打
	fmt.Print(line)

	// 文件可选
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditFile != nil {
		auditFile.WriteString(line)
	}
}
