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
| **`REDIS_ADDR`** | (空) | **必填** — 共享 Redis 地址 (例 `172.17.0.1:6379`)。空 → /admin/* 全 503 |
| `REDIS_PASSWORD` | (空) | |
| `REDIS_DB` | 3 | **必须跟 adminBackend.authing.redis.database 对齐** |

启动 log 会 warn 缺什么必填 + 对应路由会怎么 503。

### 硬编码,不开 env(运维无需也无理由改)

- `/etc/hosts` managed 块的 BEGIN/END marker — 写死成 `# BEGIN docker-host-master (DO NOT EDIT)` / `# END docker-host-master`。两个 marker 必须配对,把 env 暴露出来反而容易因运维改一个忘改另一个而导致 hosts 全文乱掉
- HOSTS 全量 reconcile 间隔 — 5 分钟(`300s`)。这是事件丢失的兜底机制,不需要让外面改
- access_token JSON 文件路径 — `/var/lib/docker-host-master/tokens.json`,跟 Dockerfile 里的 named volume mount path 强绑定。改了 → 写到容器内非 mount 目录 → 容器删了 token 全丢
- audit log 路径 — `/var/log/docker-host-master/audit.log`,同时写 stdout(`docker logs` 永远拿得到)。需要长期保留 file 就再挂个 named volume(见下文 audit log 章节),不通过 env 改路径

## 部署

**生产标准做法:tag push → GitHub Actions → Portainer 多 endpoint 自动 redeploy**(见下文)。
首次部署 / 本地测试再用 `docker run`。

### 方式一:GitHub Actions + Portainer(推荐,生产用)

`.github/workflows/deploy.yml` 在 push tag 时:
1. build + push image 到 Harbor
2. cosign 签名
3. 对 `PORTAINER_ENDPOINT_IDS` 列表里**每个 endpoint**(每个 endpoint 对应一台 host)循环执行:
   pull → 备份老容器 → create+start 新容器 → 健康检查 → 失败 per-endpoint rollback
4. 单个 host 失败不阻塞其它 host;最终任一失败 → job FAILED

**GitHub repo vars(非 secret):**

| var | 说明 | 示例 |
|---|---|---|
| `HARBOR_URL` | Harbor registry 地址 | `harbor.example.com` |
| `IMAGE_NAME` | image 名 | `docker-host-master` |
| `PORTAINER_URL` | Portainer API 地址 | `https://portainer.example.com/api` |
| **`PORTAINER_ENDPOINT_IDS`** | **逗号分隔的 endpoint id 列表 — 每个对应一台 host** | `1,2,3` |
| `CONTAINER_NAME` | 容器名(可选,默认 `docker-host-master`) | `docker-host-master` |
| `REDIS_ADDR` | Redis 地址(走 docker DNS) | `redis:6379` |
| `REDIS_DB` | Redis DB(默认 3,对齐 adminBackend.authing.redis.database) | `3` |

**GitHub repo secrets:**

| secret | 说明 |
|---|---|
| `HARBOR_USERNAME` / `HARBOR_PASSWORD` | Harbor 推送凭据 |
| `PORTAINER_ACCESS_TOKEN` | Portainer API key |
| `X_REGISTRY_AUTH` | Portainer 拉镜像用的 base64 编码 registry auth |
| `COSIGN_PASSWORD` / `COSIGN_PRIVATE_KEY` | cosign 签名 |
| `REDIS_PASSWORD` | Redis 密码 |

**Portainer 容器创建参数**(写死在 workflow 里,你不用改,但要知道在做什么):

- `NetworkMode: janyee_net` + `EndpointsConfig: { janyee_net: {} }` — **只指定网络,不指定 IP**,docker IPAM 自动分配
- `Binds: [docker.sock, /etc/hosts, docker-host-master-data:/var/lib/docker-host-master]` — sock + hosts bind mount,内部状态走 named volume
- `CapAdd: ["DAC_OVERRIDE"]` — 写 /etc/hosts 需要
- `PortBindings: 8090:8090` + `RestartPolicy: unless-stopped`

加 / 摘 host:改 `PORTAINER_ENDPOINT_IDS` 这个 var,下一次 tag push 自动生效。

### 方式二:手动 `docker run`(首次 bootstrap / 本地测试)

```bash
# 一次性建 named volume (docker 自己管,不污染宿主文件系统)
docker volume create docker-host-master-data

# 确保宿主上有 janyee_net 用户定义 bridge 网络
# (其他业务服务 blog / userLogin / adminBackend 都跑在上面)
# 没有就先建一个: docker network create janyee_net

docker run -d --name docker-host-master \
  --network janyee_net \
  -v /var/run/docker.sock:/var/run/docker.sock:rw \
  -v /etc/hosts:/etc/hosts:rw \
  -v docker-host-master-data:/var/lib/docker-host-master \
  --cap-add DAC_OVERRIDE \
  -p 8090:8090 \
  -e REDIS_ADDR=redis:6379 \
  -e REDIS_PASSWORD='your-redis-password' \
  -e REDIS_DB=3 \
  --restart unless-stopped \
  harbor.url/docker-host-master:vXXXX
```

**关键 mount 讲清楚**:

| mount / flag | 类型 | 为啥 |
|---|---|---|
| `--network janyee_net` | user-defined bridge | **只指定网络,不指定 IP**(没有 `--ip` flag)— docker 从 janyee_net 的 IPAM 池自动分配。这样多主机/多实例不会撞地址,也方便 docker DNS 用容器名互相 resolve(`redis` / `adminBackend` 这种短名直接通) |
| `/var/run/docker.sock` | host bind | 容器内调宿主 docker daemon,必须 bind |
| `/etc/hosts` | host bind | 自动维护宿主 hosts 这是核心功能,必须 bind |
| `docker-host-master-data` | **named volume** | access_token JSON 文件持久化。**不用 bind mount** — 这是服务内部状态,不应该出现在宿主目录里 |
| `--cap-add DAC_OVERRIDE` | cap | 写 /etc/hosts 需要(避免 `--privileged`) |

> **不要**用 `-v /var/lib/docker-host-master:/var/lib/docker-host-master`。bind mount 等于把服务的内部状态(包含 bcrypt 哈希的 token 文件)暴露到宿主目录,admin SSH 上去能 ls 到,reduce attack surface 反向操作。named volume 由 docker 管,落在 `/var/lib/docker/volumes/docker-host-master-data/_data/` 但运维通常不直接进去。

> **不要**用 `--ip 172.21.0.x` 把 IP 写死。撞地址麻烦不说,换 host 还要逐台改命令。让 docker 自己分配,然后用容器名(janyee_net 上 docker DNS 自动注册)互相 resolve 就够。

### REDIS_ADDR 怎么填?

| Redis 部署位置 | 该填什么 |
|---|---|
| Redis 是 janyee_net 上的容器,名字 `redis` | `redis:6379`(走 docker DNS,**推荐**) |
| Redis 跑在宿主上(非容器) | `host.docker.internal:6379`(macOS/Win 默认通,Linux 加 `--add-host host.docker.internal:host-gateway`) |
| Redis 在另一台机器 | `<那台机器的 IP>:6379` |

原 README 里写 `172.17.0.1:6379` 是 docker0 bridge gateway,只在容器跑在**默认 bridge** 网络时才管用。一旦你 `--network janyee_net`,容器看不到 docker0,这个地址就不通了 — 必须改成上表里对应方案。

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
