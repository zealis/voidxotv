import { fetchSEOPages, siteBase, xml } from '../utils/seo'

// 视频站点地图扩展（开发技术规范 §11.2）：字段与页面可见内容及 VideoObject 同源。
export default defineEventHandler(async (event) => {
  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  const pages = (await fetchSEOPages()).filter((page) => page.type === 'video')
  const entries = pages.map((page) => {
    const loc = xml(page.canonical || siteBase() + page.path)
    const video = [
      page.thumbnail_url ? `<video:thumbnail_loc>${xml(page.thumbnail_url)}</video:thumbnail_loc>` : '',
      `<video:title>${xml(page.title)}</video:title>`,
      `<video:description>${xml(page.description)}</video:description>`,
      page.content_url ? `<video:content_loc>${xml(page.content_url)}</video:content_loc>` : '',
      page.duration_ms ? `<video:duration>${Math.round(page.duration_ms / 1000)}</video:duration>` : '',
      `<video:publication_date>${xml(page.published_at)}</video:publication_date>`,
    ].join('')
    return `<url><loc>${loc}</loc><lastmod>${xml(page.published_at)}</lastmod><video:video>${video}</video:video></url>`
  }).join('')
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:video="http://www.google.com/schemas/sitemap-video/1.1">${entries}</urlset>`
})
