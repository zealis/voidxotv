import { siteBase, xml } from '../utils/seo'

// 静态公开页 + 分类页（私有 / 参数化页面不进入 sitemap）。
// 分类属于 §10.3 的「搜索 / 分类缓存」依赖面，内容变更会触发其再验证。
export default defineEventHandler(async (event) => {
  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  const base = siteBase()
  const lastmod = new Date().toISOString()

  let categories: { slug: string }[] = []
  try {
    const payload = await $fetch<{ data?: { items?: { slug: string }[] } }>('/api/v1/categories')
    categories = payload?.data?.items || []
  } catch {
    // 分类接口不可用时仍返回首页条目，不产出 5xx。
    categories = []
  }

  const urls = ['/', ...categories.map((item) => `/category/${item.slug}`)]
  const entries = urls.map((path, index) =>
    `<url><loc>${xml(base + path)}</loc><lastmod>${xml(lastmod)}</lastmod>`
    + `<changefreq>${index === 0 ? 'hourly' : 'daily'}</changefreq>`
    + `<priority>${index === 0 ? '1.0' : '0.6'}</priority></url>`).join('')

  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${entries}</urlset>`
})
