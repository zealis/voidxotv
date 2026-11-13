<script setup lang="ts">
// 个人库（§4.1 /library）：历史（续播）/ 收藏 / 晚点再看 三个分区。
// 收藏复用 reactions(kind='favorite') 的 /me/favorites；历史与晚点再看是独立表。
import { ApiError, errorMessage } from '~/composables/useApi'

const { open: openAuthDialog } = useAuthDialog()

useSeoMeta({ title: '个人库 · 夜街', robots: 'noindex,nofollow' })

type TabKey = 'history' | 'favorites' | 'watchlist'

const TABS: { key: TabKey; label: string; empty: string }[] = [
  { key: 'history', label: '历史', empty: '还没有观看记录' },
  { key: 'favorites', label: '收藏', empty: '还没有收藏的作品' },
  { key: 'watchlist', label: '晚点再看', empty: '晚点再看列表是空的' },
]

interface LibraryCard {
  content_id: number
  slug: string
  type: 'video' | 'image'
  title: string
  author_handle: string
  /** 历史 / 晚点再看接口返回的封面地址（图集首图或视频 poster）。 */
  poster_url?: string
  position_ms?: number
  duration_ms?: number
  updated_at?: string
}

interface FavoriteCard {
  slug: string
  type: 'video' | 'image'
  title: string
  author: { handle: string }
  media?: { poster_url?: string }
}

const { api, getMe } = useApi()
const asyncState = useAsyncState({ keepOnError: true, isEmpty: () => items.value.length === 0 })
const { phase, errorText, partial, retrying } = asyncState

const active = ref<TabKey>('history')
const items = ref<(LibraryCard & FavoriteCard)[]>([])
const loading = ref(false)
const message = ref('')
const checking = ref(true)
const needLogin = ref(false)

