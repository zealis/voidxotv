// M2 互动为本地乐观状态（localStorage）：点赞 / 收藏 / 关注按钮的即时回显。
// 评论自 M29 起直接走服务端（CommentSheet 内管理），不再进入本地缓存。
interface Entry {
  liked: boolean
  favorited: boolean
  following: boolean
}

const STORAGE_KEY = 'yejie-m2-interactions-v1'

function readAll(): Record<string, Entry> {
  if (!import.meta.client) return {}
  try {
    return JSON.parse(localStorage.getItem(STORAGE_KEY) || '{}') as Record<string, Entry>
  } catch {
    return {}
  }
}

function writeAll(value: Record<string, Entry>): void {
  if (!import.meta.client) return
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(value))
  } catch { /* storage disabled: keep in-memory state only */ }
}

export function useInteractions() {
  const memory = useState<Record<string, Entry>>('yejie-interactions', () => ({}))

  function ensure(slug: string): Entry {
    const existing = memory.value[slug]
    if (existing) return existing
    const fresh: Entry = { liked: false, favorited: false, following: false }
    memory.value = { ...memory.value, [slug]: fresh }
    return fresh
  }

  function hydrate(): void {
    if (!import.meta.client) return
    memory.value = { ...readAll(), ...memory.value }
  }

  function state(slug: string): Entry {
    return memory.value[slug] || { liked: false, favorited: false, following: false }
  }

  function update(slug: string, patch: Partial<Entry>): void {
    const entry = ensure(slug)
    const next = { ...entry, ...patch }
    memory.value = { ...memory.value, [slug]: next }
    const persisted = readAll()
    persisted[slug] = next
    writeAll(persisted)
  }

  function toggleLike(slug: string): boolean {
    const liked = !ensure(slug).liked
    update(slug, { liked })
    return liked
  }

  function toggleFavorite(slug: string): boolean {
    const favorited = !ensure(slug).favorited
    update(slug, { favorited })
    return favorited
  }

  // 关注以服务端为准：登录后按 /me/following 覆盖本地缓存，避免旧 localStorage 状态误导。
  function setFollowing(slug: string, following: boolean): void {
    update(slug, { following })
  }

  function toggleFollow(slug: string): boolean {
    const following = !ensure(slug).following
    update(slug, { following })
    return following
  }

  return { hydrate, state, setFollowing, toggleLike, toggleFavorite, toggleFollow }
}
