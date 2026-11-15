// 纵深防御：反向代理应该自己 deny 这些路径，应用层再挡一道。
// 即使部署方式变化（例如误把仓库根目录当静态目录、或代理配置漏了规则），
// 也不会把 .git / .env / compose.yaml / 迁移脚本这类内容暴露出去。
const BLOCKED: RegExp[] = [
  /^\/\./, // .git / .env / .workbuddy / .nuxt …
  /^\/(?:compose|docker-compose)\.ya?ml$/i,
  /^\/(?:go\.(?:mod|sum)|package-lock\.json|yarn\.lock|pnpm-lock\.yaml|package\.json)$/i,
  /^\/(?:migrations|deployments|scripts|bin|data|tests|设计文档|参考资料)(?:\/|$)/i,
  /^\/(?:Makefile|Dockerfile|\.dockerignore|\.gitignore)$/i,
]

export default defineEventHandler((event) => {
  const path = getRequestURL(event).pathname
  // ACME 验证等必须放行，否则证书续期会失败。
  if (path.startsWith('/.well-known/')) return
  if (BLOCKED.some((pattern) => pattern.test(path))) {
    throw createError({ statusCode: 404, statusMessage: 'Not Found' })
  }
})
