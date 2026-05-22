package main

import (
	"fmt"
	"sort"
	"strings"
)

// HostEntry 是 /etc/hosts 里的一行(被 docker-host-master 管理的)。
//
// 格式:`<IP> <container_name> <hostname>` (两个 alias 都写,见 plan)。
// 如果 Hostname 为空或等于 ContainerName,只写一个名字。
type HostEntry struct {
	IP            string // 容器 IP (NetworkSettings 或 Networks[首个].IPAddress)
	ContainerID   string // 用作 reconcile/diff 的稳定 key
	ContainerName string // strip leading '/' 后的 docker 容器名
	Hostname      string // 容器 Config.Hostname (一般等于 short container id 除非显式 -h)
}

// Line 把 HostEntry 序列化为 /etc/hosts 一行(不带换行)。
// 末尾追加 "# container_id=<id>" 注释,reconcile 时按这个 key diff。
func (h HostEntry) Line() string {
	names := []string{}
	if h.ContainerName != "" {
		names = append(names, h.ContainerName)
	}
	if h.Hostname != "" && h.Hostname != h.ContainerName {
		names = append(names, h.Hostname)
	}
	if len(names) == 0 {
		return ""
	}
	// IP\t<alias>... # container_id=<id>
	return fmt.Sprintf("%s\t%s\t# container_id=%s",
		h.IP, strings.Join(names, " "), h.ContainerID)
}

// Valid 判断条目能否写入(必须有 IP + 至少一个 name)
func (h HostEntry) Valid() bool {
	if h.IP == "" {
		return false
	}
	if h.ContainerName == "" && h.Hostname == "" {
		return false
	}
	return true
}

// sortEntries 让 hosts block 输出稳定:按 IP 排序,IP 相同按 name
func sortEntries(es []HostEntry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].IP != es[j].IP {
			return es[i].IP < es[j].IP
		}
		return es[i].ContainerName < es[j].ContainerName
	})
}
