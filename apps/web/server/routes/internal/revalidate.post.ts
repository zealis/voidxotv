// 夜街 · Nuxt 内部 ISR 再验证端点（开发技术规范 §10.6）
// 只供 Go Delivery 模块调用：共享密钥鉴权，不暴露给普通用户。
// 删除 Nitro SWR 缓存键后主动回源一次，让页面按最新业务数据重新生成。
export default defineEventHandler(async (event) => {
  const token = getHeader(event, 'x-delivery-token')
  const expected = process.env.YJ_DELIVERY_TOKEN || 'dev-internal-token'
  if (!token || token !== expected) {
    throw createError({ statusCode: 401, statusMessage: 'unauthorized' })
  }

  const body = await readBody<{ paths?: string[] }>(event)
  const paths = [...new Set(
    (body?.paths || []).filter(
      (path) => typeof path === 'string' && path.startsWith('/') && !path.startsWith('//'),
    ),
  )].slice(0, 50)
  if (!paths.length) {
    return { data: { requested: [], removed: [], warmed: [] } }
  }

  // Nitro 缓存键为 `/cache:nitro/handlers:<name>:<path 前 16 字符>.<path hash>.json`
  // （nitropack internal/cache.mjs）。按路径前缀删除该页所有缓存变体（含不同查询串），
  // 不做全站清理；base 带前导斜杠，必须从根 storage 枚举。
  const storage = useStorage()
  const removed: string[] = []
  const prefixes = paths.map(cacheKeyPrefix)
  let allKeys: string[] = []
  try { allKeys = await storage.getKeys() } catch { /* storage unavailable: warm fallback below */ }
  for (const key of allKeys) {
    if (!key.startsWith('/cache') && !key.startsWith('cache')) continue
    if (prefixes.some((prefix) => key.includes(prefix))) {
      await storage.removeItem(key)
      removed.push(key)
    }
  }

  // 主动回源：页面本体仍由 Nuxt ISR/SWR 生成，Delivery 不经手 HTML。
  const warmed: string[] = []
  for (const path of paths) {
    try {
      await $fetch(path, { headers: { 'x-delivery-revalidate': '1' } })
      warmed.push(path)
    } catch {
      // 单个尚未实现的路由（如 /creator/*）不阻断其余路径的再验证。
    }
  }

  return { data: { requested: paths, removed, warmed } }
})

function cacheKeyPrefix(path: string): string {
  const pathname = path.split('?')[0] || ''
  // 与 nitropack 的 escapeKey 一致：仅保留 [A-Za-z0-9_]，取前 16 字符。
  const escaped = pathname.replace(/[^A-Za-z0-9_]/g, '').slice(0, 16)
  return escaped || 'index'
}
