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
# 一次性建 named volume (docker 自己管,不污染宿主文件系统)
docker volume create docker-host-master-data

docker run -d --name docker-host-master \
  -v /var/run/docker.sock:/var/run/docker.sock:rw \
  -v /etc/hosts:/etc/hosts:rw \
  -v docker-host-master-data:/var/lib/docker-host-master \
  --cap-add DAC_OVERRIDE \
  -p 8090:8090 \
  -e REDIS_ADDR=172.17.0.1:6379 \
  -e REDIS_PASSWORD='your-redis-password' \
  -e REDIS_DB=3 \
  --restart unless-stopped \
  harbor.url/docker-host-master:vXXXX
```

**关键 mount 讲清楚**:

| mount | 类型 | 为啥 |
|---|---|---|
| `/var/run/docker.sock` | host bind | 容器内调宿主 docker daemon,必须 bind |
| `/etc/hosts` | host bind | 自动维护宿主 hosts 这是核心功能,必须 bind |
| `docker-host-master-data` | **named volume** | access_token JSON 文件持久化。**不用 bind mount** — 这是服务内部状态,不应该出现在宿主目录里 |
| `--cap-add DAC_OVERRIDE` | cap | 写 /etc/hosts 需要(避免 `--privileged`) |

> **不要**用 `-v /var/lib/docker-host-master:/var/lib/docker-host-master`。bind mount 等于把服务的内部状态(包含 bcrypt 哈希的 token 文件)暴露到宿主目录,admin SSH 上去能 ls 到,reduce attack surface 反向操作。named volume 由 docker 管,落在 `/var/lib/docker/volumes/docker-host-master-data/_data/` 但运维通常不直接进去。

### audit log 怎么办?

默认在容器内的 `/var/log/docker-host-master/audit.log`,容器删了就丢。同时**也写 stdout**,
所以 `docker logs docker-host-master` 永远拿得到一份。需要 file 长期保留就加另一个 named volume:

```bash
-v docker-host-master-logs:/var/log/docker-host-master
```

或者直接接 ops 现有的 docker logs 收集链路(filebeat / fluent-bit / loki) — **推荐这条**,跟其他服务统一日志栈。

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

| 数据 | 存哪 | 是否暴露宿主 |
|---|---|---|
| access_token (bcrypt 哈希) | named volume `docker-host-master-data` → 容器内 `/var/lib/docker-host-master/tokens.json` | ❌ 仅 docker 管 |
| 审计 log | 容器内 `/var/log/docker-host-master/audit.log` + stdout(docker logs)| 默认不;按需可加 volume |
| /etc/hosts managed 条目 | 宿主 `/etc/hosts` (`# BEGIN docker-host-master` 块)| ✅ 这是核心功能 |

### 备份 / 恢复 tokens

```bash
# 备份
docker run --rm \
  -v docker-host-master-data:/data:ro \
  -v "$PWD":/backup \
  alpine cp /data/tokens.json /backup/tokens-$(date +%F).json

# 恢复
docker stop docker-host-master
docker run --rm \
  -v docker-host-master-data:/data \
  -v "$PWD":/backup \
  alpine cp /backup/tokens-2026-05-22.json /data/tokens.json
docker start docker-host-master
```

或者直接 `docker volume inspect docker-host-master-data` 找到底层路径手动 cp(运维场景不推荐,因为版本/路径可能变)。
