package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// HostsManager 负责 /etc/hosts 的 atomic 读写。
//
// 文件结构:
//
//	... user 自己写的条目 ...
//	# BEGIN docker-host-master (DO NOT EDIT)   ← BeginMarker
//	172.17.0.5  my_app   my-hostname  # container_id=<sha>
//	172.17.0.7  postgres pg-prod      # container_id=<sha>
//	# END docker-host-master                    ← EndMarker
//	... 更多 user 条目 ...
//
// 只动 BeginMarker / EndMarker 之间的内容,user 自己写的一律保留。
type HostsManager struct {
	path        string
	beginMarker string
	endMarker   string
	mu          sync.Mutex // 防多 goroutine 并发写;flock 防多进程
}

func NewHostsManager(path, beginMarker, endMarker string) *HostsManager {
	if beginMarker == "" {
		beginMarker = "# BEGIN docker-host-master (DO NOT EDIT)"
	}
	if endMarker == "" {
		endMarker = "# END docker-host-master"
	}
	return &HostsManager{
		path:        path,
		beginMarker: beginMarker,
		endMarker:   endMarker,
	}
}

// ReadManaged 读出当前 managed 块里的条目(只解析有 `container_id=` 注释的行)。
// 给 GET /admin/hosts 用,也给 reconcile diff 用。
func (m *HostsManager) ReadManaged() ([]HostEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.path)
	if err != nil {
		return nil, err
	}
	_, managed, _ := splitByMarkers(string(data), m.beginMarker, m.endMarker)
	return parseManagedBlock(managed), nil
}

// Replace 用 entries 整段替换 managed 块。
// 老 user 条目保留,只动 begin/end marker 之间的内容。
//
// flock(LOCK_EX) 防止跟其它进程(docker daemon 自己写 hosts、netplan、cloud-init)
// 并发写丢失;flock 是 advisory 的,需要双方都 flock 才生效,但比没有强。
//
// 写完用临时文件 + rename 原子替换,避免半写状态。
//
// 注:当 /etc/hosts 是 bind mount 到宿主时,rename 会失败(跨 device or
// 文件不能换 inode)。此时退回到原地覆盖写(读 mtime 校验防 TOCTOU)。
func (m *HostsManager) Replace(entries []HostEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 读全文 + flock
	f, err := os.OpenFile(m.path, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", m.path, err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		log.Printf("WARN: flock(LOCK_EX) %s failed: %v (continuing without lock)", m.path, err)
	} else {
		defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}

	raw, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", m.path, err)
	}

	// 拼新文件内容
	before, _, after := splitByMarkers(string(raw), m.beginMarker, m.endMarker)
	newContent := composeFile(before, entries, after, m.beginMarker, m.endMarker)

	// 容器内 bind mount 单文件,rename 不能跨 inode → 直接原地覆盖
	if err := atomicWrite(m.path, []byte(newContent), f); err != nil {
		return fmt.Errorf("write %s: %w", m.path, err)
	}
	return nil
}

// AddOrUpdate 单个条目 upsert(读 → 替换/追加 → 写)
func (m *HostsManager) AddOrUpdate(entry HostEntry) error {
	if !entry.Valid() {
		return fmt.Errorf("invalid entry: %+v", entry)
	}
	current, err := m.ReadManaged()
	if err != nil {
		return err
	}
	out := make([]HostEntry, 0, len(current)+1)
	replaced := false
	for _, e := range current {
		if e.ContainerID == entry.ContainerID {
			out = append(out, entry)
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, entry)
	}
	return m.Replace(out)
}

// Remove 按 container_id 删
func (m *HostsManager) Remove(containerID string) error {
	current, err := m.ReadManaged()
	if err != nil {
		return err
	}
	out := make([]HostEntry, 0, len(current))
	for _, e := range current {
		if e.ContainerID == containerID {
			continue
		}
		out = append(out, e)
	}
	return m.Replace(out)
}

// --- 内部工具 ---

// splitByMarkers 把文件内容切成 before / managed / after 三段。
// 没有 marker 时:before=全部, managed="", after=""
func splitByMarkers(content, begin, end string) (before, managed, after string) {
	lines := strings.Split(content, "\n")
	beginIdx, endIdx := -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == begin {
			beginIdx = i
		} else if t == end && beginIdx >= 0 {
			endIdx = i
			break
		}
	}
	if beginIdx < 0 || endIdx < 0 {
		return content, "", ""
	}
	before = strings.Join(lines[:beginIdx], "\n")
	managed = strings.Join(lines[beginIdx+1:endIdx], "\n")
	after = strings.Join(lines[endIdx+1:], "\n")
	return
}

