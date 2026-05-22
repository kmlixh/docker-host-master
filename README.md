# docker-host-master

host-level Docker 容器管理 + 自动 `/etc/hosts` 维护 + 受 access_token 保护的外部 API。

每台物理机部署一个实例,管本机的 docker。

## 功能(分阶段)

| Phase | 状态 | 内容 |
|---|---|---|
| A | ✅ | 服务 scaffold (Consul 注册 + Fiber + /health) |
| B | ⏳ | Docker 事件 daemon + /etc/hosts 自动维护 |
| C | ⏳ | Admin API (authing 保护) + access_token 颁发 |
| D | ⏳ | External API (access_token 保护) + 审计 log |
| E | ⏳ | adminFront 管理页 + 菜单 |

## 部署(每台主机一次)

```bash
docker run -d --name docker-host-master \
  -v /var/run/docker.sock:/var/run/docker.sock:rw \
  -v /etc/hosts:/etc/hosts:rw \
  --cap-add DAC_OVERRIDE \
  -p 8090:8090 \
  -e CONSUL_ADDRESS=consul.janyee.com:8500 \
  -e CONSUL_TOKEN=<token> \
  -e CONSUL_CONFIG_PATH=docker_host_master/config \
  --restart unless-stopped \
  harbor.url/docker-host-master:vXXXX
```

**关键**:
- `/var/run/docker.sock` bind mount → 容器内能调 host 的 docker daemon
- `/etc/hosts` bind mount + `--cap-add DAC_OVERRIDE` → 容器能写宿主 hosts 文件
- 用 `--network bridge` 即可(不必 host network),只要 socket 通就行

## 配置(Consul KV)

`application.yml` 模板见仓内同名文件,部署前上传到 consul KV 路径 `docker_host_master/config`(或者按主机分:`docker_host_master/<hostname>/config`)。

## 验证

```bash
# 服务自检
curl http://localhost:8090/health

# 应返
# {"status":"ok","service":"docker-host-master","hostname":"<your-host>"}
```

后续 Phase 接通后会有 `/admin/containers`、`/external/containers/*/start` 等。

## 安全

- `/admin/*` 走 authing JWKS 验签的 admin token(从 adminBackend 登录拿)
- `/external/*` 走数据库管理的 access_token(类似 OAuth client_secret,bcrypt 哈希存,只展示一次)
- 审计 log 落 `/var/log/docker-host-master/audit.log`
