# 更新日志

夜街（voidxotv）的版本与变更记录。所有面向用户的改动、修复与维护动作都记在这里，
排障时用设置页底部的版本号 / `GET /health/live` 的 `version` 字段对照本文档。

## 版本规则

语义化版本 `主.次.修订`，当前处于 `0.x` 预发布阶段：

| 改动性质 | 升哪一位 | 说明 |
| --- | --- | --- |
| 破坏性变更（API 契约、数据结构、配置项不兼容） | **次**（0.x → 0.(x+1).0） | 条目里必须标「不兼容」，并写迁移步骤 |
| 新增功能 / 新页面 / 新接口 | **次** | 0.4.0 → 0.5.0 |
| 修复缺陷 | **修订** | 0.5.0 → 0.5.1 |
| 改进（体验、性能、样式与文案微调） | **修订** | 与修复同批发布时不额外升次 |
| 重构（不改变外部行为） | **修订**；跨模块大重构可升次 | 条目要说明「行为不变」 |
| 安全维护（依赖升级、权限收紧、加固） | **修订**；若对外行为变化则升次 | 涉及凭据/权限的条目要写清影响面 |

## 条目分类

每条改动只记在最贴近的分类里，写清「哪个页面 / 接口 / 文件」+「变成什么」，不写「优化体验」这类空话。

- **新增**：用户可感知的新能力、新页面、新接口、新组件。
- **修复**：缺陷修复。要写现象 + 根因 + 改法，方便以后回查同类问题。
- **改进**：不修缺陷但让体验更好的调整（布局、文案、性能、可访问性、开发脚本与验证工具）。
- **重构**：结构调整，外部行为不变（拆模块、换实现、统一令牌）。
- **安全维护**：认证授权、输入校验、依赖与镜像升级、密钥与权限、CSP/CORS 等加固。

## 发布流程

1. 改完代码，在下面 `## [未发布]` 的对应分类里加条目。
2. 跑 `node scripts/bump-version.mjs patch|minor|major`：它会同时更新 `VERSION`、两个 `package.json`、
   `internal/platform/buildinfo/buildinfo.go`，并把 `[未发布]` 的正文提升为带日期的正式版本条目。
3. 过门禁：`gofmt -l ./cmd ./internal`（应为空）→ `go vet ./...` → `go test ./...` → `go build ./...`；
   前端 `cd apps/web && npx nuxi typecheck`。
4. 确认 `GET /health/live` 的 `version` 与设置页底部版本一致。

## [未发布]

<!-- 下次改动写在这里；发布时由 bump-version.mjs 提升为正式版本条目。 -->

## [0.31.4] - 2026-11-15

### 新增

- **N+1 静态检查**：`internal/tools/checkn1` 用 Go AST 扫描 `for` / `range`
  循环体内直接调用 `db` / `tx` / `pool` 的 `Query` / `QueryRow` / `Exec`；
  挂到 `npm run check:n-plus-one`。确实必须逐条执行时，在调用行或上一行写
  `// n+1-allow: 原因` 豁免。
- **`PERFORMANCE.md` 数据库访问规范**：列表 / 批量接口的 SQL 条数必须与数据量
  无关；批量读用 `ANY($1)` / JOIN，批量写用 `unnest` /
  `WITH ORDINALITY` / `ON CONFLICT`；循环只做内存映射与参数拼接。

### 重构

- **去掉多处循环内查库（N+1）**：
  - `content.SetContentTopics`：话题状态一次 `ANY($1)` 查回、内存校验；
    关联一次 `INSERT ... SELECT unnest($2::bigint[])`。
  - 图集排序：一条 `UPDATE ... FROM unnest($2::bigint[]) WITH ORDINALITY`
    按序号重排，SQL 次数与图片张数无关。
  - `gifs.SaveConfig` 与 `spam.savePairs`：11 个配置键一次 `unnest` 批量
    upsert，不再逐键 `INSERT ... ON CONFLICT`。
  - `delivery.Enqueue`：页面版本一次批量 upsert、任务一次批量插入，并包事务；
    同一批重复页面先去重。`delivery.PublishEvent` 改用 `enqueuePages`，
    每条事件固定 3 条 SQL（原先 页数 × 3）。
  - `content.scheduler`：定时发布到期内容的 outbox 事件一次批量写入。
  - `stats` 聚合：一次 `ANY($1) ... FOR UPDATE` 锁回计数、一次批量写当天增量、
    一次批量 upsert 累计值；聚合耗时不再随内容量线性增长。

### 改进

- 验证：在真实 PostgreSQL 的事务里逐条执行上述批量语句（含约束与
  `RETURNING`），全部通过并回滚；`m93-batch-sql.js` 8 项全过（GIF 配置批量
  保存后不漂移、重算榜单成功、概览可读）；`checkn1` 扫描归零；
  `gofmt/vet/test/build`、`typecheck`、`version:check` 全过。

## [0.31.3] - 2026-11-15

### 安全维护

- **隐藏文件与源码路径不再对外**：新增 Nuxt 中间件
  `server/middleware/block-sensitive-paths.ts`——以点开头的路径（`.git` /
  `.env` / `.workbuddy` / `.nuxt` 等）、`compose.yaml`、`go.mod` 与锁文件、
  `migrations`、`deployments`、`scripts`、`bin`、`data`、`tests`、`设计文档`、
  `参考资料` 一律返回 404（`/.well-known/` 放行，避免 ACME 续期失败）。
  同时新增 `apps/web/public/robots.txt`，禁止抓取 `/admin`、`/api/`、`/studio`、
  `/settings`、`/messages`、`/library`、`/me`。
- **健康检查与指标只给本机**：`/health/live`、`/health/ready`、`/metrics`
  统一包 `internalOnly`——只接受 loopback 直连且不带 `X-Forwarded-For` /
  `X-Real-IP` / `Forwarded` 的请求，其余一律 404。容器健康检查
  （`wget 127.0.0.1:18080/health/ready`）不受影响。
- **部署侧收敛**：新增 `deployments/production/nginx.conf.example`（隐藏文件 /
  源码 / 配置 deny、`/health` 与 `/metrics` 仅 `127.0.0.1`、`/admin` 可选 IP
  白名单、拒绝 TRACE）；新增 `.dockerignore`，避免 `.git` / `.env` / `data`
  被打进镜像；`deployments/DEPLOY.md` 补「暴露面收敛」：对外只开 80/443，
  Postgres / Redis / S3 不加 `ports`。

### 修复

- **`/health/ready` 泄露依赖明细**：原本返回
  `{"postgres":"ok","redis":"ok"}`，现在成功时只回 `{"status":"ok"}`，
  失败仍是 503 `not_ready`，不区分哪个依赖挂了。

### 改进

- 验证：本机 `/health/live` 200（含版本）、`/health/ready` 200
  `{"status":"ok"}`；三者带 `X-Forwarded-For` 时均 404。页面 `/.env`、
  `/.git/config`、`/compose.yaml`、`/go.mod`、`/migrations/...`、
  `/.workbuddy/...` 全部 404；`/robots.txt` 200 且禁止后台与接口；首页 200。
  Go 单测新增「健康接口对 loopback 开放、对外部直连与反代转发返回 404」。

## [0.31.2] - 2026-11-15

### 安全维护

- **访客 IP 默认只读服务器看到的原始地址**：统一到
  `internal/platform/httpx.ClientIP`。默认完全不采信 `X-Forwarded-For` /
  `X-Real-IP`，只读 TCP 对端（`RemoteAddr`），客户端无法通过伪造请求头改变
  限流桶与审计记录。只有直连对端落在 `YJ_TRUSTED_PROXIES` 网段内（即请求确实
  来自自己的反向代理）时，才解析 `X-Forwarded-For` 并取**最右一跳非受信地址**
  —— 代理把真实客户端追加在右侧，左侧可能是客户端伪造的内容，所以绝不取最左。
  `X-Real-IP` 永不读取。
- **修复环境变量名不一致**：代码原本读 `TRUSTED_PROXIES`，而 `.env.example`
  与部署文档写的是 `YJ_TRUSTED_PROXIES`，按文档配置不生效。现以
  `YJ_TRUSTED_PROXIES` 为准并兼容旧名；默认值从 `127.0.0.1/32` 改为**空**，
  不显式配置就绝不读代理头。生产反代需要填代理网段，并在反代用
  `$proxy_add_x_forwarded_for` 追加真实地址。
- **修复 IPv6 取 IP 错误**：前台原本用 `strings.Split(RemoteAddr, ":")[0]`，
  `[2001:db8::1]:443` 会被截成空串，导致 IPv6 访客共用同一个限流桶。三处
  （前台登录 / 后台登录与审计 / 匿名播放去重）统一改用 `httpx.ClientIP`，
  正确处理端口、IPv6 与 zone，并把 IPv4-mapped IPv6 归一成 IPv4。
- 验证：`httpx` 单测 5 组（默认忽略代理头、IPv6 与 zone、受信代理取最右一跳、
  非受信对端忽略头、空 RemoteAddr）；`config` 单测覆盖默认空与
  `YJ_TRUSTED_PROXIES` 解析；端到端带
  `X-Forwarded-For: 8.8.8.8, 203.0.113.9` 登录失败，限流键仍落在 `127.0.0.1`，
  伪造成 8.8.8.8 / 203.0.113.9 均未生效。

## [0.31.1] - 2026-11-15

### 安全维护

- **登录 Cookie 收紧到 `SameSite=Strict`**：前台与后台的登录 / 退出共 4 处
  Cookie 从 `Lax` 改为 `Strict`。确认其余属性不变：`HttpOnly`（JS 读不到）、
  `Path=/`、无 `Domain`（host-only）；生产用 `__Host-` 前缀并强制 `Secure`
  （启动校验会拒绝不带 Secure 的生产配置）。会话令牌只存服务端 Redis，
  浏览器侧无法读取。
- **认证响应不再被缓存**：`httpserver.JSON` 统一加 `Cache-Control: no-store`，
  带 CSRF 令牌的登录 / 会话响应不会被浏览器或中间代理留下。
- **页面安全响应头补齐**：新增 `Cross-Origin-Resource-Policy: same-origin`
  （第三方页面不能跨站读取本站内容）；生产环境再加 `X-Frame-Options: DENY`
  与 `Cross-Origin-Opener-Policy: same-origin`，作为 CSP `frame-ancestors`
  的旧浏览器兜底。
- **退出登录两步确认**：前台创作者工作台的「退出」点一次变成「确认退出」
  （3 秒后自动还原），后台退出保留确认弹窗。退出不要求密码，避免忘记密码时
  退不出去。
- `.tools/headless-test/m91-cookie-security.js`：18 项全过（前台 / 后台 Cookie
  的 HttpOnly / SameSite=Strict / Path=/ / 无 Domain、认证响应 no-store、
  页面安全头、前台两步确认与 3 秒还原、后台确认弹窗）。

### 改进

- `SameSite=Strict` 的取舍：从外部链接首次打开时，文档请求不带 Cookie，SSR
  可能先显示未登录，客户端 XHR 会立刻恢复；后台是 SPA，不受影响。若不能接受
  这个首屏闪烁，可改回 `Lax`（跨站写请求同样不会带 Cookie）。

## [0.31.0] - 2026-11-15

### 新增

- **敏感操作二次确认（step-up）**：删除作品 / 评论 / 图集图片 / 头像、后台内容
  处置（通过 / 驳回 / 下架 / 限流 / 批量通过）、申诉决定、举报处置（含封禁 /
  禁言 / 移除评论）、用户封禁 / 解封 / 认证、话题下线 / 恢复 / 合并 / 推荐置顶、
  分类隐藏 / 上线、后台账号改角色 / 停用 / 重置密码、重算榜单，都要求重新输入
  登录密码（后台启用 TOTP 的账号再加动态验证码），通过后发 5 分钟确认令牌。
  令牌与会话分开存 Redis（`yejie:stepup:<scope>:<id>:<token>`，按用户隔离），
  会话被劫持但不知道密码也删不掉数据；同一次验证内连续处置不用反复输入。
- 新增 `POST /api/v1/auth/step-up` 与 `POST /api/v1/admin/auth/step-up`；
  无令牌返回 403 `step_up_required`，密码错误 401，TOTP 未填 400 `totp_required`。
- 后台「热门作品 TOP」补「重算」按钮（`stats:aggregate` 权限），重算前同样
  要二次确认。

### 改进

- 前台 `useApi` 与后台 `useAdminApi` 统一处理 403 `step_up_required`：自动弹
  「确认身份」、拿令牌后**就地重试一次**原请求；用户取消则按原错误返回，各调用点
  不需要逐个改。
- 退出登录不要求密码（否则忘记密码就退不出去），改成明确的确认弹窗，CSRF 保护
  保持不变。
- `.tools/headless-test/m90-stepup.js`：15 项全过（后台 / 前台无令牌被挡、
  错误密码 401、令牌 300 秒、同令牌复用、点「重算」自动弹窗并重试成功）。

### 安全维护

- 后台审计新增 `auth.stepup`，只记录「是否启用了 TOTP」，不记录密码或验证码。

## [0.30.4] - 2026-11-15

### 改进

- **评论 / 回复输入框改为多行自动加高**：原来是一行 `<input>`，长内容只能左右
  滚动、看不全。改成 `<textarea rows="1">`，按 `scrollHeight` 随内容长高
  （40–120px，约 1–5 行），超过后框内滚动；发送成功 / 失败恢复 / 选 GIF /
  插表情后都会重算高度。
- 圆角从 `999px` 改为 `20px`：单行 40px 高时仍是胶囊，多行加高后不会变成
  大圆弧；外壳改为 `align-items: flex-end`，@ / 表情图标在长高后贴底对齐。
- 回车行为：Enter 发送、Shift+Enter 换行，中文输入法选词时的 Enter 不拦；
  空输入按回车仍然直接返回、不写库。
- `.tools/headless-test/m71-composer-inline.js` 16 项、`m89-comment-limit.js`
  12 项全过（布局 / 焦点环 / 回车空输入 / 表情面板锚点 / 120 字自动加高到
  60px / Shift+Enter 换行 / 140 与 141 的接口边界）。

## [0.30.3] - 2026-11-15

### 改进

- **评论 / 回复上限从 500 字收紧到 140 个可见字符**：标点与单个表情各算 1，
  GIF 整体算 1（用户看到的是一张图），不让 `[[gif:…]]` 长标记吃掉额度。
  后端 `interactions.commentLength` 统一这个口径，超限返回 400。
- 前端输入框去掉原生 `maxlength`（它按 UTF-16 计，一个表情会被算成 2 个字符），
  改为 `clampDraft` 按码位截断：141 个 ASCII 或 141 个表情都会被截到 140；
  选择 GIF 时自动把文字裁到 139，表情按钮插入也按同一预算判断。
- `.tools/headless-test/m89-comment-limit.js`：8 项全过（ASCII 141→140、
  141 个表情→140 且 UTF-16 长度 280、后端 141 拒绝 / 140 通过）；
  Go 单测覆盖中文全角标点、表情、文字 + GIF 的计数。

## [0.30.2] - 2026-11-15

### 改进

- **后台「词库位置」显示文案改为 `Sensitive-lexicon`**：不再露出
  `Vocabulary/微信输入法` 这种内部目录结构，链接本身仍指向仓库目录、
  仍在新标签页打开；`lexicon_source_label` 由服务端下发，前端不硬编码。

