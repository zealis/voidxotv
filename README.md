# 夜街 · 短视频/图集社区

单 Go API + Worker + Nuxt 前端的模块化单体（开发技术规范 §1.5），媒体经对象存储 / CDN 直发，PostgreSQL 是唯一事实来源。

## 代码结构

```
cmd/api        Go API 进程（模块在 internal/modules/* 注册到 internal/app/router.go）
cmd/worker     转码 / 交付（Outbox）/ 统计聚合 / 远程导入 / 定时发布 / 邮件投递
cmd/migrate    golang-migrate 迁移（migrations/ 内嵌，可重复执行）
cmd/seed       开发/运维种子账号（生产必须改口令）
apps/web       Nuxt 4 前端（app/pages 沉浸流与工具页，components/base Vp* 组件库）
deployments    生产 compose 与部署手册（DEPLOY.md）
```

> 设计规范与设计图（UI / 技术规范 V1.0.2）保留在维护者本地，不随仓库分发。

## 本地开发

```bash
node scripts/dev.mjs up --watch    # 推荐：容器 → 迁移 → API / Worker / Nuxt，并监听 Go 源码自动重启
```

拆开手动跑也可以：

```bash
docker compose up -d              # Postgres(5432) / Redis(6380) / S3 测试桶(9100)
go run ./cmd/migrate -direction up
go run ./cmd/seed                 # 可选：演示账号与运营数据
scripts/dev.sh                    # 一键起 API + Worker + Nuxt dev（Linux / WSL，前台阻塞）
```

`scripts/dev.mjs` 的其他用法（Windows / macOS / Linux 通用）：

```bash
node scripts/dev.mjs up            # 起全套，已在跑的服务自动跳过；前台守护，Ctrl+C 收本次启动的进程
node scripts/dev.mjs up --detach   # 只启动不守护，适合 CI
node scripts/dev.mjs status        # 健康检查 + API 版本
node scripts/dev.mjs restart api   # 改完后端：重建并重启（api / worker / web / all）
node scripts/dev.mjs stop all      # 停止
```

- API：`http://127.0.0.1:18080`（`/health/ready`）
- 前端：`http://127.0.0.1:3000`
- 环境变量样例见 `.env.example`；Go 进程不读 .env 文件，用 shell / compose 注入。
- 进程日志在 `data/logs/{api,worker,web}.log`，pid 文件在 `data/pids/`。

## 版本与更新日志

- 版本号的事实来源是根目录 [`VERSION`](VERSION)，变更记录见 [`CHANGELOG.md`](CHANGELOG.md)。
- **改完代码必须做两件事**：在 `CHANGELOG.md` 的 `[未发布]` 里按「新增 / 修复 / 改进 / 重构 / 安全维护」
  写条目，再跑一次版本提升。
- `node scripts/bump-version.mjs patch|minor|major|X.Y.Z`：同步 `VERSION`、两个 `package.json`、
  Go 的 `internal/platform/buildinfo/buildinfo.go`，并把 `[未发布]` 的正文提升为带日期的正式版本条目。
- `node scripts/bump-version.mjs --check`（或 `npm run version:check`）：校验四处版本号一致，可接进 CI。
- 线上版本：`curl /health/live` 的 `version` 字段与 API / Worker 启动日志，前端显示在「设置」页底部。

## 验收口径

- 后端：`gofmt -l`（应为空）→ `go vet ./...` → `go test ./...`（通知/内容/搜索等含 PostgreSQL 集成测试，需 `YJ_DATABASE_URL`）→ `go build ./...`
- 前端：`cd apps/web && npx nuxi typecheck`
- 数据库：迁移连跑两遍 `version=N dirty=false`
- 版本：`npm run version:check`（四处版本号一致）
- 部署：见 [deployments/DEPLOY.md](deployments/DEPLOY.md)
