package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	dockerclient "github.com/docker/docker/client"
)

// DockerClient 是 docker SDK 的薄包装,只暴露我们需要的几个操作。
// 用 unix socket 连本机 daemon(/var/run/docker.sock,容器内 bind mount 进来)。
type DockerClient struct {
	cli     *dockerclient.Client
	timeout time.Duration
}

func NewDockerClient(endpoint string, timeoutSec int) (*DockerClient, error) {
	if endpoint == "" {
		endpoint = "unix:///var/run/docker.sock"
	}
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost(endpoint),
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &DockerClient{
		cli:     cli,
		timeout: time.Duration(timeoutSec) * time.Second,
	}, nil
}

func (d *DockerClient) Close() error { return d.cli.Close() }

// Ping 探测 docker daemon 是否可达,返 server version
func (d *DockerClient) Ping(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	p, err := d.cli.Ping(ctx)
	if err != nil {
		return "", err
	}
	return p.APIVersion, nil
}

// ListContainers 列出所有容器(含已停止)
func (d *DockerClient) ListContainers(ctx context.Context, all bool) ([]types.Container, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.cli.ContainerList(ctx, container.ListOptions{All: all})
}

// Inspect 拿单个容器详情
func (d *DockerClient) Inspect(ctx context.Context, id string) (types.ContainerJSON, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.cli.ContainerInspect(ctx, id)
}

// Start / Stop / Restart / Pause / Unpause / Remove
func (d *DockerClient) Start(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.cli.ContainerStart(ctx, id, container.StartOptions{})
}

func (d *DockerClient) Stop(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	timeoutSec := int(d.timeout.Seconds())
	return d.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeoutSec})
}

func (d *DockerClient) Restart(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	timeoutSec := int(d.timeout.Seconds())
	return d.cli.ContainerRestart(ctx, id, container.StopOptions{Timeout: &timeoutSec})
}

func (d *DockerClient) Pause(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.cli.ContainerPause(ctx, id)
}

func (d *DockerClient) Unpause(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.cli.ContainerUnpause(ctx, id)
}

func (d *DockerClient) Remove(ctx context.Context, id string, force bool) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	return d.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: force})
}

// Logs 返回容器 stdout/stderr 流(WebSocket / SSE 用)。
// tail 0 表示从头开始;非 0 取最后 N 行(字符串形式,docker API 要求)。
// caller 用完必须 Close。
func (d *DockerClient) Logs(ctx context.Context, id string, tail int, follow bool) (io.ReadCloser, error) {
	tailStr := "all"
	if tail > 0 {
		tailStr = fmt.Sprintf("%d", tail)
	}
	// 注:Logs 不应该用 timeout context(follow=true 时会阻塞读)
	return d.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       tailStr,
		Timestamps: false,
	})
}

// Events 订阅 docker daemon 事件流。
// 只过滤 type=container 的(start/die/destroy/create 等)。
// caller 应在 ctx done 时停止消费。
func (d *DockerClient) Events(ctx context.Context) (<-chan events.Message, <-chan error) {
	args := filters.NewArgs()
	args.Add("type", "container")
	return d.cli.Events(ctx, events.ListOptions{Filters: args})
}

// ExtractEntry 从 inspect 结果挑出 IP + name + hostname 拼成 HostEntry。
// IP 选择策略:
//   - NetworkSettings.IPAddress 非空 → 用它(default bridge 网络)
//   - 否则取 NetworkSettings.Networks 第一个 map entry 的 IPAddress
//   - 都空 → 返 error(条目不写)
func ExtractEntry(info types.ContainerJSON) (HostEntry, error) {
	if info.ContainerJSONBase == nil {
		return HostEntry{}, errors.New("inspect base nil")
	}
	name := strings.TrimPrefix(info.Name, "/")
	host := ""
	if info.Config != nil {
		host = info.Config.Hostname
	}

	ip := ""
	if info.NetworkSettings != nil {
		if info.NetworkSettings.IPAddress != "" {
			ip = info.NetworkSettings.IPAddress
		} else {
			for _, net := range info.NetworkSettings.Networks {
				if net != nil && net.IPAddress != "" {
					ip = net.IPAddress
					break
				}
			}
		}
	}
	if ip == "" {
		return HostEntry{}, fmt.Errorf("no IP for container %s", info.ID)
	}
	return HostEntry{
		IP:            ip,
		ContainerID:   info.ID,
		ContainerName: name,
		Hostname:      host,
	}, nil
}