## [0.30.1] - 2026-11-15

### 改进

- **后台词库改为「词库位置」并支持更新对比**：`AdminFeatureFlags.vue` 不再显示
  词库文件名，改成 `Vocabulary/微信输入法` 可点击链接（新标签页打开仓库目录）；
  新增「更新」按钮，与「下载词库」共用
  `POST /api/v1/admin/settings/spam/lexicon`。
- `internal/modules/spam`：`SyncLexicon` 先读 GitHub contents 的 blob sha，与本地
  `spam.lexicon_remote_sha` 比对，相同直接返回 `changed=false`（不下载、不写库、
  不重建词表）；不同才下载并重编译。GitHub 接口不可达时退化为下载后比内容
  sha256（`spam.lexicon_digest`），内容一致同样不重复写库。仍然只拉取
  `Vocabulary/微信输入法/微信输入法内置词库26.11.7.txt` 这一个文件。
- 接口返回 `downloaded.changed`，前端据此提示「词库已下载 / 词库已更新 /
  词库已是最新」，不再出现「点了更新但看不出有没有变化」。
- `.tools/headless-test/m88-spam-control.js`：35 项全过（新增词库位置链接与
  `target="_blank"`、文件名不再出现、二次「更新」识别为已最新且不写库）。

## [0.30.0] - 2026-11-15

### 新增

- **后台「功能开关 → 垃圾信息控制」**：总开关、自定义敏感词（一行一个）、
  词库下载按钮与概况、文本测试框。命中敏感词的用户输入一律**拒绝提交**，
  返回 `content_blocked` / 422 和带字段名的中文理由（如「评论包含不适当用语，
  请修改后重试」）；默认关闭，词库未下载或开关关闭时一律放行。
- `internal/platform/textfilter`：中文子串 + 纯 ASCII 按词边界的编译词表。
  大小写不敏感，词尾拼数字（`cnm123`）仍命中，忽略单字符 / `http` / `www` /
  URL 片段这类退化词条；**域名、URL、邮箱整体豁免**——`cnm` 命中，
  `cnmdcs`、`cnm.com`、`https://cnm.com/x`、`cnm@example.com` 都不命中。
- `internal/modules/spam`：配置与词库全文存 `system_settings`（词库全文不进
  接口响应），编译缓存 30 秒按 revision 轮询、后台保存 / 下载后立即重建；
  `Guard` 是所有写接口共用的拒发入口；配置服务异常时保持旧状态并放行。
- 后台接口 `GET/PUT /api/v1/admin/settings/spam`、
  `POST /api/v1/admin/settings/spam/lexicon`（下载）、
  `POST /api/v1/admin/settings/spam/test`（测试），读写权限分开、写操作走 CSRF。

### 改进

- **全站 10 个用户写接口 / 11 类自由文本接入审核**：作品标题与简介
  （创建 / 编辑 / 作品管理）、POI 位置名称与地址（编辑 / 发布）、话题名称、
  评论与回复、弹幕、昵称、个人简介、举报说明、申诉理由。邮箱、密码、凭据、
  slug、URL、对象键、枚举、数字 ID、时间戳一律不参与；评论正文里的
  `[[gif:URL]]` 标记在审核前剔除，图源地址不算用户输入的文字。
- **词库只在后台点「下载词库」时才下载**：固定拉取 `konsheng/Sensitive-lexicon`
  的 `Vocabulary/微信输入法/微信输入法内置词库26.11.7.txt` 这一个文件
  （172663 字节 / 13364 行，编译后 13360 条词），不拉 tarball、不遍历仓库、
  不自动检测更新、不下载其它词库；下载结果原子写入并校验编码与大小。
- 后台测试框可直接验证智能判断，命中时列出词条；下载 / 保存写审计。
- `.tools/headless-test/m88-spam-control.js`：32 项全过（后台卡片、启用与自定义词、
  7 组智能判断、点击下载与词库生效、10 个输入口的 422 理由、邮箱豁免、
  评论抽屉展示理由、回归后恢复默认）；Go 单测覆盖匹配算法、词库 URL 与配置脱敏。

### 修复

- 弹幕发送失败原本写死「弹幕发送失败，请稍后重试」，发布页草稿自动保存
  原本用「草稿保存失败：…」盖住真实原因，评论抽屉还会在具体理由下面再叠
  一句笼统的「发送失败」。三处改为优先展示服务端给的可操作理由，
  笼统兜底只在没有具体理由时出现。

## [0.29.3] - 2026-11-15

### 新增

- **后台表格窄屏卡片视图**：新增 `apps/web/app/assets/css/admin-cards.css` 并加入
  `nuxt.config.ts` 的 `css`；`.admin-table` / `.admin-cards` 在
  `@media (max-width: 1160px)` 切换，卡片外壳统一 `admin-card-*` 类名。
  话题运营、举报中心、用户管理、账号权限、系统设置（分类 / Connector / 导入任务 /
  审计日志）、数据概览、内容审核、作者申诉共 **9 个组件 11 张表格**补上卡片 DOM：
  同一批数据、同一组操作函数，宽屏仍是原表格。卡片含标题 + 状态标签、`<dl>` 键值、
  flex-wrap 按钮组；`AdminReview` 卡片加勾选框接管表格多选，批量通过仍可用；
  `AdminAccounts` 卡片保留角色下拉。

### 修复

- **窄屏话题管理操作按钮互相重叠**：390px 下 `AdminTopics.vue` 的
  `min-width="300" fixed="right"` 操作列被硬压缩，「下线 / 取消推荐 / 置顶 / 合并」
  边框互相裁切、第二行话题名被按钮盖住。根因：表格列宽合计约 868px + 侧栏 220 +
  内边距，窄屏必然横向滚动且右侧固定列压字。改法：≤1160px 隐藏表格、改用卡片，
  按钮换行排列，并补齐其它后台表格页。
- **侧栏会话卡片被全局样式误伤**：`pages/admin/index.vue` 的会话卡片原本就叫
  `.admin-card`（flex 布局 / 8px 内边距 / 无边框），与新增的全局列表卡片类撞名后
  会多画一圈边框，还被回归脚本误统计成一条空数据。改名 `.session-card`，
  全局卡片样式不再作用于它。

### 改进

- 卡片内长文本 `word-break: break-word`；按钮间距统一交给 `gap`，清掉 Element Plus
  相邻按钮的 `margin-left: 12px`（否则换行后第二行会整体右移 12px）。
- `.tools/headless-test/m87-admin-cards.js`：48 项全过（7 个后台模块在 390px 下
  表格全部隐藏、卡片接管、按钮与标题零重叠、无横向溢出；话题 / 举报 / 用户在
  1440px 回到表格且卡片隐藏；窄屏卡片点「合并」能打开弹窗且不提交）。

### 重构

- `AdminUsers.vue` 原有 scoped 的 `.user-card` / `.card-*` 样式与 1160px 断点迁入
  全局 `admin-cards.css`，类名统一为 `admin-card-*`，此处只保留表格 / 弹窗专用
  样式。外部行为不变，全后台共用一套断点与卡片口径。

## [0.29.2] - 2026-11-15

### 修复

- **后台话题运营位「推荐」无法取消**：点「推荐」后按钮仍是「推荐」，再点只会重复
  写同一条 `featured` 记录。根因：`SetTopicPlacement` 只做
  `INSERT … ON CONFLICT DO UPDATE`、没有删除分支，`placement` 也只接受
  `featured/pinned`；前端 `setPlacement` 固定传 `'featured'`；此外 `ListTopics`
  只返回「最新一条」`placement`，同时置顶时会显示成 `pinned`，不能拿它判断
  是否已推荐。改法：
  - `POST /api/v1/admin/topics/placements` 增加可选 `enabled`（缺省 true，兼容
    旧调用），`false` 时删除 `topic_id + placement` 那条记录；审计记录 `enabled`。
  - `ListTopics` 增加独立的 `featured` 布尔，`AdminTopic` 同步该字段。
  - `AdminTopics.vue` 的「推荐」按钮按 `featured` 在「推荐 / 取消推荐」间切换
    并传 `enabled`，成功提示区分「运营位已更新 / 已取消推荐」。

### 改进

- `.tools/headless-test/m86-topic-placement.js`：13 项全过（在临时话题上完成
  推荐 → 取消 → 再次推荐 的完整 toggle，接口 `featured` / `placement` 同步、
  按钮文案同步、无前端异常）；临时话题、孤立运营位与审计跑完清理，基线
  topics 2 / topic_placements 2 不变。

## [0.29.1] - 2026-11-15

### 改进

- **创作者主页 /「我」页返回按钮统一**：两页 hero-top 原来是「‹ 首页」纯文字
  链接（12px、无边框无底色），与项目其它页面（搜索 / 消息 / 个人库 / 设置 /
  草稿箱 / 关系 / 创作中心 / 话题）统一的 34×34 圆形边框返回钮不一致；截图里
  那块深蓝紫是 hero 渐变横幅、并非按钮底色，更容易显得另类。改成同款圆形按钮
  （`←` + `aria-label="回到首页"` + `to="/"`），两页 scoped 样式副本同步，
  删掉多余的 `ImIcon` 与 hover 变色。
  - `.tools/headless-test/m85-back-button.js`：15 项全过（文案 / 读屏标签 / 链接、
    与 `/topic` 对照页的宽高 / 圆角 / 边框 / 颜色 / 字号逐项一致、点击回到首页、
    无前端异常）。

## [0.29.0] - 2026-11-15

### 新增

- **后台评论举报展示与处置**：补上 0.28.0 接入评论举报后遗漏的管理端链路。
  - `internal/modules/admin/repository.go`：`ReportCase` 增加 `object_path`；
    `ListReportCases` 按对象类型取标题——作品取 `contents.title`、评论取正文摘要
    （`[[gif:…]]` 换成「[动图]」，不把内部标记展示到界面）、用户取昵称或邮箱；
    评论举报返回所属作品的 `/video|image/<slug>` 深链接，作品举报同样给链接，
    不再一律显示「（对象已删除）」。
  - `ResolveReportCase`：`upheld + comment + remove_comment` 时把评论软删除
    （`status='removed'`，与前台删除评论同一口径）；`admin/handler.go` 补
    `comment.remove` 审计留痕。评论举报判成立不再只是记录 `linked_action`。
  - `internal/modules/interactions/reports.go`：前台「我的举报」同样补评论标题，
    不再因为只 JOIN `contents` 而显示空标题。
  - `apps/web/app/components/admin/AdminReports.vue`：对象列改中文类型标签
    （作品 / 评论 / 用户）、评论摘要加「评论：」前缀、标题可点击跳转到作品；
    处置弹窗按对象类型给联动选项，评论专属「不联动评论 / 移除这条评论」。
    `useAdminApi.ts` 的 `AdminReportCase` 同步 `object_path`。

### 改进

- `.tools/headless-test/m83-admin-comment-report.js`：12 项全过（列表返回评论摘要
  与作品路径、作品举报单不受影响、处置接口 200、评论真的变 `removed`）。
  `.tools/headless-test/m84-admin-report-ui.js`：9 项全过（中文标签、摘要、跳转
  链接、处置弹窗专属联动且不含内容动作）。临时举报单、通知、审计与评论状态
  跑完按基线还原（cases 5 / reports 3 / user_notifications 14 /
  admin_audit_logs 76 / visible_comments 14 / comment 91 为 visible）。

## [0.28.1] - 2026-11-15

### 改进

- **右侧操作栏图标 hover 动效**：`apps/web/app/components/immersive/ActionRail.vue`
  给点赞 / 评论 / 收藏 / 分享四颗图标各加一组短促 keyframes（心跳缩放、轻弹、
  旋转、轻推，420–520ms 一次性），鼠标悬浮时播放、移开即取消。
  用纯 CSS 而不是示例里的 GSAP CDN：项目没有该依赖，外链脚本会多一个 CSP
  例外与一次外部请求；动画只作用于圆底内 `svg` 的 `transform`，不改布局、
  不改变按钮命中区，也不影响已有 `:active` / `.active` 状态。
  `@media (hover: hover)` 限定有悬停能力的设备，触屏不会把 `:hover` 粘住；
  `prefers-reduced-motion: reduce` 下完全禁用。
  - `.tools/headless-test/m82-rail-hover.js`：23 项全过（四个图标各自的
    animationName 与 transform、按钮几何不跳、移开取消、评论按钮仍能打开
    抽屉、减少动态效果下禁用、无前端异常）。

## [0.28.0] - 2026-11-15

### 新增

- **评论区点赞与更多操作**：主评论与楼中楼回复的「回复」后增加点赞与三点更多图标。
  - 点赞图标复用视频点赞的 `ImIcon heart`（14px，已赞染品牌粉），旁边显示点赞数；
    点击走服务端 toggle，乐观更新、失败回滚、未登录唤起登录弹窗。
  - `migrations/000022_comment_likes`：新增 `comment_likes` 表（comment_id + user_id
    主键去重）与用户维度索引。
  - `internal/modules/interactions/handler.go`：新增
    `POST /api/v1/comments/{commentID}/like`（登录 + CSRF，返回 `liked` / `like_count`）；
    `GET /api/v1/contents/{id}/comments` 与 `/comments/{commentID}/replies` 补
    `like_count` / `liked`（按当前访客即时计算，匿名恒 false），不进公共缓存口径。
  - 更多图标用指定的 24 格三点 SVG，点开「举报」菜单（带旗帜图标），复用
    `ReportDialog`；`ReportDialog` 新增 `objectType`，评论举报提交
    `object_type: 'comment'`（后端 CHECK 本就允许），标题与说明随类型切换。
    meta 行原有的删除文字按钮保持不变。菜单锚在按钮自己的 `.more-wrap` 上，
    不锚整条 meta，否则会被推到评论行最右端、离三点很远。

### 改进

- `.tools/headless-test/m81-comment-like.js`：18 项全过（图标顺序与同尺寸、举报弹窗、
  未登录引导与状态回滚、临时账号登录后点赞 0→1、刷新后仍已赞、再点取消回 0）；
  跑完清理临时账号并核对基线 users 19 / contents 15 / storage_objects 8 /
  comment_likes 0。

## [0.27.3] - 2026-11-15

### 修复

- **首页沉浸流 / 切视频时底栏闪现与控制条下方空白**：方向键或滚轮切条时底栏会短暂露出，
  控制条同时离开贴底位置，下方留一条 61px 空区。根因：`switching`（切换窗口锁）只在
  `watch(current)` 里置位，而 `current` 由 IntersectionObserver 滚到 ratio≥0.9 才更新
  （已是滚动末尾）；滚动中间态 observer 的 `ratio < 0.9` 分支先 `player.pause()`，
  `rawCollapsed` 变 false → 底栏展开。改法：`pages/index.vue` 抽 `beginSwitch()`，
  在 observer 的「开始滚动」分支就锁住，`watch(current)` 只做兜底；方向键 `go()`
  仍只负责 `scrollTo`，激活和滚轮 / 触摸走同一条 observer 路径。
