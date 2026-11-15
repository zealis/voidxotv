# 数据库访问规范（防 N+1）

列表、批量、详情接口的 **SQL 次数必须与数据量无关**。一次请求打几条 SQL 由
代码结构决定，不能因为「这一页有 20 条内容」就变成 40 条。

## 禁止

- 在 `for` / `for rows.Next()` 循环体内直接调用 `db.Query`、`db.QueryRow`、
  `db.Exec`（`tx` / `pool` / `h.db` / `r.db` 同样算）。
- 在循环里调用仓库方法、方法内部再查库（间接 N+1）——静态工具查不到，
  评审时必须看。
- 为了「代码简单」逐条查询关联数据（作者、封面、点赞状态、话题状态等）。

## 正确做法

- **批量读**：`WHERE id = ANY($1)`、JOIN、`LATERAL`，一次查回后在内存用
  `map[id]` 组装。示例：`feed.repository.Avatars`、`ViewerReactions`、
  `interactions.library.attachPosters`、`notifications.attachActors`。
- **批量写**：
  - `INSERT ... SELECT unnest($2::bigint[]) ON CONFLICT DO NOTHING`
  - `UPDATE ... FROM unnest($2::bigint[]) WITH ORDINALITY AS t(id, ord)`
  - `INSERT ... SELECT ... FROM unnest($1::text[], $2::text[]) ... ON CONFLICT`
  - 示例：`content.SetContentTopics`、图集排序、`gifs.SaveConfig`、
    `delivery.Enqueue`。
- **循环只做内存操作**：去重、拼接参数、`map` 映射、格式化。
- **外部调用**也一样：S3 预签名是本地 HMAC 可接受；真正的网络请求不要在
  循环里逐条阻塞，要并发 + 限流 + 失败回退。

## 检查

```bash
go run ./internal/tools/checkn1 ./internal
# 或
npm run check:n-plus-one
```

工具用 Go AST 扫描 `for` / `range` 循环体里的直接数据库调用。确实必须逐条
执行时（例如运维脚本、固定一两条的初始化），在调用行或上一行写：

```go
// n+1-allow: 单条迁移脚本，量固定为 1
```

## 设计口径

- 列表接口：**SQL 条数是常数**（通常 1–3 条），不随 `limit` 增长。
- 详情接口：关联数据一次查回，不要「先查内容、再逐条查作者/封面/统计」。
- 批量操作：一条语句处理 N 条，事务保证原子性。
