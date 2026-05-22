package main

import (
	"context"
	"log"
	"sync"
	"time"
)

// Daemon 是后台 goroutine,做两件事:
//  1. 订阅 docker 事件 → 实时 upsert/remove /etc/hosts
//  2. 周期性 reconcile (5min default) — 兜底丢事件 / 启动时全量同步
//
// 设计:启动失败(docker daemon 不可达)不让主进程崩,只 log warn,
// 服务还能跑 (Admin / External API 走 docker SDK 失败会自然 error 出来)。
type Daemon struct {
	docker *DockerClient
	hosts  *HostsManager
	cfg    *Config
	wg     sync.WaitGroup
}

func NewDaemon(d *DockerClient, h *HostsManager, cfg *Config) *Daemon {
	return &Daemon{docker: d, hosts: h, cfg: cfg}
}

// Start 启动 daemon goroutine,返回立即(不阻塞调用方)。
// ctx done 时 graceful stop。
func (dm *Daemon) Start(ctx context.Context) {
	dm.wg.Add(2)
	go dm.eventLoop(ctx)
	go dm.reconcileLoop(ctx)
}

// Wait 等 goroutine 退出(主程序 shutdown 时调)
func (dm *Daemon) Wait() {
	dm.wg.Wait()
}

// eventLoop 订阅 docker 事件流,实时 upsert/remove。
// docker daemon 重启会断流,这里加自动重连。
func (dm *Daemon) eventLoop(ctx context.Context) {
	defer dm.wg.Done()
	log.Println("daemon: event loop started")

	for {
		select {
		case <-ctx.Done():
			log.Println("daemon: event loop exit (ctx done)")
			return
		default:
		}

		msgs, errs := dm.docker.Events(ctx)
		// 进入消费循环
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgs:
				if !ok {
					log.Println("daemon: event stream closed, will retry in 3s")
					goto retry
				}
				dm.handleEvent(ctx, msg.Actor.ID, string(msg.Action))
			case err, ok := <-errs:
				if !ok {
					log.Println("daemon: event errors channel closed, will retry in 3s")
					goto retry
				}
				if err != nil {
					log.Printf("daemon: event stream err: %v (retry in 3s)", err)
					goto retry
				}
			}
		}
	retry:
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// handleEvent 把 docker 事件翻译成 hosts 操作
func (dm *Daemon) handleEvent(ctx context.Context, containerID, action string) {
	switch action {
	case "start":
		// 启动 → inspect → upsert
		info, err := dm.docker.Inspect(ctx, containerID)
		if err != nil {
			log.Printf("daemon: inspect %s failed: %v", containerID[:12], err)
			return
		}
		entry, err := ExtractEntry(info)
		if err != nil {
			log.Printf("daemon: extract entry %s: %v (skip)", containerID[:12], err)
			return
		}
		if err := dm.hosts.AddOrUpdate(entry); err != nil {
			log.Printf("daemon: hosts upsert %s: %v", entry.ContainerName, err)
		} else {
			log.Printf("daemon: hosts + %s %s (%s)", entry.IP, entry.ContainerName, entry.Hostname)
		}

	case "die", "destroy", "stop", "kill":
		// 死掉 / 删除 → 摘 hosts。stop 也摘是为了用户停容器后不让 hosts 留 stale 条目;
		// 真的想保留就 pause 不要 stop。
		if err := dm.hosts.Remove(containerID); err != nil {
			log.Printf("daemon: hosts remove %s: %v", containerID[:12], err)
		} else {
			log.Printf("daemon: hosts - %s", containerID[:12])
		}

	default:
		// 其它事件(pause/unpause/rename/...)不影响 IP,忽略
	}
}

// reconcileLoop 每 N 秒全量同步一次:
//   - 列出所有 running 容器
//   - 比对 /etc/hosts managed 块
//   - 不一致的全部重写
//
// 兜底:防事件流断连丢事件 / 服务重启时 catch up。
func (dm *Daemon) reconcileLoop(ctx context.Context) {
	defer dm.wg.Done()

	interval := time.Duration(dm.cfg.Hosts.ReconcileIntervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	log.Printf("daemon: reconcile loop started (every %s)", interval)

	// 启动时立即跑一次
	dm.reconcileOnce(ctx)

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("daemon: reconcile loop exit (ctx done)")
			return
		case <-t.C:
			dm.reconcileOnce(ctx)
		}
	}
}

// reconcileOnce 全量重写一次 hosts managed 块
func (dm *Daemon) reconcileOnce(ctx context.Context) {
	containers, err := dm.docker.ListContainers(ctx, false) // 只 running
	if err != nil {
		log.Printf("daemon: reconcile list containers: %v", err)
		return
	}

	want := make([]HostEntry, 0, len(containers))
	for _, c := range containers {
		// list 拿到的 c.ID 是 full ID,直接 inspect 获取详情
		info, err := dm.docker.Inspect(ctx, c.ID)
		if err != nil {
			log.Printf("daemon: reconcile inspect %s: %v (skip)", c.ID[:12], err)
			continue
		}
		entry, err := ExtractEntry(info)
		if err != nil {
			// 没 IP — 跳过(很正常,e.g. network=host 模式)
			continue
		}
		want = append(want, entry)
	}

	if err := dm.hosts.Replace(want); err != nil {
		log.Printf("daemon: reconcile hosts replace: %v", err)
		return
	}
	log.Printf("daemon: reconcile done, %d entries in managed block", len(want))
}
