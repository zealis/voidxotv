# 夜街 · 部署手册

单机 Docker Compose 起步架构（开发技术规范 §2 模块化单体）：**Go API + Worker + Nuxt 前端 + PostgreSQL + Redis + 自建 SMTP**，媒体走对象存储 / CDN 直发（应用不做媒体中继，§2.1 定案）。

## 1. 前置条件

| 依赖 | 说明 |
|---|---|
| Docker 26+ / Compose v2 | 运行编排 |
| Go 1.27+（构建机） | `go build ./cmd/api ./cmd/worker`；或直接用 CI 产物 |
| Node 22+（构建机） | `cd apps/web && npm ci && npm run build`，产物 `apps/web/.output/` |
| 对象存储 | 生产为 Cloudflare R2 / Backblaze B2 私人桶；必须允许浏览器跨域 PUT 并暴露 `ETag`（分片直传） |
| FFmpeg | Worker 镜像内置或宿主机挂载 `.tools/ffmpeg` |
| SMTP | 自建（§11.7）。`YJ_SMTP_HOST` 留空时邮件进死信、找回密码不可用 |

## 2. 构建

```bash
# 后端二进制（Linux 目标机）
CGO_ENABLED=0 GOOS=linux go build -o bin/yejie-api ./cmd/api
CGO_ENABLED=0 GOOS=linux go build -o bin/yejie-worker ./cmd/worker

# 前端
cd apps/web && npm ci && npm run build
```

镜像化参考（可在 CI 中完成，产物推到内部 registry）：

```dockerfile
# api.Dockerfile / worker.Dockerfile 骨架
FROM golang:1.27 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -o /out/yejie-api ./cmd/api   # worker 同理

FROM gcr.io/distroless/static-debian12
# worker 另需 ffmpeg：FROM jrottenberg/ffmpeg 或 apt 安装后多阶段拷贝
COPY --from=build /out/yejie-api /app/yejie-api
ENTRYPOINT ["/app/yejie-api"]
```

```dockerfile
# web.Dockerfile 骨架
FROM node:22 AS build
WORKDIR /src/apps/web
COPY apps/web/package*.json ./
RUN npm ci
COPY apps/web/ ./
RUN npm run build

FROM node:22-slim
WORKDIR /app
COPY --from=build /src/apps/web/.output ./
EXPOSE 3000
CMD ["node", ".output/server/index.mjs"]
```

## 3. 配置

1. `cp .env.example deployments/production/.env`，逐项替换：
   - `YJ_DATABASE_URL` / `REDIS_PASSWORD`（强口令）
   - `YJ_S3_*`（生产桶 + CDN 域名 `YJ_S3_PUBLIC_ENDPOINT`；`YJ_PUBLIC_MEDIA=true` 才会启用 3600s 详情缓存）
   - `YJ_SITE_URL` / `NUXT_PUBLIC_SITE_URL`（对外域名，二者一致）
   - `YJ_DELIVERY_TOKEN`（必须替换开发默认值，启动时 fail-fast 校验）
   - `YJ_ADMIN_BOOTSTRAP_PASSWORD`（强口令，仅用于首次种子账号）
   - `YJ_SMTP_*`（找回密码 / 邮件通知依赖）
   - `YJ_COOKIE_SECURE=true`（HTTPS 必开）
2. 生产 fail-fast 项：`YJ_ENV=production` 时代码会校验默认口令/令牌必须替换（config.go）。

## 4. 上线步骤

```bash
cd deployments/production
docker compose --env-file .env up -d postgres redis
docker run --rm --network yejie_default \
  -e YJ_DATABASE_URL='postgres://<user>:<pass>@postgres:5432/<db>?sslmode=disable' \
  <api-image> migrate   # 或宿主机: YJ_DATABASE_URL=... go run ./cmd/migrate -direction up
docker compose --env-file .env up -d
curl -fsS http://127.0.0.1:18080/health/ready   # {"status":"ok"}
curl -fsS http://127.0.0.1:18080/health/live
```

- 迁移**可重复执行**（golang-migrate，CI 会连跑两遍验证），升级 = 换镜像后再跑一次 migrate。
- 反向代理：80/443 → `web:3000`（页面）与 `api:18080`（`/api/**`）。访客 IP 默认
  只读 TCP 对端（不信任任何代理头）；部署在反代后面时，`YJ_TRUSTED_PROXIES` 填
  代理网段（如 `127.0.0.1/32` 或内网段），API 才会采信 `X-Forwarded-For` 的
  **最右一跳非受信地址**。反代务必用 `$proxy_add_x_forwarded_for` 追加、不要直接
  透传客户端发来的头；`X-Real-IP` 不参与判断（§11.6）。
- **暴露面收敛**：对外只开 80 / 443；`api` 只绑 `127.0.0.1:18080`，
  Postgres / Redis / S3 **不要加 `ports:`**（只留在 compose 内部网络）。
  反代示例见 `deployments/production/nginx.conf.example`：deny 一切以点开头的路径
  （`.git` / `.env` / `.workbuddy`）、`compose.yaml`、`go.mod`、`migrations`、
  `deployments`、`scripts`、`bin`、`data`、`设计文档`、`参考资料`；
  `/health/` 与 `/metrics` 只允许 `127.0.0.1`（应用层也会拒绝带代理头的请求）；
  后台 `/admin` 建议再按来源 IP 收紧。Nuxt 侧有同名中间件做纵深防御，
  `robots.txt` 同时禁止抓取后台与接口。镜像构建请使用仓库根的 `.dockerignore`，
  不要把 `.git` / `.env` / `data` 打进镜像。

## 5. 运维要点

| 事项 | 口径 |
|---|---|
| 备份 | PostgreSQL 定时 `pg_dump`（事实来源）；Redis 可丢失（会话/缓存/限流），S3 桶按云商策略 |
| 后台账号 | `go run ./cmd/seed` 幂等创建；生产必须改掉种子口令并**启用 TOTP 二次验证**（设置页 → 安全与参数） |
| Outbox | `outbox_events` 失败事件在后台「设置 → Outbox」可见、可重放；邮件死信带原因 |
| 定时发布 | Worker 内置调度器每 30s 扫描 `scheduled`；确保 worker 常驻 |
| 权重调参 | 热门榜 `play/like` 权重在后台设置页可改，下一次 5 分钟聚合生效 |
| 扩容 | 数据库为主瓶颈时先读写分离/升配；搜索压力上来时按 §9.2 的统一接口替换点切全文检索/独立引擎 |
| 日志 | 结构化 JSON 到 stdout，交给容器日志采集；生产关闭详细调试输出（§11.6） |

## 6. 验收清单（上线前）

- [ ] `go test ./...`、`go vet ./...`、`gofmt -l` 全绿；`npx nuxi typecheck` 通过
- [ ] `docker compose up` 后 `/health/ready` 200，14 个页面（首页/搜索/话题/个人库/设置/后台…）返回 200
- [ ] 迁移连跑两遍 `version=N dirty=false`
- [ ] 首页首屏 JS 体积与 Lighthouse 分数达标（前端工程架构规范性能预算）
- [ ] 举报→处置→通知、评论→回复→@提及通知、定时发布、找回密码（SMTP 配置后）四条链路冒烟通过