- **详情页视频播放失败 / `00:00 / 00:00`**：`useImmersivePlayer.ts` 的 mp4 分支设完
  `src` 就立刻把 `state.ready` 置 true，元数据没到时控制条先显示 `00:00 / 00:00`
  被当成播放失败；真报错也不会置回失败态，重试按钮还拿着 SSR/SWR 里可能过期的
  签名 URL。改法：`state.ready` 改由 `loadedmetadata` 置位，`error` 时置回 false
  并给「播放失败，点击重试」；`ImmersiveDetail.vue` 增加 `reloadDetail()`（重新取
  公开详情刷新媒体地址后 `player.load`），失败时自动重试一次，重试按钮也走它。

### 重构

- **键盘控制两页统一**：`utils/playerKeys.ts` 只处理左右 ±5 秒定位；首页 ↑/↓ 切视频，
  详情页 ↑/↓ 只阻止页面滚动（只有一条视频，没有下一条可切）。删掉 `volumeKeys`
  开关、`volumeHint` prop 与 `ImmersiveItem.vue` / `ImmersiveDetail.vue` 里全部
  音量提示定时器——键盘调音量取消，音量只由控制条滑块 / 静音键控制；音量浮层的
  hover 宽容期逻辑不变。行为不变：左右键、输入框聚焦不抢键、空格 / m / f 快捷键。

### 改进

- `.tools/headless-test/m77-player-hotkeys.js` T2 改为「详情页上下键不调音量」；
  新增 `m80-switch-chrome.js` 15 项（方向键 / 滚轮切换全程底栏收起且控制条贴底、
  稳定后无空白、详情页无 `00:00 / 00:00`、两页键盘一致、拦截首次 mp4 请求后
  自动重试恢复）；`m78-volume-hint.js` 因测的对象已移除而退役删除。

## [0.27.2] - 2026-11-15

### 修复

- **首页沉浸流 / 方向键**：0.27.0 把 ↑/↓ 从「切换上一条 / 下一条视频」改成了
  「调音量」，沉浸流没法用键盘翻内容。根因：把上下键在两个沉浸入口统一当成音量键，
  没区分首页有多条内容、详情页只有一条。改法：`utils/playerKeys.ts` 的
  `handlePlayerArrowKeys` 增加 `volumeKeys` 开关；`pages/index.vue` 传
  `{ volumeKeys: false }`，并在自己的 switch 里恢复 `go(current ± 1)`（滚轮 /
  触摸切视频不受影响），同时删掉首页的 `volumeHint` 及对应 prop——首页不再有
  键盘调音量，不留死代码。`ImmersiveDetail.vue` 保持上下键 ±5% 音量与 0.27.1
  的音量指示条自动唤出；两页左右键仍 ±5 秒定位、输入框聚焦不抢键。

### 改进

- `.tools/headless-test/m77-player-hotkeys.js` 与 `m78-volume-hint.js`：T4 断言
  反过来——首页 ↓/↑ 让 `.scroller.scrollTop` 0→900→0、不弹音量指示条；详情页
  上下调音量与指示条弹出的断言保留。m77 9 项、m78 15 项全过。

## [0.27.1] - 2026-11-15

### 修复

- **播放控制条 / 音量指示条（键盘调音量）**：按 ↑/↓ 时音量确实变了，但音量指示条
  始终不弹出来，看不到当前音量。根因：方向键在页面层只调用 `player.setVolume()`，
  指示条却由 `ImmersiveItem.vue` 内部的 `openPop` 控制，组件收不到「音量变了」的信号；
  旧的 m77 回归每次调音量前都先 hover 出浮层，也没覆盖到这个场景。改法：
  `utils/playerKeys.ts` 的 `handlePlayerArrowKeys` 回报 `'seek' | 'volume' | null`；
  `pages/index.vue` 与 `ImmersiveDetail.vue` 在音量键时递增 `volumeHint` 并传给控制条；
  `ImmersiveItem.vue` watch 该信号，只有 `active` 卡片唤出指示条（首页同时渲染 3 条，
  不会一起弹），连按重置计时、静手 1.5s 自动收起；鼠标进入音量按钮 / 指示条即接管，
  交回原有 300ms hover 宽容期，切内容 / Esc / 点静音 / 卸载都清理定时器。
  音量锚点加 `.pb-anchor-volume` 供 `:hover` 判定；左右键仍只定位、输入框聚焦不弹。

### 改进

- `.tools/headless-test/m78-volume-hint.js`：新增 13 项回归，刻意不 hover 音量按钮，
  验证直接按 ↓ 指示条自动弹出并显示 95、连按重置计时、超时收起、hover 接管不误关、
  首页只弹 1 个、左右键与输入框聚焦不误弹（`.tools` 不入库）。

## [0.27.0] - 2026-11-15

### 新增

- **播放器方向键快捷键**：`apps/web/app/utils/playerKeys.ts` 新增共享处理，
  左右 ±5 秒定位、上下 ±5% 音量；`apps/web/app/pages/index.vue` 与
  `apps/web/app/components/immersive/ImmersiveDetail.vue` 的全局 keydown 复用
  同一份实现，两个沉浸入口行为一致。
  - 只在拿到视频时长时生效；输入框 / 文本域 / 可编辑区聚焦时不抢键；
    `setVolume` 会自动解除静音，音量按百分比取整避免浮点尾数。
  - 首页沉浸流的上下键原本用于切换视频，现让位给音量；切视频仍可用滚轮 /
    触摸滑动。`ImmersiveItem.vue` 进度条上原有的局部 `@keydown.left/right`
    删除，改由页面级统一处理，避免聚焦时局部与全局各 seek 一次变成 ±10 秒。
  - `.tools/headless-test/m77-player-hotkeys.js`：9 项全过（详情页左右 ±5 秒、
    上下 ±5% 音量、输入框聚焦不误触、首页上下改为音量且控制条不切走、无异常）。

## [0.26.0] - 2026-11-15

### 新增

- **弹幕持久化（表 + 接口 + 前端接入）**：此前弹幕只存在组件内存里，
  `danmakuActive` 只保留 8 秒内条目，滚一次就移除、刷新清空。
  - `migrations/000021_danmaku.*`：新增 `danmaku` 表（content_id / user_id / body /
    time_ms / created_at）与 `(content_id, time_ms, id)` 索引；`time_ms` 记录发送时
    的视频播放位置。
  - `internal/modules/danmaku/handler.go`：`GET /api/v1/contents/{id}/danmaku`
    公开按时间点返回（上限 500 条）；`POST` 走登录 + CSRF，正文限 30 字、
    时间点上限 24h，只允许给已发布可播放的视频写。路由经
    `internal/app/router.go` 的 `Dependencies.Danmaku` 注入。
  - `ImmersiveItem.vue`：激活时拉取，发送入库后本地立即显示；把 8 秒内存窗口改成
    按 `player.currentTime` 跨过的时间区间逐条触发，视频 loop 回绕清空触发标记后
    重新播放，过期弹幕在下一帧清理，切换内容时重置列表与游标。401 用登录弹窗提示，
    其它失败保留草稿并给一句可操作提示。
  - 发弹幕需要登录（与点赞 / 评论同一口径）；未登录点发送提示「登录后即可发弹幕」。
  - `.tools/headless-test/m76-danmaku-persist.js`：插一条临时弹幕验证 GET 200、
    首次加载与刷新后都出现、两次接口请求、POST 未登录 401；跑完删除并核对
    `danmaku` 表回到 0 条。

### 修复

- **功能条表情面板离表情按钮太远**：`EmojiPanel` 的 `right: 0` 是为评论的窄锚点
  准备的，挂到几百 px 宽的 `.pb-input` 上会贴到输入区最右端。
  `ImmersiveItem.vue` 加 `.pb-input :deep(.emoji-panel) { left: 0; right: auto }`
  让面板左对齐输入区、正好浮在表情按钮上方。
  - `m75-danmaku-emoji.js` 加两条断言：面板左边缘与表情按钮差 ≤20px、
    面板不再贴输入区右端，共 20 项全过。

## [0.25.0] - 2026-11-15

### 新增

- **功能条弹幕输入接入表情面板**：`apps/web/app/components/immersive/EmojiPanel.vue`
  新增 `emojiOnly` 模式（`true` 时隐藏底部「表情 / GIF」切换、不渲染 `GifPanel`），
  因为弹幕是纯文本、放不了 GIF；`ImmersiveItem.vue` 的 `.pb-input` 表情图标从
  装饰改为可点按钮，移动端 `.dm-sheet` 的「插入表情」也不再写死 😊，
  两处都打开 `emojiOnly` 面板。
  - 选中表情插到光标处（与评论 `insertEmoji` 同口径），`maxlength=30` 自己挡一道；
    面板可连选，发送 / 点面板外 / Esc / 切换内容时收起。
  - 评论面板默认行为不变（仍是两个页签 + GIF 搜索）。
  - `.pb-input` 从 `<label>` 改成 `<div>` 并加 `position: relative` 作面板锚点，
    避免 `<label>` 把面板内的点击也当成聚焦输入框；表情按钮只做图标提亮、不加悬浮灰底。
  - 移动端面板用 `.dm-sheet :deep(.emoji-panel)` 强制横向铺满输入条。
  - `.tools/headless-test/m75-danmaku-emoji.js`：校验弹幕面板无 GIF 页签、光标处插入、
    toggle / 点外 / Esc 收起、评论面板仍有两个页签、移动端铺满与插入、截图。

## [0.24.1] - 2026-11-15

### 改进

- **创作者主页复用「我」页布局**：`apps/web/app/pages/creator/[handle].vue` 原来是
  独立的极简 hero + 自适应作品列表，与「我」页视觉不一致。现改成同一套渐变横幅
  hero（104px 头像 / 昵称 + 认证标 / 作品·获赞·关注·粉丝四项计数 / 夜街号 / 简介）、
  分区标题条、右侧搜索框与 6 列作品卡片网格（封面 fallback / 获赞角标 / 单行标题），
  桌面 1280 宽 5 列、窄屏 3 / 2 列的断点与「我」页一致。
  - 访客视角去掉编辑资料、草稿箱、个人库、消息、设置、发布作品、批量管理、
    我的举报等私密入口；右上换成「关注 / 已关注」，本人访问时隐藏。
  - 分区条只有「作品」一个分区且用非按钮标签渲染，不做点了没反应的死按钮；
    搜索框做本地标题过滤、不新增接口；空态区分「搜不到」与「还没有作品」。
  - 保留独立路由 `/creator/<夜街号>`（`/creator/34` 仍补零成 000034）、SSR 取数、
    关注状态、`CreatorBadge` 与 `data-surface="immersive"`。
  - 样式是 scoped 副本，不把 `.hero` / `.card` / `.grid` 这类通用类名提到全局污染
    其它页面；`.avatar` 外径沿用「我」页口径（104 内容 + 2px 边框×2 = 108）。
  - `.tools/headless-test/m74-creator-layout.js`：22 项全过（hero 渐变 / 四项计数 /
    分区条非按钮 / 搜索过滤 / 5 列与 2 列断点 / 无私密入口 / 关注按钮 / 补零 / 截图）。

## [0.24.0] - 2026-11-15

### 新增

- **视频作者头像可点击进入个人主页**：`apps/web/app/components/immersive/ActionRail.vue`
  右侧操作栏的圆形头像原来只是展示，现包成指向 `/creator/${item.author.handle}` 的
  `NuxtLink`；首页沉浸流与视频详情页共用同一组件，两处一起生效。
  - `aria-label` 用作者昵称（缺省回落「用户 + 夜街号」，与信息层同一口径），
    hover 时头像描边转品牌粉，
    键盘聚焦沿用全局圆形焦点环；界面不增加任何提示文字。
  - 关注按钮仍是头像外的独立绝对定位按钮，点它只切换关注、不触发跳转；
    画面点击播放 / 暂停也不受影响（头像不在 `.media-slot` 内）。
  - `.tools/headless-test/m73-avatar-link.js`：校验链接 href、跳转后的创作者页、
    关注按钮不误导航、桌面 / 移动端截图。

## [0.23.7] - 2026-11-14

### 改进

- **默认头像改用 DiceBear Critters**：`internal/platform/avatar/dicebear.go` 的 `Endpoint`
  从 `10.x/micah/png` 换成 `10.x/critters/png`。新用户默认头像、设置页「随机头像
  6 选 1」、后台给用户设置头像共用同一份实现，三处一起切换。
  - 源文件从 `micah.go` 重命名为 `dicebear.go`（内容是通用的 seed 生成与 PNG
    下载校验，不该绑死风格）；同步 `cmd/api/main.go`、
    `internal/modules/auth/default_avatar.go`、`internal/modules/admin/user_avatar.go`
    的注释与 `README.md`「默认头像」章节。
  - Critters 由 DiceBear 以 **CC0 1.0**（公有领域）发布，不要求署名；原 Micah 的
    CC BY 4.0 / Micah Lanier 署名因此失效，删掉 `apps/web/app/pages/settings.vue`
    头像区那段小字，界面不再展示开发与许可说明，README 改记风格来源。
  - `GET /api/v1/avatar/preview?seed=…` 实测 200 / `image/png`，与直连
    `10.x/critters/png?seed=…&size=256` 字节完全一致（md5 相同），确认 API 重启后
    走的是新风格；同源代理与 CSP 白名单不变。
- **评论框图标去掉悬浮灰色底框**：`apps/web/app/components/immersive/CommentSheet.vue`
  的 `.pick:hover:not(:disabled)` 删掉 `background: rgba(255,255,255,0.1)`，
  只保留 `color` 提亮；悬停不再出现那块长方形圆角灰底。
  - `m71-composer-inline.js` 加断言：悬停时背景为 `rgba(0, 0, 0, 0)`、图标仍提亮，
    连同原有用例共 17 项全过。

## [0.23.6] - 2026-11-14

### 改进

- **评论输入区 · 取消独立发送按钮，输入框占满一行**：`apps/web/app/components/immersive/CommentSheet.vue`
  原来是「输入框 + @/表情按钮 + 44px 粉色圆形发送按钮」三段并排，输入框被挤窄。
  现在删掉发送按钮，把 `input` 与 `@` / 表情两个图标一起包进 `.input-wrap` 一整条圆角长条，
  图标内嵌右端；发送改由回车 / 软键盘「发送」键触发（`<form @submit.prevent="submit">`
  保留，区内只剩一个 text input，隐式提交照常生效，`submit()` 对空输入仍直接返回）。
  - 边框 / 圆角 / 底色 / 焦点态从 `input` 挪到 `.input-wrap`（`:focus-within`），
    失败态改 `.composer.failed .input-wrap`；`input` 自身透明无边框，文字区不与图标重叠。
  - 补 `input:focus-visible { box-shadow: none }`：`tokens.css` 的全局品牌粉焦点环
    原本会画在直角 input 上，变成一个与外层胶囊不搭的矩形（像表单报错）；
    焦点统一由 `.input-wrap` 的边框表达，与弹幕 / 搜索输入区同一口径。
  - `.pickers` 仍保持 `position: relative`（窄屏 `static`），表情面板桌面右对齐 /
    移动端铺满一行的定位不变；`.pick` 高度 42→40px 适配外壳内高。
  - 删掉 `button[type="submit"]` 与 `.send-icon` 的全部样式。
  - `.tools/headless-test/m66-send-icon.js` 退役（测的对象已移除），新增
    `m71-composer-inline.js` 校验无发送按钮、输入区占满一行、图标内嵌、
    回车提交通路（空输入不写库）、表情面板桌面 / 移动端定位。

## [0.23.5] - 2026-11-14

### 修复

