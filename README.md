# docker-host-master

host-level Docker 容器管理 + 自动 `/etc/hosts` 维护 + 受 access_token 保护的外部 API。

每台物理机部署一个实例,管本机的 docker。

## 三大功能

1. **自动 /etc/hosts** —— 监听 docker events,容器起来自动加 `<IP> <name> <hostname>`,容器死了删
2. **adminFront 管理页** —— 容器列表 + 启停 + 日志 + access_token 颁发
3. **外部 REST API** —— `/external/*`,受 access_token 保护,给 CI/CD 启停容器用

## 配置(纯 env 变量,无 Consul/无配置文件)

| env | 默认 | 说明 |
|---|---|---|
| `SERVER_PORT` | 8090 | HTTP 监听端口 |
| `SERVICE_NAME` | docker-host-master | 自身名字(只用于日志) |
| `DOCKER_ENDPOINT` | `unix:///var/run/docker.sock` | docker daemon 连接,bind mount socket 后基本不用改 |
| `DOCKER_TIMEOUT_SEC` | 30 | docker API 调用超时 |
| `HOSTS_FILE` | `/etc/hosts` | 容器内 bind mount 进来的宿主 hosts |
| `HOSTS_BEGIN_MARKER` | `# BEGIN docker-host-master (DO NOT EDIT)` | managed 块起始标记 |
| `HOSTS_END_MARKER` | `# END docker-host-master` | managed 块结束标记 |
| `HOSTS_RECONCILE_INTERVAL_SEC` | 300 | 5min 全量 reconcile 兜底丢事件 |
| `DB_HOST` | 172.17.0.1 | postgres 主机(docker bridge gateway = 宿主) |
| `DB_PORT` | 5432 | postgres 端口 |
| `DB_USER` | postgres | |
| **`DB_PASSWORD`** | (空) | **必填** — 空 → token store 跳过 → /admin/tokens + /external/* 503 |
| `DB_NAME` | docker_host_master | |
| `DB_SSLMODE` | disable | |
| **`OAUTH_ISSUER`** | (空) | **必填** — authing JWKS URL,例如 `https://auth.janyee.com`。空 → /admin/* 全部拒绝 |
| `AUDIT_LOG` | `/var/log/docker-host-master/audit.log` | 外部调用审计 log 文件路径 |

启动时 log 会打 warn 提示哪些必填空了 + 哪些路由会因此不可用。`/health` 永远可用。

## 部署

### 1. 初始化 token store DB(每个共享 DB 实例一次)

```bash
psql -h <pg-host> -U postgres -c "CREATE DATABASE docker_host_master;"
psql -h <pg-host> -U postgres -d docker_host_master \
     -f migrations/001_access_tokens.sql
# 或者首次启动让 GORM AutoMigrate 自动建(默认开)
```

### 2. 每台主机 docker run

```bash
docker run -d --name docker-host-master \
  -v /var/run/docker.sock:/var/run/docker.sock:rw \
  -v /etc/hosts:/etc/hosts:rw \
  -v /var/log/docker-host-master:/var/log/docker-host-master:rw \
  --cap-add DAC_OVERRIDE \
  -p 8090:8090 \
  -e DB_HOST=172.17.0.1 \
  -e DB_PASSWORD='your-pg-password' \
  -e OAUTH_ISSUER=https://auth.janyee.com \
  --restart unless-stopped \
  harbor.url/docker-host-master:vXXXX
```

**关键 mount**:
- `/var/run/docker.sock` → 容器内能调宿主 docker daemon
- `/etc/hosts` → 自动维护宿主 hosts
- `--cap-add DAC_OVERRIDE` → 写 /etc/hosts 需要(避免 `--privileged`)

## 验证

```bash
# /health
curl http://<host>:8090/health
# → {"status":"ok","service":"docker-host-master","hostname":"X","docker_api":"1.x"}

# /etc/hosts daemon
docker run -d --name test-nginx nginx:alpine
sleep 2
cat /etc/hosts | grep test-nginx
# → 172.x.x.x  test-nginx <hostname>  # container_id=...

# admin token (需要 ADMIN token from adminBackend)
curl -H "Token: $ADMIN_TOKEN" http://<host>:8090/admin/containers | jq .

# external (需要先 adminFront 颁发 access token)
curl -H "X-Access-Token: <plain>" -X POST \
     http://<host>:8090/external/containers/test-nginx/restart
```

## 路由概览

| 路径 | 鉴权 | 说明 |
|---|---|---|
| `/health` | 无 | 健康检查 |
| `/admin/containers/*` | authing JWT (Token 头 + tenant=admin) | 列表/inspect/启停/重启/暂停/删除/日志 |
| `/admin/hosts` | 同上 | 查看当前 managed /etc/hosts 块 |
| `/admin/tokens/*` | 同上 | access_token CRUD + 重生 |
| `/external/containers/:id/{start,stop,restart,pause}` | X-Access-Token (bcrypt 比对) | 给 CI/CD 启停 |

## 安全

- `/admin/*`:authing JWKS 验签 admin token,仅 tenant_id == "admin" 通过
- `/external/*`:DB 里存 bcrypt(plain),plain 只在创建时返一次。外部调用走 bcrypt 比对(N 通常 < 10,~5ms × N 可接受)
- 审计日志:`/external/*` 每次调用记 `[ts] token="<name>" action=<x> container=<y> result=ok/err`
