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

## 默认头像

新用户注册成功后，API 会异步向 `https://api.dicebear.com/10.x/micah/png` 取一张
**随机 seed** 的 256px PNG，存入项目自己的对象存储（`avatars/{user_id}/{ts}.png`），
再把 key 写进 `users.avatar_key`。之后与用户自己上传的头像走完全相同的读取路径
（`settings.avatarURL` → `store.MediaURL`），页面渲染不再依赖 dicebear.com。

- 实现：`internal/modules/auth/default_avatar.go`，由 `cmd/api/main.go` 在 S3 可用时注入；
  S3 不可用或 DiceBear 超时/失败时只记 WARN，注册照常成功，前端回落占位头像。
- 手动选择：设置页「随机头像」与后台「用户管理 → 头像」都提供 **6 张随机候选**，可「换一批」再挑。
  预览走同源代理 `GET /api/v1/avatar/preview?seed=…`（前端 CSP 不允许外链 `<img>`，
  且只接受 24 位 hex seed）；选定后由 `POST /api/v1/me/avatar/random` 或
  `POST /api/v1/admin/users/{id}/avatar` 下载并落库。共用
  `internal/platform/avatar` 的下载校验与 `settings.Handler.SetRandomAvatar` 的写入实现。
- 异步且幂等：写库条件是 `avatar_key IS NULL`，不会覆盖用户在异步任务完成前自己上传的头像；
  写库失败会删掉刚上传的对象，不留孤儿。
- 只作用于**新注册用户**；存量用户与 seed 数据保持原状。

**署名**：Micah 是 [Micah Lanier《Avatar Illustration System》](https://www.figma.com/community/file/829741575478342595)
的 remix，按 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/) 授权。
风格与 API 见 [DiceBear Micah](https://www.dicebear.com/styles/micah/)。
设置页的头像区域也展示了同样的署名。

## 评论表情

评论输入框的表情按钮打开 **Noto 3D** 表情面板（8 个分类、225 个常用表情），选中后插入到光标处；
评论正文里的表情渲染成同源 PNG，清单外的表情回落系统字体。

- **为什么自托管**：前端 CSP 是 `img-src 'self' data: blob:`，外链图片会被拦；
  自托管也不依赖第三方 CDN 的可用性。
- **素材是字体不是图片**：`参考资料/Noto-3D-Emoji-Fonts/` 给的是 `Noto-3D-128*.ttf`，
  图形以 **sbix**（Apple 位图字体表）内嵌 PNG 的形式存在。`scripts/sync-emoji3d.mjs`
  直接解析 TTF（cmap 码点 → 字形号、sbix 字形 → 内嵌 PNG），零依赖导出，不引入图形库。
- **只导出常用表情**：字体里有 1400+ 个表情，整套搬进来既没必要也拖慢面板；
  导出范围完全由清单决定。清单唯一事实来源是 `apps/web/app/data/emoji.json`，加表情只需
  往分类里加字符，再跑 `npm run emoji:sync`（`--check` 只校验，`--ppem=82` 换一档尺寸）。
- **文件名规则**：code point 十六进制序列命名，变体选择符 `FE0F` 省略
  （`❤️` → `2764.png`）；`emojiSrc()` 做同样的归一化，让 `❤` 与 `❤️` 命中同一张图。
- **不支持组合表情**：这套字体没有 `😮‍💨`、`❤️‍🔥` 这类 ZWJ 组合的字形（GSUB 里也没有对应连字），
  强行塞进去只会得到第一段的图。所以清单里不放组合表情，它们回落到系统字体。
- **插入而不是替换**：按光标位置插入并恢复光标，输入框 `maxlength=200` 由代码兜底。
- `@` 提及仍是占位入口（`disabled`），功能未实现。

**署名**：图形来自 Google [Noto Emoji](https://github.com/googlefonts/noto-emoji) 的 3D 字型
（`Noto-3D-Emoji-Fonts`），按 [SIL OFL 1.1](https://scripts.sil.org/OFL) 授权；
许可证随资源放在 `apps/web/public/emoji3d/LICENSE`。

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