- **播放控制条 · 音量滑块一移鼠标就消失**：`apps/web/app/components/immersive/ImmersiveItem.vue`
  的音量浮层用 `.pb-pop` 的 `bottom: calc(100% + 8px)` 浮在按钮上方，鼠标从按钮走向滑块
  会先落进这 8px「既不属于按钮也不属于浮层」的空隙，`@mouseleave="hoverVolume(false)"`
  当场把 `openPop` 清空，滑块始终点不到。
  - `hoverVolume(false)` 改成留 300ms 宽容期再关；期间鼠标重新进入按钮或浮层
    （`cancelVolumeClose`）就撤销关闭，浮层自身也接管 `mouseenter` / `mouseleave`。
  - `.pb-pop-volume::after` 加一条 30×10px 透明桥接条补上空隙，让鼠标路径始终落在
    `.pb-anchor` 的后代里；宽度只盖按钮正上方，不挡左侧其它按钮。
  - 按钮 `@focus` 改走 `hoverVolume(true)`；`onVolumeClick`、切换内容、组件卸载时
    都清掉待关闭定时器，避免延迟回调误关后来新开的浮层。
  - `.tools/headless-test/m70-volume-hover.js`：分步模拟鼠标跨过间隙到滑块、命中
    range 控件、点击滑块、移开后的宽容期与最终关闭、宽容期内移回可撤销关闭。

## [0.23.4] - 2026-11-14

### 改进

- **播放控制条 · 弹幕三颗图标统一换成设计稿字形**：`apps/web/app/components/immersive/ImmersiveItem.vue`
  的弹幕开关两态与「弹幕设置」按钮改用同一套 1024 格 SVG——开「气泡 + 圆形对勾」、
  关「气泡 + 圆形斜杠」、设置「气泡 + 六边形螺母」；`fill` 统一 `currentColor`，
  开态仍由 `.pb-word.on` 染品牌粉，关态与设置保持次级文字色。
  - 三颗图标共用 `.pb-danmaku`，尺寸统一 18×18 居中，不再各写一条 CSS；
    删掉旧 24 格自制字形与 `fill-opacity` / `fill-rule` / `clip-rule` 属性。
  - `aria-pressed` / `aria-label` / `title` 与两态切换逻辑保持不变。
  - `.tools/headless-test/m69-danmaku-icons.js` 新增三图标一致性回归；
    `m67` / `m68` 断言同步到 1024 格口径。

## [0.23.3] - 2026-11-14

### 改进

- **播放控制条 · 弹幕开关**：文字「弹」+ 粉色小圆点换成双态图标——
  开：气泡 + 勾（`.pb-word.on` 把 `currentColor` 染成品牌粉 `#ff2e7e`）；
  关：气泡 + 禁止符（次级文字色）。点一下文字与状态一起变，不再靠一个 4px 小圆点表达开/关。
  - `aria-pressed` 保留，`title` 随状态在「关闭弹幕 / 打开弹幕」之间切换；
    图标 18px、与「弹幕设置」同尺寸对称排布。
  - 顺手删掉只服务旧文字态的 `.pb-dot` 样式。
  - `.tools/headless-test/m68-danmaku-toggle.js`：14 项全过（两态图标不同、粉色/灰色、
    title 与 aria-pressed 同步、18px 居中、旧粉点已移除、来回切换正常）。

## [0.23.2] - 2026-11-14

### 改进

- **播放控制条 · 弹幕设置按钮**：原来是文字「弹」（和左边的弹幕开关长得一模一样），
  换成设计给的 24 格组合字形（漏斗 + 螺母）SVG。
  - `fill="currentColor"` + 保留 `fill-opacity=".9"`：设计稿是纯白，但控制条里其他图标走
    `--vp-im-text-secondary`，直接写白会比邻居亮一档。
  - 图标 18px 落在 30px 方钮里正中（偏差 0，靠 `.pb-word.pb-icon` 的 grid 居中，
    不再吃文字的 `padding: 0 6px`），与左侧播放 / 音量图标同高。
  - `.tools/headless-test/m67-danmaku-settings-icon.js`：10 项全过（无「弹」文字、24 格、
    currentColor、18×18 / 30×30、居中、aria-label 与点击提示仍正常）。

## [0.23.1] - 2026-11-14

### 改进

- **评论发送按钮图标**：从 `ImIcon name="share"`（共用分享箭头）换成独立的纸飞机 SVG
  （24 格单路径、纯白填充），并给按钮补 `display:grid; place-items:center`——
  图标 18px 在 44px 圆钮里四边留白刚好各 13px，之前靠行内基线对齐会偏下 1–2px。
  `.tools/headless-test/m66-send-icon.js` 12 项全过（viewBox / 单路径 / 白填充 / 44×44 与 18×18 /
  四边留白差 ≤1px / 空输入禁用 / 移动端与表情键留 8px 间隙）。

## [0.23.0] - 2026-11-14

### 新增

- **底栏「消息」未读小粉点**：有未读通知时，底栏消息图标右上角显示 7px 的品牌粉圆点
  （`--vp-im-accent`，带一圈与底栏同色的描边，避免糊在图标笔画上）；读完 / 登出后消失。
  - 新接口 `GET /api/v1/notifications/unread`：只回 `{unread, unread_by_category}`，
    不查列表——底栏每个页面都要用，必须便宜（复用 `unreadCounts`）。
  - `composables/useUnread.ts`：`useState` 共享计数，60s 轮询，离开 `/messages` 时立刻刷新；
    401（未登录 / 刚登出）清零，网络抖动保持上一次的值、不弹提示。
  - `BottomNav.vue`：粉点挂在「消息」项上；`aria-hidden`，不参与读屏（未读数在消息页头部已有文案）。
  - `.tools/headless-test/m65-unread-dot.js`：12 项全过（未登录无点、有未读出现的颜色/尺寸/圆角、
    登出清零、重新登录恢复、标记已读后离开消息页消失）。

## [0.22.1] - 2026-11-14

### 改进

- **开发脚本 · `reset-limits`**：登录限流计数存在 Redis（后台 `admin_login_fail:<ip>`，
  连续 10 次失败锁 15 分钟；前台 `yejie:ratelimit:login:<ip>`），手测时故意输错密码
  很容易把本机锁住。新增 `node scripts/dev.mjs reset-limits` 一键清空这两类键，
  不用再记 `redis-cli --scan` 的组合命令；README 与其他子命令一起列出。
  （本次就是因为验证「错密码返回 401」时累计失败 10 次把自己锁了，已手动清掉并复验登录 200。）

## [0.22.0] - 2026-11-14

### 新增

- **沉浸界面 · 发布时间**：视频与图集左下角作者名右侧显示发布时间（`名字 · 4天前`），
  规则按需求实现：1 分钟内「刚刚」→ 按分钟 → 按小时 → 7 天内按天 → 超过 7 天显示
  `M月D日` → 超过 365 天才补年份 `YYYY年M月D日`。
  - 新增 `app/utils/time.ts` 的 `publishedLabel(value, now?)`：未来时间（时钟误差 / 定时发布）
    统一按「刚刚」，非法值返回空串由调用方决定不渲染。
  - `InfoLayer` 加 `publishedAt` 属性，渲染成带 `datetime` 的 `<time>`；时间字号 14px、
    透明度 0.75，比作者名弱一档。
  - `apps/web/tests/time.test.ts`：7 组边界用例（空值 / 刚刚 / 分钟 / 小时 / 天 / 满 7 天出日期 /
    364 天不带年、366 天带年）。
  - `.tools/headless-test/m64-author-time.js`：12 项全过（视频与图集都有时间、同一行、
    `<time datetime>` 正确、移动端不折行不越界）。

## [0.21.2] - 2026-11-14

### 改进

- **播放器 · 静音键改为来回切换**：同一个键既能取消静音，也能重新静音。
  - 桌面功能条：点击音量键 = 切换静音（文案在「取消静音 / 静音」之间切换）；
    音量滑块改为**鼠标悬浮**出现（点击已被静音开关占用，再兼职开弹层会互相打架）。
  - 移动端顶栏那枚图标不再「点完就消失」——之前取消静音后就没有任何办法再关掉声音，
    现在常驻显示，图标随状态在 🔇 / 🔊 之间切换。
  - 事件名 `unmute` 改为 `muteToggle`，`pages/index.vue` 与 `ImmersiveDetail.vue` 同步为
    `player.setMuted(!player.state.muted)`。

### 修复

- **详情页「晚点再看」胶囊在窄屏压住顶栏图标**：390px 下胶囊占 x292–370、搜索图标占 x330–374，
  两者重叠（肉眼看是搜索框里透出一行「晚点再看」）。`max-width:780px` 时收起——
  晚点再看在右侧「⋯」菜单里本来就有入口。
- `.tools/headless-test/m63-unmute-bar.js` 扩到 23 项：新增「再点一次重新静音」「移动端图标常驻
  且可再关」「滑块靠悬浮出现、移开收起」。

## [0.21.1] - 2026-11-14

### 修复

- **播放器 · 取消静音**：画面上那枚「🔇 点击取消静音」浮层胶囊已移除；桌面端的音量键
  在静音状态下改为**点一下直接恢复声音**（原来是「打开音量滑块」，滑块还要再拖一次，等于点了没反应）。
  非静音时仍点开滑块调音量；`aria-label` 随状态在「取消静音 / 音量」之间切换。
  - 手机端整条功能条本就不渲染（`max-width:780px` 时 `.player-bar` 隐藏），浮层一删就没有开声音的入口，
    因此在顶栏「搜索」左边补一颗 44px 的纯图标按钮（静音时出现，点一下恢复声音后自动收起），
    不压作者信息、不压右侧按钮列。
  - `.tools/headless-test/m63-unmute-bar.js`：16 项全过（浮层不存在、音量键在条内、点一下即取消静音、
    非静音才弹滑块、移动端图标位置与点击效果、无 pageerror）。

## [0.21.0] - 2026-11-14

### 新增

- **已认证创作者标签**：创作者（`users.role = 'creator'`）的昵称旁显示靛蓝印章图标，
  出现在三个位置——评论区（顶层评论与楼中楼）、创作者主页标题、「我」页昵称。
  - 组件 `components/base/CreatorBadge.vue`：设计稿给的 24 格实心印章（对勾靠 `evenodd` 镂空），
    尺寸由 `size` 传入，评论 13 / 楼中楼 12 / 我页 16 / 主页标题 18，`aria-label="已认证创作者"`。
  - 接口补字段 `verified_creator`：`GET /contents/{id}/comments`、`.../replies`、
    `POST /comments`（刚发的评论也要带标签，否则要刷新才出现）、
    `GET /creators/{handle}` 的 `profile`、`GET /me/profile`。
  - 判定一律用 `users.role = 'creator'`，不额外加表；取消认证后标签立即消失。
  - 首页信息流里的作者名暂未加（本轮只按要求覆盖评论区与个人主页）。
  - `.tools/headless-test/m62-creator-badge.js`：12 项全过（标签存在、无障碍名、尺寸区间、
    与昵称中心偏差 ≤3px、实心 `#4f46e5` + 单路径 evenodd、未认证用户无标签、我页有标签）。

## [0.20.1] - 2026-11-14

### 修复

- **后台 · 用户管理窄屏错位**：手机上表格 7 列被挤成一团、右侧固定列压在「最近处置 /
  注册时间」上，操作按钮和「正常」标签互相叠字。原因是 `ElTable` 的列最小宽度合计约 868px，
  窗口窄于该值时表格横向滚动，`fixed="right"` 的操作列浮在其它列之上。
  改法：
  - `AdminUsers.vue` 新增卡片列表（账号 + ID、角色/状态标签、作品/最近处置/注册、
    操作按钮），`@media (max-width: 1160px)` 隐藏表格、显示卡片；1161px 起仍用表格。
  - 操作按钮抽成 `AdminUserActions.vue`，表格与卡片共用一套逻辑（含「认证 / 取消认证」二选一），
    顺带压掉「同一条用户操作在两处各写一遍」的漂移风险。
  - 列宽收紧（账号 160→140、角色 100→84、状态 90→80、作品 72→56、最近处置 110→100、
    注册时间 160→112、操作 230→296），按钮内边距 5px 9px、间距 6px，
    1280px 起操作列不再折成两行。
- **后台 · 弹窗宽度**：`400/440/520px` 的写死宽度在 390px 屏幕上顶出右缘，
  统一加 `html.dark .el-dialog { max-width: calc(100vw - 32px) }`。
- `.tools/headless-test/m61-users-mobile.js`：29 项全过（390px 卡片布局、按钮不越界/不被标签压住、
  弹窗不溢出、900/1160/1161/1280 四档断点与「表格不横向滚动」）。

## [0.20.0] - 2026-11-14

### 新增

- **后台 · 用户管理**：创作者可以取消认证。已认证用户的「操作」列不再把按钮藏起来，
  而是把「认证」换成「取消认证」，点击后走同一个处置弹窗（理由必填）。
  - 接口：`POST /api/v1/admin/users/{id}/sanction` 新增动作 `unverify_creator`，
    把 `users.role` 从 `creator` 退回 `user`，并写处置记录 + 站内通知「创作者认证已取消」；
    对非创作者调用返回 400「该用户不是创作者」，对管理员账号拒绝降级。
  - 迁移 `000020_sanction_unverify_creator`：`user_sanctions.action` 的 CHECK 原本只允许
    `warn/mute/ban/unban/verify_creator`，不放开约束写入会被 23514 打回（表现为 500）。
  - 撤回只退角色：已发布作品、粉丝关系、作品计数全部保留，不做连带下架。
  - `.tools/headless-test/m60-unverify-creator.js`：10 项全过（创作者行有「取消认证」
    且无「认证」、普通用户行相反、弹窗标题与确认文案正确、点取消不写库）。

## [0.19.1] - 2026-11-14

### 改进

- **后台 · 功能开关**：删掉页面里的全部说明性小字（页头副标题、卡片说明、
  图源/线路/缓存三段提示），只留字段名与「已启用 / 未启用」状态标；
  选项文案去掉开发者口径（`关闭（评论区隐藏 GIF 页）` → `关闭`、
  `线路二 · 普通集群负载（域名接口）` → `普通集群`、`内容分级（content_filter）` →
  `内容分级`、分级档位 `off/low/medium/high` 改为 `不限/低/中/高`）；页内重复的
  「功能开关」标题删除，状态标移入「GIF 表情包」卡片头。
- **后台 · 页头**：各分区标题下一律不再显示一行说明（`page-head` 副标题与
  导航配置里的 `desc` 字段一并删除），页头只保留分区名与右上角角色标。
- `.tools/headless-test/m55-admin-flags.js`：断言反转并加强——不再要求「有解释文案」，
  改为校验页头无副标题、卡片无说明段、表单无提示小字、文案里不出现
  `localStorage` / `content_filter` / `线路二` / `线路三` 这类开发口径。

## [0.19.0] - 2026-11-14

### 新增

- GIF 图源补上 **KLIPY**（参考插件里的「线路一」）：后台「功能开关 → GIF 表情包 → 图源」
  现在有「关闭 / API 盒子 / KLIPY」三选一。
