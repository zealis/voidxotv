// 沉浸流数据层：只消费公开 feed / 深链接接口，不接触私密内容。
export interface FeedAuthor {
  id: number
  handle: string
  avatar_url?: string
}

export interface FeedMedia {
  mode: string
  poster_url?: string
  media_url?: string
  duration_ms: number
  signed: boolean
  expires_at?: string
  master_playlist?: string
  // 后端重写过的各变体播放清单（分片 URL 已带签名）。签名模式下必须用
  // Blob 喂给 hls.js：直接从存储拉变体会拿到相对分片引用，签名丢失 → 403。
  playlists?: Record<string, string>
  status: string
  // 可选 HEVC 源：仅在浏览器声明支持 hvc1 时使用，H.264 的 media_url 始终回退。
  hevc_url?: string
}

export interface FeedStats {
  likes: number
  comments: number
  favorites: number
  shares: number
}

export interface FeedItem {
  id: number
  slug: string
  type: 'video' | 'image'
  title: string
  description: string
  author: FeedAuthor
  published_at?: string
  stats: FeedStats
  media: FeedMedia
  images: string[]
  // 仅搜索结果与分类列表下发，用于结果卡的语义标签。
  tags?: string[]
  // 发布时添加的位置（§8）；仅详情下发。
  poi?: { name: string; address?: string }
}

export interface FeedResponse {
  items: FeedItem[]
  // 不透明游标：规则排序为 "score:id"，按时间排序为整数 id，末页为 null。
  next_cursor: number | string | null
  count: number
}

export function useFeed() {
  const { api } = useApi()

  // channel=follow 是关注流：只出我关注作者的作品，未登录时后端返回 401。
  async function list(
    limit = 12, cursor: string | number = '', channel: 'recommend' | 'follow' = 'recommend',
  ): Promise<FeedResponse> {
    const suffix = cursor === '' ? '' : `&cursor=${encodeURIComponent(String(cursor))}`
    const scope = channel === 'follow' ? '&channel=follow' : ''
    return api<FeedResponse>(`/api/v1/feed?limit=${limit}${suffix}${scope}`)
  }

  // 关注关系：hydrate 用 handles，单条操作用 follow()。两者都要求登录。
  async function following(): Promise<string[]> {
    const data = await api<{ handles: string[] }>('/api/v1/me/following')
    return data.handles || []
  }

  async function follow(handle: string, value: boolean): Promise<{ following: boolean; follower_count: number }> {
    return api<{ following: boolean; follower_count: number }>(
      `/api/v1/creators/${encodeURIComponent(handle)}/follow`,
      { method: 'POST', body: { value } },
    )
  }

  async function followState(handle: string): Promise<{ following: boolean; follower_count: number }> {
    return api<{ following: boolean; follower_count: number }>(
      `/api/v1/creators/${encodeURIComponent(handle)}/follow`,
    )
  }

  async function detail(slug: string): Promise<FeedItem> {
    return api<FeedItem>(`/api/v1/feed/${encodeURIComponent(slug)}`)
  }

  // 演示素材回退：正式头像走 users.avatar_key → 对象存储。
  function avatar(item: FeedItem): string {
    return item.author.avatar_url || `/demo/c${(item.author.id % 12) + 1}.jpeg`
  }

  function tags(text: string): string[] {
    return (text.match(/#[^\s#]+/g) || []).slice(0, 4)
  }

  function caption(text: string): string {
    return text.replace(/#[^\s#]+/g, '').replace(/\s+/g, ' ').trim()
  }

  function title(item: FeedItem): string {
    return item.title || caption(item.description).slice(0, 18) || '未命名作品'
  }

  function formatCount(value: number): string {
    if (value >= 10000) return `${(value / 10000).toFixed(1)}万`
    if (value >= 1000) return `${(value / 1000).toFixed(1)}k`
    return String(value)
  }

  return { list, detail, following, follow, followState, avatar, tags, caption, title, formatCount }
}
