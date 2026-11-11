// 私有页面禁止进入公共 CDN 缓存（开发技术规范 §2.4 / §10.6、前端架构规范 §4.5）。
// Nitro 对动态 SSR 页面会写 no-cache，这里在 render:response 之后统一覆盖为 private, no-store。
const PRIVATE_PREFIXES = [
  '/me',
  '/library',
  '/messages',
  '/settings',
  '/studio',
  '/upload',
  '/publish',
  '/admin',
] as const

export default defineNitroPlugin((nitroApp) => {
  nitroApp.hooks.hook('render:response', (response, context) => {
    const path = context.event.path || ''
    const isPrivate = PRIVATE_PREFIXES.some(
      (prefix) => path === prefix || path.startsWith(`${prefix}/`),
    )
    if (!isPrivate) return
    response.headers = {
      ...(response.headers || {}),
      'cache-control': 'private, no-store',
    }
  })
})