- 两条线路的走法不同，代码里按图源分流：
  - **API 盒子**：本站代理（`/api/v1/gifs/search`），id/key 只在服务端；结果落库缓存省配额。
  - **KLIPY**：浏览器直连 `api.klipy.com`。它的条款要求请求由终端发起、不得经服务端转发或缓存，
    所以 `app_key` 由新增的 `GET /api/v1/gifs/config` 下发给前端（它本身就是客户端密钥），
    结果缓存只写访客自己的 `localStorage`（默认 1 天，可关）。
- KLIPY 配置项：App Key、内容分级 `content_filter`（off/low/medium/high）、国家/地区（ISO 两位，
  留空则不带）、每页条数（8–50）、浏览器缓存天数（0–90）。
- KLIPY 集成要求的两条都在：搜索框占位固定 `Search KLIPY`（必需），面板底部 `Powered by KLIPY`。
- `gifs.Config` 增加 `klipy_*` 五个键（迁移 `000019_gif_klipy`）；
  `Ready()` 按当前图源各自的密钥判断，后台保存时对应图源没填密钥会被拦下。

### 安全维护

- CSP 显式放行 KLIPY：`connect-src` 加 `https://api.klipy.com`，`img-src` 加 `https://*.klipy.com`。
  没有依赖 `YJ_MEDIA_ORIGINS` 里那个 `https:` 兜底——生产可能把媒体源收敛成自有 CDN。
- `/api/v1/gifs/config` 只下发前端发起请求所需的最小信息：KLIPY 的 key + 参数，
  apihz 只给条数，**id/key 永不出现在这个接口里**。服务端 `/search` 对 KLIPY 直接 400
  （`gif_client_side`），杜绝「绕过条款用服务端代理」这条路径。
- 服务端响应解析改为防御式：先找第一个「像结果集」的数组（`data/results/gifs/stickers/items`），
  再从每条里挖 https 图片地址（`file/media/preview/gif/webp/tinygif/hd/url`），
  下一页判定同时认 `has_next`、`next_page` 和 Tenor 口径的 `next` 游标。

## [0.18.3] - 2026-11-14

### 改进

- 底部页签两个图标改成**一样大**：GIF 徽标渲染 30px → 32px，视觉圆环 18.3px → 19.6px，
  与左侧 26px 圆脸图标的圆环（18/24 → 19.5px）等大。之前为了「比表情小一档」把它压小了，
  并排看确实像少画了一圈。

## [0.18.2] - 2026-11-14

### 修复

- **GIF 搜索永远报「表情包服务暂时不可用」**：上游把 `page`、`count` 返回成**字符串**
  （`{"page":"1","maxpage":3075,"count":"9223"}`），我方按 `int` 解码，整条响应直接失败。
  改法：加 `flexInt` 类型，数字 / 字符串 / 空值都能吃，单个字段异常按 0 处理不再废掉整个响应。
- **热门一进来就「没有更多了」**：热门接口的响应**根本没有** `page/maxpage/count`
  （只有 `{"code":200,"res":[…]}`），旧代码把缺失的 `maxpage` 兜成 1 页，无限加载当场被掐死。
  改法：`max_page = 0` 表示「上游没说还有多少页」，前端改为按「某页返回空 / 返回的全是重复」
  判断到底；热门实测按 `page` 翻页是有效的（p1/p2/p3 各 21 条且互不重复）。
- 上游业务错误（密钥不对、额度用尽）不再被统一成「服务暂时不可用」，改为把上游的中文原文透出
  （截断 120 字），用户能直接看到「通讯秘钥不正确」这种可操作的提示。
- 前端追加分页时按 URL 去重：上游偶尔会把同一批图再给一遍，重复 key 会让列表抖。

## [0.18.1] - 2026-11-14

### 改进

- GIF 结果网格从一行 2 格改成 **一行 4 格**：格子宽度 158px → 78px、圆角 10 → 8px、间距 6 → 4px，
  一屏能扫到的候选翻倍，和表情页 8 列密度的观感更接近。
- 结果列表改为**无限加载**：去掉「加载更多」按钮，滚到距底部 120px 时自动接下一页；
  到底后显示「没有更多了」，续接过程中底部显示「加载中…」。
  另外补了一条「一页填不满容器时自动续一页」——否则格子少到不足以产生滚动条，
  滚动事件永远不触发，用户会停在一页上再也看不到更多。
- 图片仍是 `loading="lazy"`（只在进入视口时下载），配合无限加载不会一次拉几十张。

## [0.18.0] - 2026-11-14

### 新增

- **评论 GIF 表情包**：表情面板底部的「GIF」页签从占位变成真功能——搜索框 + 双列结果网格，
  点选后挂在输入框上方预览（可移除），发送时以图片形式进评论。参考 `owlsgo/plugins/gif-stickers`，
  但落地方式按本项目来：**服务端代理**，密钥不出站。
- 后台新增 **「功能开关」** 分类（导航在「系统设置」之后）：GIF 图源选「关闭 / API 盒子」，
  填线路（普通集群 / VIP）、开发者 ID、开发者 KEY、每次返回（1–20）、结果缓存天数（0–90）。
  密钥只存服务端，浏览器拿不到。
- 新模块 `internal/modules/gifs`：
  - `GET /api/v1/gifs/search?q=&page=`（需登录）：服务端代上游（`apihzbqb.php`，空关键词走热门、
    有关键词走搜索），每人 20 次/分钟限流；结果按「线路 + 关键词 + 页码 + 条数」哈希缓存到
    `gif_search_cache`，过期清理 + 最多保留 500 行。
  - `GET/PUT /api/v1/admin/settings/gif`：走 `settings:read` / `settings:write` + CSRF，
    写操作进审计——审计只记 provider/线路/ID/条数/天数与「key 是否设置过」，不落 key 本身。
- 迁移 `000018_gif`：新建 `gif_search_cache`，并在 `system_settings` 里种 6 个 `gif.*` 键
  （默认 `gif.provider=off`，没配密钥时面板明确提示「尚未配置」而不是空列表）。

### 安全维护

- 评论正文里的 GIF 标记 `[[gif:https://…]]` 服务端校验：只接受集成过的图源主机
  （`apihz.cn` / `klipy.com` 及其子域）、必须 https、无端口无凭据无 query、≤300 字符。
  外站地址与 http 一律 400。**为什么卡这么死**：正文会被渲染成 `<img>`，放开任意 https
  等于给评论一个嵌第三方图片（追踪像素）的口子。
- 上游返回 http 的图片地址时自动升级成 https（页面是 https，http 图会被浏览器拦掉）；
  上游报错不写缓存，避免把一次失败记成一天内的「结论」。

### 修复

- 后台接口的中文错误说明被前端技术词过滤连坐：`admin: invalid input: 选择 API 盒子需同时填写…`
  里的 `invalid` 命中过滤规则，界面只剩「请求失败，请稍后再试」。现在先剥掉 `admin: …:` 英文前缀
  再判可读性，缺密钥、邮箱已存在这类提示能正常显示。

### 改进

- 表情面板布局从三行 grid 改成纵向 flex：GIF 页要顶掉分类条的位置，grid 的行模板会留下空轨。
- 评论管理列表与通知摘要把 GIF 标记转成 `[GIF]`，运营和收通知的人不会看到一串 URL。
- 新增回归脚本 `.tools/headless-test/m54-gif.js`（16 项：页签切换、未配置提示、正文渲染）
  与 `m55-admin-flags.js`（13 项：导航、表单、缺密钥拦截、保存）。

## [0.17.0] - 2026-11-13

### 新增

- 后台账号改为「邮箱 + 密码」登录，取消用户名（与前台账号同一口径）。**不兼容**：
  - 迁移 `000017_admin_email`：`admin_users` 新增 `email TEXT NOT NULL`、删除 `username`
    （用户名与邮箱不是一一对应，无法无损还原，**不可逆**，回滚脚本只能按 `a_<id>` 重新生成用户名）；
    唯一索引建在 `lower(email)` 上，避免 `Admin@x.com` 与 `admin@x.com` 各占一行。
    存量 4 个 seed 账号补占位邮箱 `yj_<role>@admin.local`，否则丢掉用户名后会永久锁死。
  - `POST /api/v1/admin/auth/login` 请求体 `username` → `email`（旧字段会因未知字段直接 400）；
    登录查询走 `auth.NormalizeEmail` + `lower(email)`，大小写不敏感。
  - `POST /api/v1/admin/accounts` 建号改收 `email`，格式校验复用新增的 `auth.ValidEmail`
    （此前 admin 包自带一套 `^[a-z][a-z0-9_]{2,31}$` 规则，与前台不一致，已删除）。
  - `GET /api/v1/admin/accounts` 等接口的 `username` 字段统一改为 `email`；
    审计列表 `admin_audit_logs` 关联出的 `username` 改为 `admin_email`。
- 会话与身份：`sessionRecord` / `Principal` / TOTP 的 `account name` 都改用邮箱，
  后台顶栏、改密对话框显示的也是邮箱。**升级后旧后台会话里的身份字段为空，需要重新登录一次**。
- `cmd/seed`：开发用后台账号改为 `yj_<role>@admin.local`，`ON CONFLICT DO NOTHING`
  不再绑定 `username` 列。

### 修复

- 后台密码强度校验原来只挡住「密码 = 用户名」，换成邮箱后把**邮箱本身**和 `@` 前面的
  **本地部分**都纳入比较，避免 `ops@x.com` / `ops` 直接当密码（服务端与前端同规则）。

### 改进

- 后台 UI 全部换字段：登录页「管理员账号」→「管理员邮箱」（`type="email"`、`autocomplete="username"`）、
  账号管理列表列名「账号」→「邮箱」、建号弹窗「登录邮箱」、重置密码与启用/停用确认文案、
  审计日志列「管理员」→「管理员邮箱」、建号成功提示改为「管理员 <邮箱> 已创建」。

## [0.16.2] - 2026-11-13

### 改进

- 底部 GIF 徽标尺寸回调：0.16.1 把它算成和圆脸等大（都是 19.5px 圆环），按要求改成
  「比表情图标小一点」，一度缩到 28px（圆环 17px）偏小，最终定为 **30px**——
  圆环 18.3px 对圆脸 19.5px，小一档但不显小。`m52` 断言同步为「差 1~3px」，
  以后再动尺寸跑一下就知道有没有破坏这个关系。

## [0.16.1] - 2026-11-13

### 改进

- 表情面板底部的 GIF 徽标换成设计给的图形：原来是 CSS 画的描边圆 + `GIF` 文字，
  现在内联那份 36 格、`evenodd` 填充的复合字形（圆环 + 三个字母一体）。
  `ImIcon` 只吃 24 格单路径线性图标，放不进去，所以在 `EmojiPanel.vue` 里内联并注释了原因。
- 颜色跟随主题：原稿是 `#2F3035 / .7`（给浅色底用的），深色面板上几乎看不见，
  改为 `fill="currentColor"`，由按钮的 `--vp-im-text-secondary` 决定，仍保持「比表情弱一档」。
- 尺寸对齐：GIF 图标渲染 32px（外环半径 11/36 → 视觉圆环 19.5px），与左侧 26px 圆脸图标的
  圆环（18/24 → 19.5px）直径一致；底部格子 30 → 32px。

## [0.16.0] - 2026-11-13

### 新增

- 表情面板底部加类型选择器（参考图：一个圆脸 + 一个 GIF 圆标）：左边「表情」是当前类型，
  用品牌强调色 `--vp-im-accent`（#ff2e7e）画圆脸图标、`aria-pressed="true"`；右边「GIF」是
  描边圆 + 三个字母，尺寸与圆脸一致（30×30、2px 描边、9px 粗体）。
- GIF **仅做样式、先不实现功能**：`disabled` + `title="GIF 即将上线"` +
  `aria-label="GIF（即将上线）"` + 透明度 0.6 + `cursor: not-allowed`，与「@ 提及」占位入口
  同一套处理，不做成点了没反应的按钮。
- 面板结构改为三行栅格（分类条 / 表情栅格 / 底部选择器），`max-height` 264 → 300px，
  栅格加 `min-height: 0` 才不会被底部条压死、仍可滚动。

## [0.15.4] - 2026-11-13

### 修复

- 移动端表情面板左侧溢出屏幕、右侧空一大块：面板是 340px 的卡片挂在 `.pickers`（表情按钮）
  左边，390px 屏上实测 `left=-20`、右边界 322（右侧空 68px），「常见」被切掉一半。
  改法：`CommentSheet.vue` 里 `.composer` 加 `position: relative`，窄屏（≤560px）把
  `.composer .pickers` 降为 `static`，面板的定位基准从「表情按钮」变成「整条输入区」；
  `EmojiPanel.vue` 对应加媒体查询 `left: 0; right: 0; width: auto`，横向铺满整屏
  （像键盘一样贴两条边），360/390/430 三档实测面板左右边界与视口完全重合。
- 顺带把窄屏分类标签的左右内边距 10px → 8px：8 个两字分类在 360px 上正好排得下
  （内容 348px < 358px 可用宽），不用再横向滚动；320px 才需要滚。
- 窄屏表情图 26px → 28px，格子变宽后不至于显得稀。

## [0.15.3] - 2026-11-13

### 改进

- 表情分类条支持「鼠标悬浮 + 滚轮」横向滑动：8 个分类总宽 378px、面板只有 340px，
  第 8 个「符号」原来被硬裁掉一半，且滚动条隐藏、桌面鼠标又没法左右滚，等于点不到。
  `EmojiPanel.vue` 把滚轮的 `deltaY` 映射成 `scrollLeft`（`deltaMode` 为行时按 16px/行折算，
  Firefox 不会一格只挪 3px）；**只在真的滚动时才 `preventDefault`**，滚到头以后滚轮交还外层，
  不会出现「在面板上滚不动」的死区。
- 分类条两头加渐隐提示（左右各 22px，随 `scrollLeft` 切换）：被遮住的一侧有渐变，
  看起来是「还有更多」而不是被裁断；渐隐放在滚动容器外的定位层上，否则会跟着内容一起滚走。
- 点选分类时 `scrollIntoView({ inline: 'nearest' })`，把半遮的标签滚到完整可见。

## [0.15.2] - 2026-11-13

### 改进

- 评论表情从 Twemoji 扁平 SVG 换成 **Google Noto 3D 表情**（`参考资料/Noto-3D-Emoji-Fonts`）。
  面板图形 `apps/web/app/utils/emoji.ts` 改指 `/emoji3d/*.png`，面板格从 24px 调到 26px、
  评论正文表情从 18px 调到 20px（3D 渲染比线性图标需要更大显示尺寸），基线偏移同步 -5px。
- **只导出常用表情，不整套搬**：字体里有 1400+ 个表情（含大量冷门符号与旗帜），
  导出范围完全由 `apps/web/app/data/emoji.json` 决定——仍是原来那份 8 分类清单，
  共 225 个；整套 `public/emoji3d` 合计 1.3MB（225 个 64×64 PNG）。
- **组合表情从清单移除**：这套字体没有 `😮‍💨`、`❤️‍🔥`、`❤️‍🩹` 的 ZWJ 字形（GSUB 里也没有对应连字，
  只有 2 条连字记录），塞进去只会渲染成第一段的图，所以直接从清单删掉，
  它们回落到系统字体。清单 228 → 225。
- 变体选择符 `FE0F` 的处理向下兼容：`❤️` 与 `❤` 仍命中同一张 `2764.png`。

### 重构

