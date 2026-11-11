// 互动状态（§6.2 操作栏回显）：点赞 / 收藏 / 关注都以服务端为准。
// 内存里只存当前会话的乐观状态；落库接口失败时由调用方提示，不假装成功。
// 旧 M2 的 localStorage 乐观缓存已移除：离线假点亮会让「我」的列表与角标对不上。
interface Entry {
  liked: boolean
  favorited: boolean
  following: boolean
}

export function useInteractions() {
  const { api } = useApi()
  const memory = useState<Record<string, Entry>>('yejie-interactions', () => ({}))

  function state(slug: string): Entry {
    return memory.value[slug] || { liked: false, favorited: false, following: false }
  }

  function update(slug: string, patch: Partial<Entry>): void {
    memory.value = { ...memory.value, [slug]: { ...state(slug), ...patch } }
  }

  // 服务端真相覆盖本地（进入页面 / 换列表后调用）：feed 条目带当前查看者的标记。
  function applyServer(slug: string, liked: boolean, favorited: boolean): void {
    update(slug, { liked, favorited })
  }

  function setFollowing(slug: string, following: boolean): void {
    update(slug, { following })
  }

  // 点赞 / 收藏开关（POST /api/v1/contents/{id}/reactions，幂等）。
  // 成功才更新本地乐观状态；401 等错误原样抛出，由调用方转为提示。
  async function toggleLike(slug: string, contentID: number): Promise<boolean> {
    const liked = !state(slug).liked
    await api(`/api/v1/contents/${contentID}/reactions`, {
      method: 'POST', body: { kind: 'like', value: liked },
    })
    update(slug, { liked })
    return liked
  }

  async function toggleFavorite(slug: string, contentID: number): Promise<boolean> {
    const favorited = !state(slug).favorited
    await api(`/api/v1/contents/${contentID}/reactions`, {
      method: 'POST', body: { kind: 'favorite', value: favorited },
    })
    update(slug, { favorited })
    return favorited
  }

  // 关注落库走 useFeed.follow()；这里只负责本地状态（深链接页暂未接服务端关注）。
  function toggleFollow(slug: string): boolean {
    const following = !state(slug).following
    update(slug, { following })
    return following
  }

  return { state, applyServer, setFollowing, toggleLike, toggleFavorite, toggleFollow }
}