function linkOf(item: { type: string; slug: string }): string {
  return item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`
}

async function load() {
  loading.value = true
  message.value = ''
  asyncState.begin()
  try {
    let data: { items: LibraryCard[] | FavoriteCard[] } | null = null
    if (active.value === 'history') {
      data = await api<{ items: LibraryCard[] }>('/api/v1/me/history?limit=50')
    } else if (active.value === 'watchlist') {
      data = await api<{ items: LibraryCard[] }>('/api/v1/me/watchlist?limit=50')
    } else {
      data = await api<{ items: FavoriteCard[] }>('/api/v1/me/favorites')
    }
    items.value = (data.items || []) as (LibraryCard & FavoriteCard)[]
    asyncState.succeed()
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
      asyncState.succeed()
    } else {
      asyncState.fail(error)
      message.value = errorMessage(error)
    }
  } finally {
    loading.value = false
  }
}

function openTab(tab: TabKey) {
  if (active.value === tab) return
  active.value = tab
  items.value = []
  void load()
}

async function removeWatchlist(item: { content_id: number }) {
  try {
    await api(`/api/v1/contents/${item.content_id}/watchlist`, { method: 'POST' })
    items.value = items.value.filter((it) => it.content_id !== item.content_id)
  } catch (error) {
    message.value = errorMessage(error)
  }
}

function progressPct(item: LibraryCard): number {
  if (!item.duration_ms || !item.position_ms) return 0
  return Math.min(100, Math.round((item.position_ms / item.duration_ms) * 100))
}

function positionText(item: LibraryCard): string {
  const total = Math.round((item.duration_ms || 0) / 1000)
  const seen = Math.round((item.position_ms || 0) / 1000)
  const fmt = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
  return `${fmt(seen)} / ${fmt(total)}`
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
    return
  }
  await load()
})
</script>

<template>
  <main class="library" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/me" class="back" aria-label="回到「我」页">←</NuxtLink>
      <h1>个人库</h1>
    </header>

    <nav class="tabs" role="tablist" aria-label="个人库分区">
      <button
        v-for="tab in TABS" :key="tab.key" type="button" role="tab"
        :aria-selected="active === tab.key" :class="{ on: active === tab.key }"
        @click="openTab(tab.key)"
      >{{ tab.label }}</button>
    </nav>

    <p v-if="message" class="notice" role="status">{{ message }}</p>
    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <p>登录后查看历史、收藏与晚点再看。</p>
      <button type="button" class="cta" @click="openAuthDialog()">登录</button>
    </section>

    <VpAsyncState
      v-else :state="phase" :error="errorText" :partial="partial" :retrying="retrying"
      variant="list" :rows="4"
      :empty-title="TABS.find((t) => t.key === active)!.empty"
      :empty-hint="active === 'history' ? '看过的视频会出现在这里，并支持续播。'
        : active === 'favorites' ? '在沉浸流里点收藏，作品会出现在这里。'
        : '在视频的「更多」里点「晚点再看」，白天存下来晚上慢慢看。'"
      @retry="load"
    >
      <ul v-if="items.length" class="list">
        <li v-for="item in items" :key="item.slug">
          <NuxtLink :to="linkOf(item)" class="card">
            <div class="cover">
              <!-- 历史 / 晚点再看走 poster_url，收藏沿用 media.poster_url。 -->
              <img
                v-if="item.poster_url || item.media?.poster_url"
                :src="item.poster_url || item.media?.poster_url" alt="" loading="lazy"
              />
              <span v-else class="cover-fallback">{{ item.type === 'image' ? '图集' : '视频' }}</span>
              <span v-if="active === 'history' && item.position_ms" class="cover-progress">
                <span class="fill" :style="{ width: `${progressPct(item)}%` }" />
              </span>
            </div>
            <div class="info">
              <strong>{{ item.title || '（无标题）' }}</strong>
              <span class="sub">@{{ item.author_handle || item.author?.handle }}</span>
              <span v-if="active === 'history' && item.position_ms" class="sub">
                看到 {{ positionText(item) }}
              </span>
            </div>
          </NuxtLink>
          <button
            v-if="active === 'watchlist'" type="button" class="ops"
            @click="removeWatchlist(item)"
          >移除</button>
        </li>
      </ul>
    </VpAsyncState>
  </main>
</template>

<style scoped>
.library {
  min-height: 100dvh;
  padding: 20px 20px 56px;
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: 760px; margin: 0 auto 14px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none;
}
.bar h1 { margin: 0; font-size: 18px; }
.tabs { display: flex; gap: 8px; max-width: 760px; margin: 0 auto 16px; }
.tabs button {
  padding: 7px 16px; border-radius: 99px; cursor: pointer; font-size: 13px;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-secondary);
}
.tabs button.on { background: var(--vp-im-accent); border-color: var(--vp-im-accent); color: #fff; }
.notice { max-width: 760px; margin: 0 auto 12px; font-size: 12px; color: var(--vp-im-accent); }
.hint { max-width: 760px; margin: 24px auto; font-size: 13px; color: var(--vp-im-text-secondary); }
.gate { max-width: 760px; margin: 40px auto; text-align: center; }
.gate p { margin: 0 0 14px; font-size: 13px; color: var(--vp-im-text-secondary); }
.cta {
  border: 0; cursor: pointer; font-family: inherit;
  display: inline-block; padding: 8px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: #fff; font-size: 13px; text-decoration: none;
}
.list { list-style: none; margin: 0 auto; padding: 0; max-width: 760px; display: grid; gap: 10px; }
.list li { display: flex; align-items: center; gap: 10px; }
.card {
  flex: 1; display: flex; gap: 12px; align-items: center;
  padding: 10px 12px; border-radius: 14px; text-decoration: none;
  background: rgba(255, 255, 255, 0.04); border: 1px solid rgba(255, 255, 255, 0.06);
  color: var(--vp-im-text-primary);
}
.cover {
  position: relative; flex: none; width: 108px; height: 64px;
  border-radius: 10px; overflow: hidden; display: grid; place-items: center;
  background: var(--vp-im-surface-raised);
}
.cover img { width: 100%; height: 100%; object-fit: cover; }
.cover-fallback { font-size: 11px; color: var(--vp-im-text-secondary); }
/* 卡片封面上的续播进度：不能叫 .bar——页头的 header 也叫 .bar，
   absolute/bottom/3px 会把整个标题栏拽到页面底部。 */
.cover-progress {
  position: absolute; left: 0; right: 0; bottom: 0; height: 3px;
  background: rgba(255, 255, 255, 0.25);
}
.cover-progress .fill { display: block; height: 100%; background: var(--vp-im-accent); }
.info { min-width: 0; display: grid; gap: 2px; }
.info strong { font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sub { font-size: 11px; color: var(--vp-im-text-secondary); }
.ops {
  flex: none; padding: 7px 14px; border-radius: 99px; cursor: pointer; font-size: 12px;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-primary);
}
</style>
