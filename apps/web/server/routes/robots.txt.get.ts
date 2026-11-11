import { siteBase } from '../utils/seo'

// 放行公开页面与公开媒体资源；禁止抓取 API、内部端点与私有路径（前端 §5.2）。
export default defineEventHandler((event) => {
  setHeader(event, 'content-type', 'text/plain; charset=utf-8')
  const base = siteBase()
  const disallow = [
    '/api/', '/internal/', '/me', '/library', '/messages', '/settings',
    '/studio', '/admin', '/publish', '/login', '/register', '/search',
  ]
  return [
    'User-agent: *',
    'Allow: /',
    ...disallow.map((path) => `Disallow: ${path}`),
    // 媒体资源与播放列表不得被误封；S3/CDN 域名在生产环境应同步放行。
    'Allow: /demo/',
    `Sitemap: ${base}/sitemap.xml`,
    '',
  ].join('\n')
})