- `scripts/sync-emoji.mjs`（复制 Twemoji SVG）替换为 `scripts/sync-emoji3d.mjs`：
  素材是字体不是散图，脚本直接解析 TTF——cmap（格式 4 + 12）拿码点→字形号，
  sbix 拿字形→内嵌 PNG，零依赖导出。`npm run emoji:sync` 指向新脚本，
  支持 `--check`（只校验，CI 用）与 `--ppem=82`（换位图档位，默认取 ≥55px 的最小 strike）。
  同时删掉 `apps/web/public/emoji/`（229 个 SVG）与旧脚本。
- 许可随资源分发：字体是 SIL OFL 1.1，脚本会把 `LICENSE` 复制到 `public/emoji3d/`，
  README「评论表情」章节同步改写（素材来源、只导常用的原因、组合表情为何不支持）。

## [0.15.1] - 2026-11-13

### 修复

- 评论抽屉里作者名显示成一串 6 位数字（如 `000034`），头像是个圆角方块里的「0」。
  根因：`GET /api/v1/contents/{id}/comments` 与 `.../comments/{commentID}/replies` 只查 `users.handle`，
  前端把 handle 同时当昵称和头像首字；夜街号改成 6 位数字后（0.13.0）handle 已经完全不像昵称了。
  改法：两个接口补 `display_name`（取 `COALESCE(NULLIF(btrim(display_name), ''), handle)`）和
  `avatar_url`（`users.avatar_key` 经 `store.MediaURL` 换成可访问地址，对象键只走 `json:"-"` 的私有字段不外泄）。
- `POST /api/v1/contents/{id}/comments` 的返回值同样带上作者昵称与头像：乐观插入的「我」占位条目被服务端结果替换后，
  不会再闪回夜街号；S3 未注入或签名失败时头像留空，评论本身照常返回。
- 「回复 @某人」标识显示夜街号：`listReplies` 的 `reply_to` 改为被回复人的昵称（`COALESCE(rt.display_name, rt.handle)`），
  前端 `startReply()` 改成接收整条评论对象，输入框上方 chip 与占位文案一并改用昵称。
- 创作者中心「评论管理」的作者列只显示 handle，改为昵称：`GET /api/v1/me/comments` 补 `display_name`。
- 评论头像渲染：`CommentSheet.vue` 有头像时用 `<img>` 铺满 34px / 26px 圆形（`object-fit: cover`），
  没有头像时不显示空白方块，回退到默认用户图标，并按 `user_id` 生成固定色相底色，让不同评论者能区分开。

### 改进

- 新增回归脚本 `.tools/headless-test/m48-comments.js`：校验昵称不是 6 位夜街号、真实头像可加载、
  头像仍为圆形、回复标识用昵称；另用请求拦截抹掉 `avatar_url` 验证无头像回退（不改数据库）。当前 9 项全过。

## [0.15.0] - 2026-11-13

### 新增

- 评论表情面板：输入框右侧的表情按钮打开 **Twemoji 14.0.2** 面板，8 个分类
  （常见 / 情绪 / 手势 / 爱心 / 动物 / 食物 / 活动 / 符号）共 228 个表情。
  选中后按**光标位置**插入输入框并恢复光标，不是一律追加到末尾。
- 评论正文渲染表情：`splitEmoji()` 把文字与表情切开，清单内的表情渲染成同源
  `<img src="/emoji/xxx.svg">`（18px、基线对齐），清单外的表情回落系统字体，不丢字符。
  变体选择符 `FE0F` 归一化，`❤` 与 `❤️` 命中同一张图。
- `scripts/sync-emoji.mjs` + `apps/web/app/data/emoji.json`：表情清单是唯一事实来源，
  `npm run emoji:sync` 从 `参考资料/twemoji-14.0.2` 复制对应 SVG 到 `apps/web/public/emoji/`
  并校验缺失（`--check` 可用于 CI）。当前资源 229 个文件 / 829KB。
- 面板交互：点面板外或发送后收起；`aria-expanded` 标记展开态，`aria-selected` 标记当前分类。

### 改进

- **为什么自托管图形**：前端 CSP 是 `img-src 'self' data: blob:`，直接引 CDN 的 SVG 会被拦；
  自托管也不受第三方可用性影响。`public/emoji/LICENSE-GRAPHICS` 随资源分发，
  README 增加「评论表情」章节写明来源、命名规则与扩充方式。
- `.gitignore` 增加 `/参考资料/`：Twemoji 完整包 16MB、3689 个 SVG，只入库清单用到的子集。
- `@` 提及入口维持占位（`disabled` + 「即将上线」），本轮只做表情。

## [0.14.3] - 2026-11-13

### 新增

- 评论输入框右侧新增「@ 提及」与「表情」两个入口，位置与顺序对齐设计稿（输入框在左，
  两个图标在右、贴发送按钮）。`ImIcon` 补 `at` 字形（内圈 + 外弧），与 `face` 同线宽。
- 两个入口当前是**占位**：`disabled` + `title`「即将上线」+ `aria-label` 说明，
  视觉上比输入框弱一档。按需求先不实现功能——不做成「能点但无反应」，
  否则会被当成坏了。
- 在 `CommentSheet` 里留了注释，下一轮实现时直接替换 `disabled` 与 `@click` 即可。

### 改进

- 窄屏适配：390px 下输入框仍有 226px 宽，`@`、笑脸、发送按钮各自 32/32/44px，
  不换行、无横向溢出。

## [0.14.2] - 2026-11-13

### 改进

- 「夜街号」文案统一加中文冒号：「我」页、创作者页、设置页、发布页侧栏都从
  「夜街号 000347」改为「夜街号：000347」；创作者页的 SEO description 与
  og:description 同步。数字号后面直接接文字时，没有冒号会读成一句话。

### 修复

- 本地 `dev.mjs` 在 watch 重建后把 API 停掉却起不来（现象：`/api/v1/*` 全部连接失败、
  创作者页 404、`status` 显示 api 未运行，日志只剩一行「启动 api」没有后续输出）。
  根因有两层：`stop()` 用 `taskkill /F` 后立即返回，Windows 上 exe 句柄尚未释放，
  紧接着 `spawn` 同一个二进制会 `EPERM`；而 `spawn` 的失败是异步 `emit('error')`，
  原实现把它当同步 throw 处理，错误没被捕获，服务就停在「已停止」状态。
  改法：`stop()` 轮询等进程真正消失（最长 3s）；新增 `spawnService()` 用 `'spawn'` /
  `'error'` 事件判断是否真的拉起，遇到 `EPERM` 退避重试 3 次；watch 巡检改为逐个服务
  try/catch，一个重启失败不再中断另一个，也不结束监听。

## [0.14.1] - 2026-11-13

### 改进

- 夜街号补零到 6 位：第 1 个用户是 `000001`，`000002` 依次递增；第 1000000 个用户起
  按真实位数展示（`1000000`，不再补零）。注册时的 `handle` 与默认昵称一起生成，
  即「用户000346 / 夜街号 000346」。
- 新增 `internal/platform/nightid`：`Format(id)` 与 `Normalize(输入)` 集中补零规则，
  登录、创作者页、关注、评论 @提及 都走同一个函数。**故意不用 Postgres 的 `lpad` 做匹配**
  ——`lpad('1000000', 6, '0')` 会截成 `100000`，用它比较会把 7 位号误判成别的用户；
  `Normalize` 对超过 6 位或含非数字的输入原样返回。
- 兼容旧输入：`/creator/34` 与 `/creator/000034` 指向同一人，用 34 或 000034 都能登录；
  创作者页即使从旧链接进入，也显示接口返回的 `000034`，不会把号写成 34。
- 迁移 `000016_padded_handle`：存量 `handle` 与「默认昵称」一起补零，用户自己改过的昵称
  （如「我是重置」）保持不动。

## [0.14.0] - 2026-11-13

### 新增

- **登录 / 注册改为「邮箱 + 密码」**：弹窗、`/login` 页、找回密码页都不再要求填账号；
  注册表单只有邮箱与密码两栏。邮箱统一小写存储与查询，`A@x.com` 注册、`a@x.com` 登录可命中。
- **夜街号 = 用户 ID**：注册时用序列值直接生成 `handle`（纯数字）与默认昵称
  「用户 + 夜街号」，不再让用户起英文账号。新用户注册后「我」页显示「用户343 / 夜街号 343」。
- 沉浸流作者名改为显示昵称（`feed.Author` 新增 `display_name`）：夜街号是纯数字后，
  `@340` 认不出是谁，而昵称可被用户在设置页修改，不能用前端拼「用户+号」代替。

### 重构

- `auth.Repository.CreateUser` 改用一个 CTE 取 `nextval('users_id_seq')` 再
  `OVERRIDING SYSTEM VALUE` 插入：`id`、`handle`、`display_name` 在同一语句里落定，
  避免「先插临时 handle 再回写」的两次往返与唯一索引冲突。
- `auth.Service.Login` 按 `Account` 查询：先匹配邮箱，未命中回落到 handle，
  保留纯数字夜街号登录（后台脚本、curl、既有 e2e 不必立即改）。
- 找回密码两阶段都改成只按邮箱查（`LookupResetTarget(email)`），删掉 `trimHandle`
  与 `handlePattern`；响应文案改为「如果这个邮箱已注册，验证码邮件已发出」。

### 改进

- 资料类位置统一显示「夜街号 N」（「我」页、创作者页、设置页、发布页），
  去掉 `@` 前缀——数字号不是用户名。创作者页标题与 SEO 描述同步。
- `internal/modules/auth` 注册校验用例改为邮箱维度（缺邮箱 / 非法邮箱 / 弱密码）。
- 迁移 `000015_numeric_handle`：存量英文 handle 统一改成 `id` 的字符串形式，
  `display_name` 为空的补成「用户 + 夜街号」，没有邮箱的早期调试账号补占位邮箱
  （否则登录只认邮箱后该账号永久无法登录）。

### 不兼容

- `POST /api/v1/auth/register` 只接受 `{email, password}`，`handle` 字段被忽略；
  `POST /api/v1/auth/login` 以 `email` 为准（仍兼容 `account` / `handle` 字段）。
- 用户 `handle` 变为纯数字：`/creator/{handle}` 链接、关注接口、所有返回作者标识的
  接口（`author.handle`、`author_handle`）里的值都是数字，不再是可读英文名。
- 存量账号的英文 handle **已不可恢复**（迁移覆盖原值，备份见 `data/backup/`，不入库）。

## [0.13.0] - 2026-11-13

### 新增

- 消息中心（`/messages`）与「我」（`/me`）显示底部主导航：此前底栏只在沉浸流 `/` 与
  视频 / 图集详情页出现，用户从这两页要么按浏览器后退、要么绕回首页才能切走。现在
  首页 / 发现 / 发布 / 消息 / 我的 五个入口在这两页也常驻。
- 底栏从 `ChromeBars` 抽成独立组件 `BottomNav.vue`，`ChromeBars` 只保留沉浸流的
  「关注 / 推荐」频道栏。抽出后普通工具页复用同一个底栏，不必再带一个用不上的频道切换。

### 改进

- 底栏按当前路由高亮，不再写死「首页」：`/messages` 亮「消息」，`/me` 亮「我的」，
  `/me/relations`、`/me/drafts` 等子页仍归属「我的」；`/search`、`/category`、`/topic`
  都归到「发现」。同时补 `aria-current="page"`，读屏能播报当前位置。
- 消息中心底部留白 56px → `calc(84px + env(safe-area-inset-bottom))`：底栏高 61px，
  原留白会让最后一条通知被底栏压住（「我」页原本就是 84px，未改）。
- 两页底栏与沉浸流共用同一套收起逻辑（`collapsed`），普通页不传该值，因此常驻显示；
  PC 与移动端表现一致，无横向溢出。

## [0.12.1] - 2026-11-13

### 修复

- 后台「用户管理」操作列在窄屏换行后，「头像」比上一行的「警告」右移 12px，看起来没有左对齐。
  根因：Element Plus 给相邻按钮加了 `.el-button + .el-button { margin-left: 12px }`，
  它和容器的 `gap` 叠加，且换行不会归零——第二行第一个按钮仍然带着 12px 左边距。
  改法：`.row-actions :deep(.el-button + .el-button) { margin-left: 0 }`，
  按钮间距统一由 `gap: 8px` 控制（水平与换行后的行间距一致）。
  实测 900px：两行首按钮 left 均为 654；760px：均为 526。

## [0.12.0] - 2026-11-13

### 新增

- **随机头像 6 选 1**：前台「设置 → 头像」与后台「用户管理」都能打开一个 6 张
  DiceBear Micah 随机头像的选择器，可「换一批」再挑，选定后下载到自有对象存储并设为头像。
  前台给自己换，后台给指定用户换，两处共用同一个 `AvatarPicker` 组件与同一份写入逻辑。
- `GET /api/v1/avatar/preview?seed=…`：同源图片代理。前端 CSP 只允许 `'self'` 与自有媒体源，
  直接 `<img src="https://api.dicebear.com/...">` 会被拦，所以由后端按 seed 回一张 PNG；
  只接受本系统生成的 24 位 hex seed，非法参数返回 400。
- `POST /api/v1/me/avatar/random` 与 `POST /api/v1/admin/users/{id}/avatar`：
  提交选中的 seed，下载 / 存对象 / 写 `avatar_key` / 删旧图，返回新的签名地址。
  后台端点走 `users:sanction` 权限与 CSRF 校验。

### 重构

- 把 DiceBear seed 生成、PNG 下载校验、对象 key 命名抽到 `internal/platform/avatar`，
  注册默认头像、前台随机头像、后台随机头像三处共用，去掉重复的 HTTP 与校验代码。
- `settings.Handler.SetRandomAvatar` 作为唯一写入实现，由 `cmd/api/main.go` 通过
  `admin.Handler.SetAvatarSetter` 注入后台；后台模块不需要自己持有对象存储与写库逻辑。

### 改进

- 「换一批」会清空当前选择，确认按钮回到禁用态，避免拿旧 seed 保存新一批里的其它头像。
- 保存失败（S3 不可用、seed 不合法）不会留下无人引用的对象；覆盖头像时删掉旧对象。

## [0.11.0] - 2026-11-13

### 新增

- 新用户注册成功后自动分配一张 **DiceBear Micah 随机头像**：以 `crypto/rand` 生成 seed，
  向 `https://api.dicebear.com/10.x/micah/png?seed=…&size=256` 取 256px PNG，
  **下载后存入项目自己的对象存储**（`avatars/{user_id}/{ts}.png`），再把 key 写进
  `users.avatar_key`。之后与用户自己上传的头像走完全相同的读取路径，页面渲染不依赖
  dicebear.com 是否在线。
- `internal/modules/auth/default_avatar.go`：`DefaultAvatar` 组件，最小存储接口
  `AvatarObjectStore`（PutBytes / Remove），由 `cmd/api/main.go` 在 S3 可用时注入；
  HTTP 客户端 8s 超时、响应校验状态码 / Content-Type / 256KiB 上限。
- `auth.Repository.SetDefaultAvatarKey`：带 `avatar_key IS NULL` 条件，异步任务不会覆盖
  用户在它完成前自己上传的头像；写库失败或未写入时删掉刚存的对象，不留孤儿。

### 改进

- 注册链路不被外部服务拖累：头像分配在注册响应之后用独立 `context.WithTimeout` 异步执行，
  任何失败只记 WARN，注册照常 201；S3 不可用时组件不注入，前端回落占位头像。
