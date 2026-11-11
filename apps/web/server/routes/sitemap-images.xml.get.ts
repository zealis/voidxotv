import { fetchSEOPages, siteBase, xml } from '../utils/seo'

// 图片站点地图：公开图集全部图片（按 media_assets 顺序）+ 公开视频封面。
// 下架 / 转非公开的内容由 Go 元数据接口过滤，再生成时自动移除。
export default defineEventHandler(async (event) => {
  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  const pages = await fetchSEOPages()
  const entries = pages.map((page) => {
    const urls = page.type === 'image'
      ? (page.images || [])
      : (page.thumbnail_url ? [page.thumbnail_url] : [])
    if (!urls.length) return ''
    const images = urls.map((url) =>
      `<image:image><image:loc>${xml(url)}</image:loc>` +
      `<image:title>${xml(page.title)}</image:title>` +
      `<image:caption>${xml(page.description)}</image:caption></image:image>`).join('')
    return `<url><loc>${xml(page.canonical || siteBase() + page.path)}</loc>` +
      `<lastmod>${xml(page.published_at)}</lastmod>${images}</url>`
  }).join('')
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:image="http://www.google.com/schemas/sitemap-image/1.1">${entries}</urlset>`
})