// composeFile 把 before + new managed block + after 拼起来。
// entries 为空时:整个 managed block(含 marker)都不写,文件回到无 marker 状态。
func composeFile(before string, entries []HostEntry, after, begin, end string) string {
	var buf bytes.Buffer
	buf.WriteString(strings.TrimRight(before, "\n"))
	if buf.Len() > 0 {
		buf.WriteString("\n")
	}
	if len(entries) > 0 {
		sortEntries(entries)
		buf.WriteString(begin)
		buf.WriteString("\n")
		for _, e := range entries {
			line := e.Line()
			if line == "" {
				continue
			}
			buf.WriteString(line)
			buf.WriteString("\n")
		}
		buf.WriteString(end)
		buf.WriteString("\n")
	}
	tail := strings.TrimLeft(after, "\n")
	if tail != "" {
		buf.WriteString(tail)
		if !strings.HasSuffix(tail, "\n") {
			buf.WriteString("\n")
		}
	}
	return buf.String()
}

// parseManagedBlock 把 managed 段(begin/end 中间的内容)解析回 HostEntry 列表。
// 只处理有 `# container_id=<id>` 注释的行,其它(空行/手注释)忽略。
func parseManagedBlock(block string) []HostEntry {
	out := []HostEntry{}
	for _, line := range strings.Split(block, "\n") {
		raw := strings.TrimSpace(line)
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		// 拆 "<ip>\t<n1> <n2> ...# container_id=<id>"
		commentSplit := strings.SplitN(raw, "#", 2)
		head := strings.TrimSpace(commentSplit[0])
		fields := strings.Fields(head)
		if len(fields) < 2 {
			continue
		}
		entry := HostEntry{
			IP:            fields[0],
			ContainerName: fields[1],
		}
		if len(fields) >= 3 {
			entry.Hostname = fields[2]
		}
		if len(commentSplit) == 2 {
			cmt := strings.TrimSpace(commentSplit[1])
			if v, ok := stripPrefix(cmt, "container_id="); ok {
				entry.ContainerID = v
			}
		}
		out = append(out, entry)
	}
	return out
}

func stripPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// atomicWrite 优先 temp+rename,失败回退到原地 truncate+write(bind mount 场景)。
//
// 一旦在当前进程内检测过 rename 不支持(典型场景:/etc/hosts 是 bind mount 单文件,
// inode 被宿主锁定 → rename 报 EBUSY),后续直接走 in-place 不再尝试 — 避免每次
// reconcile/docker event 都刷一条同样的 WARN(那玩意儿一小时几十条没用的噪音)。
//
// renameBroken 是包级 bool,无 atomic 也安全:atomicWrite 永远在 HostsManager.mu
// 持锁下调用 (Append/Remove/Reconcile 等公开方法都先 m.mu.Lock())。
var (
	renameBroken     bool
	renameBrokenOnce sync.Once
)

func atomicWrite(path string, data []byte, currentFile *os.File) error {
	if !renameBroken {
		dir := filepath.Dir(path)
		tmp, err := os.CreateTemp(dir, ".hosts.tmp.*")
		if err == nil {
			_, werr := tmp.Write(data)
			tmp.Close()
			if werr == nil {
				if rerr := os.Rename(tmp.Name(), path); rerr == nil {
					return nil // 走得通,正常路径
				} else {
					// 第一次失败:记一次 INFO 然后永久切 in-place 模式
					renameBrokenOnce.Do(func() {
						log.Printf("hosts: temp+rename 不可用 (%v) — 通常因为 %s 是 bind mount 单文件,inode 锁定;切到 in-place truncate+write 模式,后续静默", rerr, path)
					})
					renameBroken = true
				}
			}
			os.Remove(tmp.Name())
		}
	}
	// fallback / 永久路径:原地 truncate + write,currentFile 已 flock
	if _, err := currentFile.Seek(0, 0); err != nil {
		return err
	}
	if err := currentFile.Truncate(0); err != nil {
		return err
	}
	_, err := currentFile.Write(data)
	return err
}