- 只对**新注册用户**生效，存量用户与 seed 数据保持原状。
- 合规：Micah 是 Micah Lanier《Avatar Illustration System》的 CC BY 4.0 remix，
  设置页头像区加了带原作、许可证与 DiceBear 链接的署名，README 新增「默认头像」章节
  说明实现、失败策略与署名。

## [0.10.0] - 2026-11-13

### 新增

- 首页沉浸流与视频详情页的「更多操作」菜单新增**晚点再看**（排在举报之前），
  点击把当前视频放进晚点再看。底层复用已有的 `watchlist` 表与
  `POST /api/v1/contents/{id}/watchlist` 幂等 toggle 接口，不新建表、不改后端。
- 菜单项会回显状态：已加入时显示「已在晚点再看」（图标换成对勾）并可再次点击移出，
  避免 toggle 按钮永远写着「加入」、第二次点击却悄悄移出。首页在切条时
  `GET /api/v1/contents/{id}/watchlist` 查初始状态，用 activation ticket 防竞态；
  刷新后状态仍正确。
- 只有视频条目显示该入口，图集条目的菜单仍只有「举报」。
- 未登录点击直接唤起登录弹窗（说明「登录后即可把视频放进晚点再看」），
  与点赞 / 收藏的引导口径一致。
- `ImIcon` 新增 `clock` 图标。

### 改进

- 全站文案统一「稍后再看」→「晚点再看」（个人库分区、「我」页分区、空态、详情页按钮、
  成功提示），避免同一个功能在菜单里叫「晚点再看」、在个人库里叫「稍后再看」。
  个人库空态改成「白天存下来晚上慢慢看」，对应「晚间再看」的产品定位。
- 详情页原有的独立「晚点再看」按钮保留，与菜单入口共用同一份状态。

## [0.9.1] - 2026-11-13

### 改进

- 未登录点赞 / 收藏不再只弹一句「登录后才能点赞」的 toast，而是直接唤起登录弹窗，
  并在标题下说明原因（「登录后即可点赞」「登录后即可收藏」）。用户在沉浸流或视频详情页
  正要点互动时就被带到登录入口，不用自己再找。登录成功后原地刷新，回到同一条内容。
- 首页「关注作者」的未登录拦截也换成弹窗（`reason` 为「登录后即可关注作者」）：
  同一个关注动作此前在创作者页弹窗、在首页只 toast，体验割裂。
- 已登录路径不变：实测点赞数 1 → 2、`aria-pressed=true`、无弹窗。

## [0.9.0] - 2026-11-13

### 新增

- 全站登录 / 注册弹窗：前台 13 处入口（`/me`、`/me/relations`、`/me/drafts`、`/library`、`/messages`、
  `/settings`、`/studio`、`/search`、`/publish`、创作者页关注拦截、举报弹窗、找回密码完成页、
  改密后重新登录）不再跳 `/login` 独立页，改为在当前页唤起 `VpAuthDialog`，登录成功后
  按 `redirect` 导航或原地重载数据。
- `useAuthDialog`：`useState` 存弹窗状态（开关 / 登录或注册 / 说明文案 / 成功后去向 / 所在表面），
  跨组件共享、SSR 每请求独立。
- `VpDialog` 新增 `initialFocus`：登录弹窗打开即聚焦账号输入框，不用再点一次。

### 改进

- 弹窗跟随所在页面的表面类型：弹窗挂在 `app.vue`、在 `[data-surface]` 容器之外，
  原先只能拿到 `:root` 的深色令牌，浅色工具页上会突兀地弹一个深色框。
  现在 `open()` 时读取当前 `[data-surface]` 并透传到弹窗层——`/settings` 走白底深字，
  `/me`、`/library` 这类沉浸页保持深色。
- 顺手清掉三处面向用户的开发术语：「会话使用 HttpOnly Cookie · 写操作需 CSRF 双提交校验」
  →「登录状态仅保存在本机，退出后即失效」；「验证码只存服务端缓存 · 邮件经队列异步投递」
  →「验证码 10 分钟内有效，请勿转发给他人」（对齐后端 `captchaTTL = 10min`）；
  发布页「上传与发布需要会话及 CSRF 校验」→「登录后即可上传、转码并发布作品」。

### 修复

- 举报弹窗内点「登录」时，举报弹窗不关闭、与登录弹窗叠在一起（两个 `VpDialog` 都是 z-index 60，
  后开的那个把前一个盖住一半）。现在先 `emit('close')` 收起举报弹窗再唤起登录。

### 说明

- `/login` 独立页保留：邮件、书签、无 JS 场景仍可直接访问；前台导航已全部改为弹窗。

## [0.8.0] - 2026-11-13

### 新增

- 沉浸流媒体实例池（《前端工程架构规范》§8.2 落地）：默认**向下预载 3 条、向上保留 5 条**，
  窗口内每条一个 `<video>` 实例与独立 HLS 缓冲，超出窗口立即释放（destroy HLS、revoke Blob、
  卸载 src、移除元素）。此前只有封面图预载，视频切条即 `clearSource()` 清空，回滑必然从零拉流。
- 回滑续播：切走时记住 `currentTime` 并 `hls.stopLoad()`，缓冲原样留在池里；滑回时
  `hls.startLoad(记住的位置)` 并从该位置继续播。实测 190 这条滑走停在 9.24s、缓冲 27.4s，
  回滑后从 11.48s 继续、缓冲未重新下载。
- 预载并发上限 2 条，避免和当前条目抢带宽；预载实例只缓冲头部 6 秒（`maxBufferLength`），
  播放态才放开到 12/24 秒并允许 30 秒后向缓冲。
- 按网络状况自动收缩窗口：`navigator.connection.saveData` 或 2G → 下 0 / 上 1，
  3G → 下 1 / 上 2，其余按满窗口。省流模式下实测池里始终只保留上一条。

### 重构

- `useImmersivePlayer` 从「全局单例 + 切条时清源」改为**有上限的实例池**。这是对
  《前端工程架构规范》§8.1「播放器单例复用」的有意修订：规范只要求 ±1 窗口，单例够用；
  窗口放大到下 3 / 上 5 后单例无法同时缓冲多条。仍然不是「每条内容一个长期实例」——
  池大小由窗口决定，同一时刻只有 active 条目播放与发声。
- `attach()` 语义改为「记录目标媒体槽」，真正的 `<video>` 搬运放到 `load()` 里：
  调用方总是 `attach(slot) → load(item)`，而 attach 时 activeSlug 还是上一条，
  提前搬运会把旧画面塞进新条目的槽。
- 图集条目不再创建空 `<video>` 实例，直接归还池位。

### 改进

- 封面预载跟随窗口，但只铺到 ±2：渲染窗口只有 ±1，按视频窗口（±3/±5）下载封面纯属白流量。

## [0.7.16] - 2026-11-13

### 改进

- 底栏收起时左右两侧组件跟着下移，不再留 61px 空白：信息层与操作栏原先固定在 126px
  （移动端 78 / 74），底栏一收起就悬在半空。现在 `ImmersiveItem` 在 `.item.nav-hidden` 下把
  `--vp-im-action-rail-offset-bottom` 与新增的 `--vp-im-info-offset-bottom` 一起改为 65px
  （控制条贴底高 51 + 14px 间距），两侧加 `transition: bottom 200ms` 与控制条/底栏同步；
  底栏出现时仍回到 126px。规则写在 `min-width: 781px`，移动端媒体查询不匹配，保持 78 / 74。

## [0.7.15] - 2026-11-13

### 改进

- 沉浸页信息层移除「♪ 作者 · 原声」胶囊标签：作品目前没有真实音轨字段，这个胶囊只是把
  作者名重复了一遍，占视觉空间还容易被误认为可点的音乐入口。作者名、文案、话题标签保持不变。

## [0.7.14] - 2026-11-13

### 修复

- 滑动切换作品时底栏「一上一下」闪：切条瞬间旧视频触发 `pause` 把 `playing` 置 false（底栏弹出），
  新视频加载完 `play` 又置 true（底栏收起）。现在加「切换窗口」——`current` 变化时用
  `flush: 'sync'` 同步强制收起并锁住，直到新条目 `ready` / `failed`（或 3s 兜底）才交还给真实播放状态；
  展开另加 120ms 延迟，合并播放器瞬时抖动。详情页加载期同样先收起，不再「先显示再收起」。
  实测：暂停态滑动 opacity 1→0 一次；播放中连续滑 3 条全程 0；详情页加载全程 0；移动端始终 1。

## [0.7.13] - 2026-11-13

### 改进

- 图片内容（图集）在 PC 端也收起底栏：图片没有播放/暂停语义，默认收起保持沉浸，
  点画面唤出、再点收起；切换作品时回到收起状态。`ChromeBars` 的 `playing` 属性
  改为语义更准确的 `collapsed`，由页面按内容类型计算（视频跟随 `player.state.playing`，
  图片跟随唤出状态）。
- 图片控制条同样接收 `nav-hidden`，底栏收起时贴到屏幕底部；图集左右翻页按钮加
  `@click.stop`，点箭头不会误触底栏显隐。
- 移动端 ≤780px 不受影响：底栏常驻，图片控制条本就隐藏。

## [0.7.12] - 2026-11-13

### 修复

- 播放态控制条下方仍留 61px 空区（用户反馈「没有变化」）：上一轮只删掉了深色底衬，
  但控制条本身仍固定在 `bottom: 61px`（原底栏顶边），底栏收起后那 61px 变成视频暗部，
  视觉上依旧像一条空白带。根因是**控制条位置没有随底栏收起而下移**。
  改法：PC 播放态给控制条加 `bottom: 0`，并用 `transition: bottom 200ms` 与底栏收起同步；
  暂停时回到 61px 贴住底栏顶边。移动端控制条本就隐藏，媒体查询不匹配，行为不变。

## [0.7.11] - 2026-11-13

### 改进

- 播放态撤掉控制条下方那条 61px 深色底衬（上一轮为避免「控制条悬空」加的
  `.player-bar--nav-hidden::after`）：PC 端底栏收起后，控制条下方直接透出视频画面，
  不再留一条没有内容的空白带。暂停时底栏出现、控制条仍贴着底栏顶边，两者合成一条；
  移动端控制条本就隐藏，行为不变。

## [0.7.10] - 2026-11-13

### 改进

- PC 端（≥781px）视频播放时底部主导航自动收起，暂停后恢复（200ms 过渡；隐藏时同步
  `visibility` 与 `pointer-events`，键盘不会 Tab 到看不见的链接）。`ChromeBars` 新增
  `playing` 属性，首页 `/` 与深链接详情 `/video/*` 都传 `player.state.playing`；
  隐藏规则写在 `min-width: 781px` 媒体查询里，移动端 ≤780px 底栏仍常驻，行为不变。
- 配套视觉：播放态给 `.player-bar` 加向下延伸的同色背景，补齐底栏收起后控制条与
  屏幕底部之间的 61px，避免通栏控制条看起来悬空；仅 PC 生效，移动端控制条本就隐藏。

## [0.7.9] - 2026-11-13

### 修复

- 界面把开发术语直接展示给用户：登录态失效时提示「无法获取CSRF令牌，请重新登录（csrf_unavailable）」。
  根因有两层——`ensureCsrf()` 的文案本身含「CSRF 令牌」；`errorMessage()` 还给**所有**错误统一拼上
  `（错误码）`，26 个调用点都会带英文码。改法：`csrf_unavailable` 固定为「请重新登录」；
  `errorMessage()` 改成用户可读文案层（按错误码映射中文、保留后端中文可读文案、
  英文/技术词一律兜底成「操作未完成，请稍后再试」）；`HTTP ${status}` 兜底改中文；
  `useAdminApi` 也去掉请求失败时的 `（HTTP xxx）` 并净化后端英文 message。
  错误码仍保留在 `ApiError.code` 供逻辑判断与日志，只是不再进界面。

### 新增

- `apps/web/tests/useApi.test.ts`：5 条回归，断言任何错误提示都不含错误码括号、
  CSRF/HTTP 等技术词，且已知错误码映射到中文人话。

## [0.7.8] - 2026-11-12

### 改进

- 搜索页（`/search`）按用户决定回退「与搜索框对齐」的 44px 缩进（0.7.2/0.7.7）：左对齐内容
  （筛选行、「共 N 条结果」、卡片网格、搜索历史 / 热门搜索）恢复与返回按钮同一起点
  （容器左缘），空态 / 登录门 / 「加载更多」保持视口居中；0.7.4 的历史/热门 28px 间隔保留。
  直接诱因是 0.7.7 的子块级缩进被 `.hot ul` 自己的 `padding: 0` 简写覆盖（层叠后者胜），
  出现「标题缩进、词条不缩进」的错乱——靠子块 padding 拼对齐太脆，整体放弃。
  新增 `m46-empty-center.js` 16 项断言（543/1440 双宽度 × 对齐/居中/间隔）。

## [0.7.7] - 2026-11-12

### 修复

- 搜索页（`/search`）空态 / 登录门 / 「加载更多」在视口里不居中（移动端明显，实测右偏 22–33px）：
  0.7.2 的对齐缩进加在了 `.results / .gate` 等整个容器上，居中块被限制在「输入框列」（[44px, 右缘]）
  内居中，相对视口恒右偏 22px。改法：缩进改为只给左对齐的块（`.filters / .notice / .hot h2 /
  .hot ul / .summary / .grid / .hint`），居中块不吃缩进、回归视口居中；「共 N 条结果」与卡片网格
  同输入框对齐的关系不变。新增 `m46-empty-center.js` 在 543 / 390 / 1440 三个宽度实测：
  空态标题与按钮组视口居中偏差 ≤2px、登录门标题居中，且摘要 / 筛选行对齐关系保持。

## [0.7.6] - 2026-11-12

### 修复

- 消息页（`/messages`）通知操作钮的「⋯」不在按钮圆心（水平偏右约 3px）：0.7.5 把操作钮改小后
  暴露——`.ops` 没有重置浏览器给 `<button>` 的默认 `padding: 1px 6px`，border-box 24px 被左右
  各 6px 的 padding 挤成 12px 内容盒，18px 图标装不下、溢出并被顶到右 padding 缘（水平 +3px、
  垂直因上下 padding 只有 1px 凑巧居中，故此前只测垂直未发现）。改法：`.ops` 补 `padding: 0`
  （ActionRail 的 `.follow` 同坑，当时已留注释）。实测图标与按钮圆心两轴偏差 0px
  （24px 截图逐像素：dots 中心 11.5,11.5 == 圆心 11.5,11.5）。

## [0.7.5] - 2026-11-12

### 修复

- 消息页（`/messages`）通知卡片的时间与操作钮错位：现象是「刚刚」和右侧「⋯」不在一条水平线上
  （⋯ 比时间高约 5px，且两者右缘相距一个按钮位）。根因是操作钮是 `<li>` 的独立 flex 项
  （30px 高、`align-items: flex-start` 顶在卡片顶部），而时间在 `.main` 的标题行里（行高约 17px），
  两者各自对齐没有参照。改法：操作钮移进标题行，与时间同处一条 `align-items: center` 的 flex 行，
  按钮盒缩到 24px 并用 `margin-block: -5px` 抵消撑高，标题行行高节奏不变；`@click.stop`
  防止点操作钮冒泡成「标记已读」。登录临时账号实测时间与 ⋯ 图标垂直中心差 0px。

