# docker-host-master

host-level Docker 容器管理 + 自动 `/etc/hosts` 维护 + 外部 access_token 保护的 REST API。

每台物理机部署一个实例,管本机的 docker。**无 Consul、无 postgres、无配置文件 — 纯 env vars + 一个 JSON 文件。**

## 三大功能

1. **自动 /etc/hosts** —— 监听 docker events,容器起来自动加 `<IP> <name> <hostname>`,容器死了删
2. **adminFront 管理页** —— 容器列表 / 启停 / 日志 / access_token 颁发
3. **外部 REST API** —— `/external/*`,受 access_token 保护,给 CI/CD 启停容器

## 鉴权拓扑

| 路由 | 鉴权方式 | 数据存哪 |
|---|---|---|
| `/admin/*` | adminBackend 颁的 opaque admin token,Token 头 | **共享 Redis db=3**(跟 adminBackend.authing.redis.database 对齐) |
| `/external/*` | 自己颁发的 access_token,X-Access-Token 头,bcrypt 哈希比对 | **本地 JSON 文件**(`/var/lib/docker-host-master/tokens.json`) |
| `/health` | 无 | — |

## 配置(全 env vars)

| env | 默认 | 说明 |
|---|---|---|
| `SERVER_PORT` | 8090 | HTTP 监听端口 |
| `SERVICE_NAME` | docker-host-master | 自身名字,只用于日志 |
| `DOCKER_ENDPOINT` | `unix:///var/run/docker.sock` | docker daemon 连接 |
| `DOCKER_TIMEOUT_SEC` | 30 | docker API 调用超时 |
| `HOSTS_FILE` | `/etc/hosts` | 容器内 bind mount 进来的宿主 hosts |
| `HOSTS_BEGIN_MARKER` | `# BEGIN docker-host-master (DO NOT EDIT)` | managed 块起始 |
| `HOSTS_END_MARKER` | `# END docker-host-master` | managed 块结束 |
| `HOSTS_RECONCILE_INTERVAL_SEC` | 300 | 5min 全量同步兜底 |
| **`REDIS_ADDR`** | (空) | **必填** — 共享 Redis 地址 (例 `172.17.0.1:6379`)。空 → /admin/* 全 503 |
| `REDIS_PASSWORD` | (空) | |
| `REDIS_DB` | 3 | **必须跟 adminBackend.authing.redis.database 对齐** |
| `TOKEN_STORE_FILE` | `/var/lib/docker-host-master/tokens.json` | 本地 JSON 文件 |
| `AUDIT_LOG` | `/var/log/docker-host-master/audit.log` | external 调用审计 |

启动 log 会 warn 缺什么必填 + 对应路由会怎么 503。

## 部署

每台主机一次 `docker run`,无前置 DB 初始化:

```bash
docker run -d --name docker-host-master \
  -v /var/run/docker.sock:/var/run/docker.sock:rw \
  -v /etc/hosts:/etc/hosts:rw \
  -v /var/lib/docker-host-master:/var/lib/docker-host-master:rw \
  -v /var/log/docker-host-master:/var/log/docker-host-master:rw \
  --cap-add DAC_OVERRIDE \
  -p 8090:8090 \
  -e REDIS_ADDR=172.17.0.1:6379 \
  -e REDIS_PASSWORD='your-redis-password' \
  -e REDIS_DB=3 \
  --restart unless-stopped \
  harbor.url/docker-host-master:vXXXX
```

**关键 mount**:
- `/var/run/docker.sock` → 容器内能调宿主 docker daemon
- `/etc/hosts` → 自动维护宿主 hosts
- `/var/lib/docker-host-master` → access_token JSON 文件持久化(重启不丢)
- `--cap-add DAC_OVERRIDE` → 写 /etc/hosts(避免 `--privileged`)

## 验证

```bash
# /health 应该立即可用
curl http://<host>:8090/health
# → {"status":"ok","service":"docker-host-master","hostname":"X","docker_api":"1.x"}

# /etc/hosts daemon
docker run -d --name test-nginx nginx:alpine
sleep 2
cat /etc/hosts | grep test-nginx
# → 172.x.x.x  test-nginx <hostname>  # container_id=...

# admin 端(需要先去 adminFront 登录拿 admin token)
curl -H "Token: $ADMIN_TOKEN" http://<host>:8090/admin/containers | jq .

# 外部(先在 adminFront → 主机管理 → 外部访问 Token 颁发一个)
curl -H "X-Access-Token: <plain>" -X POST \
     http://<host>:8090/external/containers/test-nginx/restart
```

## 路由概览

| 路径 | 鉴权 | 说明 |
|---|---|---|
| `/health` | 无 | 健康检查 |
| `/admin/containers/*` | adminBackend admin token (Redis db=3) | 列表/inspect/启停/重启/暂停/删除/日志 |
| `/admin/hosts` | 同上 | 查看当前 managed /etc/hosts 块 |
| `/admin/tokens/*` | 同上 | access_token CRUD + 重生 |
| `/external/containers/:id/{start,stop,restart,pause}` | X-Access-Token | 给 CI/CD 启停 |

## 数据存哪

- `/var/lib/docker-host-master/tokens.json` — access_token bcrypt 哈希
- `/var/log/docker-host-master/audit.log` — external 调用一行一记
- `/etc/hosts` 的 `# BEGIN docker-host-master` 到 `# END` 之间 — managed 块

`tokens.json` 备份就是 `cp` 一下,恢复 `cp` 回去,无 DB 复杂性。
