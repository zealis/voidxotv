import { siteBase, xml } from '../utils/seo'

// sitemap 索引：视频、图片与静态页分片，便于量大时独立再生成。
export default defineEventHandler((event) => {
  setHeader(event, 'content-type', 'application/xml; charset=utf-8')
  const base = siteBase()
  const lastmod = new Date().toISOString()
  const children = ['sitemap-videos.xml', 'sitemap-images.xml', 'sitemap-pages.xml']
  const entries = children.map((name) =>
    `<sitemap><loc>${xml(`${base}/${name}`)}</loc><lastmod>${xml(lastmod)}</lastmod></sitemap>`).join('')
  return `<?xml version="1.0" encoding="UTF-8"?>\n<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${entries}</sitemapindex>`
})