## [0.7.4] - 2026-11-12

### 改进

- 搜索页（`/search`）「搜索历史」与「热门搜索」两块之间增加 28px 纵向间隔
  （`.hot + .hot`，只作用于相邻堆叠的两块，不影响其它布局）；
  登录态实测间隔 28px。

## [0.7.3] - 2026-11-12

### 改进

- 搜索页（`/search`）筛选行去掉「分类 / 类型 / 时长」前缀文字：与下拉值「全部分类 / 全部时长」
  语义重复，只留三个胶囊下拉；读屏用户走原有的 `aria-label`（按分类筛选 / 按内容类型筛选 /
  按时长筛选），与搜索输入框的左缘对齐关系不变（实测差 0px）。
- 搜索页删除「历史仅保存在本机浏览器（最多 10 条），不会上传。」说明行及其 `.privacy` 样式：
  搜索历史区只留标题、词条与「清空」按钮。

## [0.7.2] - 2026-11-12

### 修复

- 搜索页（`/search`）其余内容列与搜索框同样错位：现象是「共 N 条结果」、结果卡片网格、
  搜索历史 / 热门搜索、提示行都顶到容器左缘（返回按钮下方），与已对齐的筛选行不在一条线上。
  改法：把对齐规则提为 `.filters / .notice / .gate / .hot / .results / .hint` 共用的
  `box-sizing: border-box` + `padding-left: 44px`（让开返回按钮 34px + 间距 10px；
  `.gate` 用 `padding-block` 声明上下内边距，避免 shorthand 覆盖左内边距），
  整个内容列（含空态文案的居中基准）统一到搜索输入框左缘。登录临时账号实测
  「共 0 条结果」与输入框左缘差 0px。

## [0.7.1] - 2026-11-12

### 修复

- 搜索页（`/search`）筛选行与搜索框错位：现象是「分类 / 类型 / 时长」一行比搜索输入框更靠左
  （顶到返回按钮下方）。根因一是筛选行没有让开返回按钮的占位（34px + 10px 间距）；根因二是本页
  无全局 border-box reset，给 `.filters` 加 `padding-left` 后 1080px 变成内容宽、容器整体左漂，
  且返回按钮自身也是 content-box（34px 声明宽 + 2px 描边 = 36px 外径）。改法：`.filters` 显式
  `box-sizing: border-box` + `padding-left: 44px`，`.back` 改 border-box（34px 含描边外径）；
  实测「分类」标签与搜索输入框左缘差 0px。

## [0.7.0] - 2026-11-12

### 重构

- `/me` 按参考图重做为个人主页：渐变资料横幅 + 大圆头像 + 用户名/编辑入口 + 关注/粉丝/获赞计数 +
  夜街号 + 胶囊入口；分区标签改为下划线选中态，作品区改为 6 列竖版卡片网格
  （封面 + 左下角爱心数 + 标题，窄屏 5/4/3/2 列自适应），保留审核徽标与申诉入口。
  原纯文字列表与分散的计数/标签样式全部移除。

### 新增

- 「我」页补齐三个真实分区：`观看历史`、`稍后再看`（复用 `/api/v1/me/history|watchlist`），
  与 `喜欢`、`收藏` 一起按需加载；私密分区带锁图标。
- 作品区支持「批量管理」（勾选 + 删除所选，走 `/api/v1/contents/batch-delete`）与按标题/作者的本地搜索，
  搜索占位文字随分区变化。
- 后端 `content.CardMeta`：一次批量给 `/contents/mine`、`/me/likes`、`/me/favorites` 补
  `poster_url`（图集首图 / 视频 poster，按 driver 区分签名与 demo 路径）和 `like_count`，
  供卡片网格使用；查询失败只记日志，卡片回退类型占位。
- `ImIcon` 新增 `edit` 铅笔图标。

### 改进

- 卡片封面缺失时显示带类型图标的渐变占位，不再空一块；观看历史/稍后再看卡片显示续播进度条。

## [0.6.1] - 2026-11-12

### 修复

- 桌面播放控制条的弹幕输入框点击/键盘聚焦时出现一圈品牌粉描边，与深色控制条不协调（像表单报错）：
  根因是全局 `:focus-visible` 给 `input` 加 `--vp-im-focus-ring`，移动端 `.dm-sheet` 早已抑制，
  但桌面 `.pb-input` 漏了同一处理。现在 `.pb-input input:focus-visible` 去掉粉环，
  改由 `.pb-input:focus-within` 提亮边框与底色表达焦点（透明边框 + `box-sizing: border-box`，无 1px 位移）。

## [0.6.0] - 2026-11-11

### 新增

- 沉浸流（`/`）与深链接详情（`/video/*`、`/image/*`）的点赞 / 收藏真实落库：`ActionRail` 的爱心 / 星星按钮
  现在调用 `POST /api/v1/contents/{id}/reactions`（kind=like/favorite），写入服务端 `reactions` 表并给作者发
  互动通知；收藏后「我」页收藏 tab 真实出现该作品。feed 列表 / 详情接口下发真实互动计数
  （`stats.likes/favorites/comments` 实时查 reactions / comments，此前恒为 0）与当前查看者的
  `liked` / `favorited` 标记；查看者标记只在带登录态的 feed 接口下发，creator / topic 公开缓存页不带。

### 修复

- 「我」页（`/me`）喜欢 / 收藏 tab 永远为空：现象是点了收藏后列表仍是「还没有收藏的作品」；根因是前端
  收藏 / 点赞只写 localStorage（M2 乐观缓存），服务端 `reactions` 表没有任何记录，`GET /me/likes` /
  `GET /me/favorites` 查不到数据；改法是前端改调 `POST /api/v1/contents/{id}/reactions` 真实落库（见新增）。
  深链接详情页的爱心 / 星星刷新后不回亮：SSR 取数（Nitro 内部同源代理）不带查看者会话，接口看不到登录态；
  挂载后按会话补一次 `GET /api/v1/contents/{id}/reactions` 同步标记与计数（与稍后再看同口径）。

### 改进

- `useInteractions` 移除 M2 的 localStorage 乐观缓存：点赞 / 收藏 / 关注一律以服务端为准，内存只保留会话内
  的乐观状态与角标增减；匿名点击不再假点亮，改为提示「登录后才能点赞 / 收藏」（与关注按钮同口径）。

## [0.5.4] - 2026-11-11

### 修复

- `scripts/dev.mjs` 在 `--watch` 下遇到编译错误会抛异常退出，连带把 API / Worker / Web 一起停掉（实测触发：
  编辑中途源码短暂不完整）。现在改成**先编译、成功才停旧进程**：编译失败时保留正在跑的服务、打印错误、
  监听继续，修好后保存自动重试；`restart` 同样是编译不过就不动现有进程。

## [0.5.3] - 2026-11-11

### 新增

- `scripts/dev.mjs`：跨平台本地服务编排，支持 `up`（容器 → 迁移 → API / Worker / Nuxt，已在跑的服务跳过）、
  `status`（含 API 版本）、`restart`（重建后重启）、`stop`、`watch`（监听 Go 源码自动重建重启）；
  `up` 默认前台守护，Ctrl+C 只收本次启动的进程。日志统一到 `data/logs/`，pid 到 `data/pids/`。

### 改进

- README 补一键启动命令、拆分步骤与日志位置；根 `package.json` 增 `dev:up` / `dev:watch` / `dev:status` / `dev:restart`。

### 修复

- `bump-version.mjs` 在 `[未发布]` 没有条目时也照常发布，会产出版本条目为「（无条目）」的空版本；
  现在直接拒绝并提示先把条目写进 `[未发布]`（确实要空发布需加 `--force`）。
- `scripts/dev.mjs` 前端启动改走 `node node_modules/nuxt/bin/nuxt.mjs dev`：此前用 `npx.cmd` + shell 在 Windows
  下进程起不来且日志空白；首次 dev 编译常需 40–90s，等待上限从 60s 放宽到 120s，避免误报「未就绪」。

## [0.5.2] - 2026-11-11

### 新增

- 纳入版本控制：初始化 git 仓库并建立首个提交（此前项目没有任何版本库，历史过程只存在于工作日志）。

### 改进

- `.gitignore` 补 `/gui-test-screenshots/`（本地 UI 验证截图，18MB 级、可重生成）与 `*.zcode-session`（编辑器会话临时文件）。

## [0.5.1] - 2026-11-11

### 新增

- 版本号与更新日志体系：新增 `VERSION`、本文件与 `scripts/bump-version.mjs`；`GET /health/live` 与 API / Worker
  启动日志输出版本与 commit；设置页底部显示版本（`YJ_APP_VERSION` 可覆盖）。
- `internal/platform/buildinfo`：版本常量支持 `-ldflags -X` 注入，本地默认读源码常量。

### 修复

- 个人库标题沉到页面底部：页头 `.bar` 与封面续播进度条同名，进度条的 `position: absolute; bottom: 0; height: 3px`
  一并作用到 header；进度条更名为 `.cover-progress`（`apps/web/app/pages/library.vue`）。
- 个人库历史 / 稍后再看只有「视频 / 图集」占位、没有缩略图：`interactions` 注入对象存储并新增批量
  `attachPosters`（图集取首图、视频取产物清单 `poster.*`，按 `driver` 区分签名与 demo 路径）。
- 创作中心面板比指标卡行宽 34px：`.panel` / `.notice` 补 `box-sizing: border-box`。

### 改进

- 创作中心内容栏从写死的 860px 改为布局令牌 `--vp-layout-content-max: 1200px`，标题栏、标签、指标卡、面板、
  作品管理、评论管理同宽；1440 宽视口两侧留白 290 → 120。
- 草稿箱样式统一：`data-surface` 由 `tool` 改 `immersive`，「最新创建在前」补胶囊样式，「新建作品」统一品牌粉，
  空态限宽 820 与列表对齐，checkbox 换成深色方框 + 品牌粉勾选。
- 设置页新增版本脚注，与 `/health/live` 版本同源。

## [0.5.0] - 2026-11-11

### 新增

- 视频与底栏之间的通栏播放控制条：播放 / 时间 / 弹幕开关 / 弹幕设置 / 输入框 / 发送 / 连播 / 清屏 / 清晰度 /
  倍速 / 画中画 / 音量 / 循环 / 全屏；进度线并入控制条顶部。
- 倍速、清晰度、音量从原浮动面板改为控制条内联弹出层（点条上别处、点外部、Esc 均收起）。
- 弹幕 UI：开关、输入发送、6 轨道从右向左滚动，`prefers-reduced-motion` 时不滚动。
- 移动端弹幕入口：`@作者名` 上方方形「弹」按钮，点开底部输入条（自动聚焦、回车发送、可连发）。
- 图片内容控制条：自动轮播 ▶/⏸、连播、清屏、全屏；图集指示器改居中胶囊（当前张 + `1 / N`）。
- `ImIcon` 新增 `loop`、`face` 图标。

### 修复

- 播放控制条模板早已存在但样式全缺、`pip` / `expand` 图标缺失会回退成三个点；补齐样式与图标。
- 弹幕按视频时间轴算 8 秒窗口，短视频 `loop` 回绕后刚发的弹幕立即消失；改为真实时钟窗口。

### 改进

- 控制条左右通栏、去掉圆角，与底栏连成一条，消除两侧与底部露画面的缝。
- 移动端（≤780px）隐藏整条控制条，音量交给系统；信息层与操作栏随之下移，不再留 65px 空档。
- 移动端弹幕输入条浮在底栏上方，不再遮住主导航。
- 操作栏、作者信息、toast 底部偏移同步上调，避免被新控制条压住。

## [0.4.0] - 2026-11-09

### 新增

- 通知中心：`user_notifications` 增加 `category` / `actor_id` / `deleted_at`（迁移 12）；`GET /notifications?category=`
  三分组过滤 + 游标分页 + `unread_by_category`；按 ids 或按分组全部已读；单条软删。
- 新 UI 一批：评论抽屉（发评 / 回复 / 删除 / @高亮）、举报对话框、搜索历史、话题页、个人库三分区。
- 操作栏「更多」菜单：分享下方三点按钮，举报收进弹出菜单，独立举报按钮移除。

### 修复

- 任何浏览器都无法播放 HLS：hls.js 经 `blob:` 拉取播放清单，但 CSP `connect-src` 不含 `blob:`
  （Go 中间件与 Nuxt 安全头两处补齐）。
- 签名模式播放 403：hls.js 直拉变体，相对分片引用不继承签名；改用后端重写的 `playlists` 生成 Blob 变体。
- 竖屏清晰度标签按长边显示（720x1280 显示成 `1280p`），改为按短边标注。
- 登录页忽略 `redirect` 参数、恒跳 `/publish`；改为只接受站内绝对路径，`//`、`/\`、外链一律回落。

### 改进

- 播放器验证脚本新增「媒体资源无加载失败」断言与 cache-buster，避免控制台报错但测试全绿。

## [0.3.0] - 2026-11-08

> 补录：依据工作日志 M12–M27 汇总，条目按里程碑归纳。

### 新增

- 关注关系、关注流与关注通知；关系列表页（关注 / 粉丝 / 赞过我）。
- 创作者中心：数据看板、作品管理、评论管理。
- 工具型页面浅色主题；状态反馈矩阵与对话框可访问性。
- 发布页字段规格与发布设置、图集发布、发布页三段式布局、封面选帧。
- 草稿箱（含分页与批量删除）、作品换封面、已发布图集换封面、头像上传全链路。
- 后台 Outbox 可观测与重放，动效令牌收尾。

### 修复

- Outbox 不再无限重试：补终态与最大尝试次数。
- 草稿箱分页与批量删除的契约瑕疵。

## [0.2.0] - 2026-11-07

> 补录：依据工作日志 M2–M11 汇总。

### 新增

- 沉浸流主界面（M2）、页面交付与 SEO（M3）、混合来源与管理后台（M4）、性能优化与真实采样（M5）。
- 搜索与分类（M6）、审核申诉与站内通知（M7）、分类运营后台与举报人反馈闭环（M8）。
- 后台账号与口令管理（M9）、「我」页三分区（M10）、设置页与通知偏好（M11）。
- 内容页下拉组件 `VpSelect`，替换原生 `select`。

### 修复

- 关注按钮「+」偏移（两个独立缺陷叠加）。
- 后台状态标签「青底粉框」、输入框焦点环与边框错位。
- 图片轮播指示器浮在评论区之上；中央播放 / 暂停图标画反。

### 改进

- 操作栏图标改版对齐参考站；后台侧栏单字标记换成真实图标；状态徽标统一到设计令牌。

## [0.1.0] - 2026-11-06

> 补录：依据工作日志 M0 / M1 汇总。

### 新增

- Go 模块化单体骨架：`cmd/{api,migrate,worker}`、`internal/app/router.go`、`internal/platform/{config,database,cache,httpserver}`。
- 认证会话模块、内容模型与发布流程、S3 预签名直传、转码管线（MP4 / HLS / 封面 / 雪碧图）。
- Nuxt 4 前端骨架、`compose.yaml`、迁移与 CI。

### 安全维护

- 口令 Argon2id 哈希；会话只存 token 哈希且带 idle / absolute 双 TTL；CSRF 双提交校验。
- 登录限流；Cookie 使用 HttpOnly + Secure + SameSite；统一 CSP、请求 ID 与错误信封。
