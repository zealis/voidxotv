// sitemap 与结构化数据共用 Go 公开元数据接口，保证字段同源（§11.2 / 前端 §5.2）。
export interface SEOPage {
  id: number
  slug: string
  type: string
  path: string
  canonical: string
  title: string
  description: string
  handle: string
  creator: string
  published_at: string
  thumbnail_url?: string
  content_url?: string
  duration_ms: number
  duration_iso?: string
  images?: string[]
  indexable: boolean
}

export function siteBase(): string {
  return (process.env.YJ_SITE_URL || process.env.YJ_WEB_ORIGIN || 'http://127.0.0.1:13000')
    .replace(/\/$/, '')
}

export async function fetchSEOPages(): Promise<SEOPage[]> {
  const pages: SEOPage[] = []
  let cursor = 0
  for (let round = 0; round < 10; round++) {
    const payload = await $fetch<{ data?: { items?: SEOPage[]; next_cursor?: number } }>(
      `/api/v1/seo/pages?limit=500&cursor=${cursor}`,
    )
    const items = payload?.data?.items || []
    pages.push(...items)
    const next = payload?.data?.next_cursor || 0
    if (!next || items.length < 500) break
    cursor = next
  }
  return pages.filter((page) => page.indexable)
}

export function xml(value: unknown): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}
