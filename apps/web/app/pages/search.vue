<script setup lang="ts">
// 搜索页（§9.2）：须登录后使用，5 秒查询间隔由服务端强制，这里只做倒计时反馈。
// 页面为动态 SSR 且不缓存，不参与 ISR 再生成（§10.6）。
import type { FeedItem } from '~/composables/useFeed'
import { ApiError, errorMessage } from '~/composables/useApi'

useSeoMeta({
  title: '搜索 · 夜街',
  robots: 'noindex,nofollow',
})

interface Category {
  id: number
  slug: string
  name: string
  description: string
  content_count: number
}

interface SearchResult {
  query: string
  items: FeedItem[]
  next_cursor: string | number | null
  count: number
  took_ms: number
}

const route = useRoute()
const router = useRouter()
const { api, getMe } = useApi()

const query = ref(String(route.query.q || ''))
const category = ref(String(route.query.category || ''))
const type = ref('')
const duration = ref(0)
const durationOptions = [
  { value: 0, label: '全部时长' },
  { value: 1, label: '1 分钟内' },
  { value: 2, label: '1–3 分钟' },
  { value: 3, label: '3 分钟以上' },
]
const typeOptions = [
  { value: '', label: '全部' },
  { value: 'video', label: '视频' },
  { value: 'image', label: '图集' },
]
// 原生 <select> 的弹层由系统绘制（白底蓝条），深色界面里很突兀，统一走 VpSelect。
const categoryOptions = computed(() => [
  { value: '', label: '全部分类' },
  ...categories.value.map((item) => ({
    value: item.slug,
    label: `${item.name}（${item.content_count}）`,
  })),
])

const items = ref<FeedItem[]>([])
const cursor = ref<string | number | null>(null)
const loading = ref(false)
const searched = ref(false)
// SSR 阶段不知道登录态，先显示检查中，避免先渲染可搜索界面再跳成登录门。
const checking = ref(true)
const needLogin = ref(false)
const message = ref('')
const cooldown = ref(0)
const hot = ref<{ query: string; hits: number }[]>([])
const categories = ref<Category[]>([])
// 搜索历史只存本机 localStorage（§9.2 隐私与保留策略）：不上传服务端，
// 最多保留 10 条、可一键清空，清空后不可恢复。
const HISTORY_KEY = 'yejie-search-history-v1'
const history = ref<string[]>([])
let timer: ReturnType<typeof setInterval> | null = null

function startCooldown(seconds = 5) {
  cooldown.value = seconds
  if (timer) clearInterval(timer)
  timer = setInterval(() => {
    cooldown.value = Math.max(cooldown.value - 1, 0)
    if (cooldown.value === 0 && timer) {
      clearInterval(timer)
      timer = null
    }
  }, 1000)
}

function buildPath(term: string, next: string | number | null) {
  const params = new URLSearchParams({ q: term, limit: '12' })
  if (category.value) params.set('category', category.value)
  if (type.value) params.set('type', type.value)
  if (duration.value) params.set('duration', String(duration.value))
  if (next !== null) params.set('cursor', String(next))
  return `/api/v1/search?${params.toString()}`
}

async function runSearch(reset = true) {
  const term = query.value.trim()
  if (term.length < 2) {
    message.value = '请输入至少 2 个字符'
    return
  }
  if (cooldown.value > 0) {
    message.value = `请 ${cooldown.value} 秒后再搜索`
    return
  }
  loading.value = true
  message.value = ''
  needLogin.value = false
  try {
    const data = await api<SearchResult>(buildPath(term, reset ? null : cursor.value))
    items.value = reset ? data.items : [...items.value, ...data.items]
    cursor.value = data.next_cursor
    searched.value = true
    if (reset) {
      rememberHistory(term)
      await router.replace({ query: { q: term, ...(category.value ? { category: category.value } : {}) } })
      await loadHot()
    }
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      needLogin.value = true
    } else if (error instanceof ApiError && error.code === 'search_rate_limited') {
      startCooldown(5)
      message.value = '搜索过于频繁，请稍后再试'
    } else if (error instanceof ApiError && error.code === 'query_too_short') {
      message.value = '请输入至少 2 个字符'
    } else {
      message.value = errorMessage(error)
    }
  } finally {
    loading.value = false
  }
}

function submit() {
  if (cooldown.value > 0) return
  void runSearch(true)
}

function rememberHistory(term: string): void {
  history.value = [term, ...history.value.filter((item) => item !== term)].slice(0, 10)
  try {
    localStorage.setItem(HISTORY_KEY, JSON.stringify(history.value))
  } catch { /* storage disabled */ }
}

function loadHistory(): void {
  try {
    const raw = JSON.parse(localStorage.getItem(HISTORY_KEY) || '[]')
    history.value = Array.isArray(raw) ? raw.filter((item) => typeof item === 'string').slice(0, 10) : []
  } catch {
    history.value = []
  }
}

function clearHistory(): void {
  history.value = []
  try {
    localStorage.removeItem(HISTORY_KEY)
  } catch { /* storage disabled */ }
}

function useHistory(term: string): void {
  query.value = term
  submit()
}

// 无结果态展示「已用条件」的可读形式（§6.7）。
const appliedFilters = computed(() => {
  const parts: string[] = []
  const categoryLabel = categoryOptions.value.find((option) => option.value === category.value)?.label
  if (categoryLabel) parts.push(`分类：${categoryLabel}`)
  const typeLabel = typeOptions.find((option) => option.value === type.value)?.label
  if (typeLabel) parts.push(`类型：${typeLabel}`)
  const durationLabel = durationOptions.find((option) => option.value === duration.value)?.label
  if (durationLabel) parts.push(`时长：${durationLabel}`)
  return parts
})

function clearFilters(): void {
  category.value = ''
  type.value = ''
  duration.value = 0
  submit()
}

async function loadHot() {
  try {
    const data = await api<{ items: { query: string; hits: number }[] }>('/api/v1/search/hot')
    hot.value = data.items || []
  } catch {
    hot.value = []
  }
}

onMounted(async () => {
  const me = await getMe()
  checking.value = false
  if (!me) {
    needLogin.value = true
  } else {
    loadHistory()
    void loadHot()
  }
  try {
    const data = await api<{ items: Category[] }>('/api/v1/categories')
    categories.value = data.items || []
  } catch {
    categories.value = []
  }
  if (query.value.trim().length >= 2) void runSearch(true)
})

onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

function linkOf(item: FeedItem) {
  return item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`
}
</script>

<template>
  <main class="search" data-surface="immersive">
    <header class="bar">
      <NuxtLink to="/" class="back" aria-label="回到沉浸流">←</NuxtLink>
      <form class="field" @submit.prevent="submit">
        <input
          v-model="query" type="search" name="q" autocomplete="off"
          placeholder="搜索作品、话题或标签（至少 2 个字符）"
          aria-label="搜索关键词"
        >
        <button type="submit" :disabled="loading || cooldown > 0">
          {{ cooldown > 0 ? `${cooldown}s` : '搜索' }}
        </button>
      </form>
    </header>

    <section class="filters" aria-label="筛选">
      <VpSelect
        v-model="category" label="分类" aria-label="按分类筛选"
        :options="categoryOptions" @change="searched && submit()"
      />
      <VpSelect
        v-model="type" label="类型" aria-label="按内容类型筛选"
        :options="typeOptions" @change="searched && submit()"
      />
      <VpSelect
        v-model="duration" label="时长" aria-label="按时长筛选"
        :options="durationOptions" @change="searched && submit()"
      />
    </section>

    <p v-if="message" class="notice" role="status">{{ message }}</p>

    <p v-if="checking" class="hint">正在检查登录状态…</p>

    <section v-else-if="needLogin" class="gate">
      <h1>搜索需要登录</h1>
      <p>为限制自动化刷取与资源消耗，夜街的搜索仅对已登录账号开放。</p>
      <NuxtLink :to="`/login?redirect=${encodeURIComponent(route.fullPath)}`" class="cta">登录后搜索</NuxtLink>
    </section>

    <template v-else>
      <section v-if="!searched && history.length" class="hot" aria-label="搜索历史">
        <h2>
          搜索历史
          <button type="button" class="clear" @click="clearHistory">清空</button>
        </h2>
        <ul>
          <li v-for="term in history" :key="term">
            <button type="button" @click="useHistory(term)">{{ term }}</button>
          </li>
        </ul>
        <p class="privacy">历史仅保存在本机浏览器（最多 10 条），不会上传。</p>
      </section>

      <section v-if="!searched && hot.length" class="hot">
        <h2>热门搜索</h2>
        <ul>
          <li v-for="term in hot" :key="term.query">
            <button type="button" @click="query = term.query; submit()">{{ term.query }}</button>
            <span>{{ term.hits }}</span>
          </li>
        </ul>
      </section>

      <section v-if="searched" class="results" aria-live="polite">
        <p class="summary">
          共 {{ items.length }} 条结果<template v-if="items.length">（本页）</template>
        </p>
        <div class="grid">
          <article v-for="item in items" :key="item.slug" class="card">
            <NuxtLink :to="linkOf(item)">
              <div class="cover">
                <img
                  v-if="item.media?.poster_url" :src="item.media.poster_url"
                  :alt="item.title || '作品封面'" loading="lazy"
                >
                <span v-else class="cover-fallback">{{ item.type === 'image' ? '图集' : '视频' }}</span>
                <span class="type">{{ item.type === 'image' ? '图集' : '视频' }}</span>
              </div>
              <h2>{{ item.title || '（无标题）' }}</h2>
              <p class="meta">@{{ item.author.handle }}</p>
              <p v-if="item.tags?.length" class="tags">
                <span v-for="tag in item.tags" :key="tag">#{{ tag }}</span>
              </p>
              <p v-else class="caption">{{ item.description || '暂无文案' }}</p>
            </NuxtLink>
          </article>
        </div>
        <div v-if="!items.length && !loading" class="empty-state">
          <p class="empty">没有找到匹配的作品。</p>
          <p class="used" v-if="appliedFilters.length">
            已用条件：关键词「{{ query.trim() }}」
            <template v-for="(part, index) in appliedFilters" :key="part"> · {{ part }}</template>
          </p>
          <p v-else class="used">已用条件：关键词「{{ query.trim() }}」</p>
          <div class="suggest">
            <button type="button" class="clear" @click="clearFilters">清除筛选项</button>
            <button type="button" class="clear" @click="query = ''; searched = false">换个关键词</button>
          </div>
        </div>
        <button
          v-if="cursor !== null" type="button" class="more"
          :disabled="loading || cooldown > 0" @click="runSearch(false)"
        >
          {{ cooldown > 0 ? `${cooldown}s 后可继续` : (loading ? '加载中…' : '加载更多') }}
        </button>
      </section>

      <p v-else-if="!hot.length" class="hint">输入关键词开始搜索。</p>
    </template>
  </main>
</template>

<style scoped>
.search {
  min-height: 100dvh;
  padding: 20px 20px 56px;
  background: var(--vp-im-bg);
  color: var(--vp-im-text-primary);
}
.bar { display: flex; align-items: center; gap: 10px; max-width: 1080px; margin: 0 auto 16px; }
.back {
  width: 34px; height: 34px; display: grid; place-items: center;
  border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary); text-decoration: none; font-size: 15px;
}
.field { flex: 1; display: flex; gap: 8px; }
.field input {
  flex: 1; height: 38px; padding: 0 14px;
  border-radius: 99px; border: 1px solid rgba(255, 255, 255, 0.16);
  background: rgba(255, 255, 255, 0.06);
  color: var(--vp-im-text-primary); font-size: 13px;
}
.field input::placeholder { color: var(--vp-im-text-secondary); }
.field button {
  height: 38px; padding: 0 18px; border-radius: 99px; border: 0;
  background: var(--vp-im-accent); color: #fff; font-size: 13px; font-weight: 600;
  cursor: pointer;
}
.field button:disabled { opacity: 0.5; cursor: not-allowed; }
.filters {
  display: flex; gap: 10px; flex-wrap: wrap;
  max-width: 1080px; margin: 0 auto 18px;
}
.notice {
  max-width: 1080px; margin: 0 auto 14px; font-size: 12px;
  color: var(--vp-im-accent);
}
.gate, .hot, .results, .hint { max-width: 1080px; margin: 0 auto; }
.gate { padding: 48px 0; text-align: center; }
.gate h1 { margin: 0 0 8px; font-size: 20px; }
.gate p { margin: 0 0 18px; font-size: 13px; color: var(--vp-im-text-secondary); }
.cta {
  display: inline-block; padding: 9px 20px; border-radius: 99px;
  background: var(--vp-im-accent); color: #fff; font-size: 13px; text-decoration: none;
}
.hot h2 .clear,
.suggest .clear {
  margin-left: 10px;
  padding: 2px 10px;
  border: 1px solid rgba(255, 255, 255, 0.2);
  border-radius: 99px;
  background: transparent;
  color: var(--vp-im-text-secondary);
  font-size: 11px;
  cursor: pointer;
}
.privacy { margin: 6px 0 0; font-size: 11px; color: var(--vp-im-text-secondary); }
.empty-state { text-align: center; padding: 30px 0; }
.empty-state .used { margin: 6px 0 14px; font-size: 12px; color: var(--vp-im-text-secondary); }
.empty-state .suggest { display: flex; gap: 10px; justify-content: center; }
.hot h2 { margin: 0 0 10px; font-size: 14px; color: var(--vp-im-text-secondary); }
.hot ul { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 8px; }
.hot li { display: flex; align-items: center; gap: 6px; }
.hot button {
  padding: 6px 14px; border-radius: 99px; cursor: pointer;
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.06); color: var(--vp-im-text-primary); font-size: 12px;
}
.hot span { font-size: 11px; color: var(--vp-im-text-secondary); }
.summary { margin: 0 0 12px; font-size: 12px; color: var(--vp-im-text-secondary); }
.grid {
  display: grid; gap: 16px;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
}
.card a { text-decoration: none; color: inherit; display: grid; gap: 6px; }
.cover {
  position: relative; aspect-ratio: 3 / 4; border-radius: 14px; overflow: hidden;
  background: rgba(255, 255, 255, 0.05);
}
.cover img { width: 100%; height: 100%; object-fit: cover; }
.cover-fallback {
  position: absolute; inset: 0; display: grid; place-items: center;
  font-size: 13px; color: var(--vp-im-text-secondary);
}
.type {
  position: absolute; right: 8px; bottom: 8px;
  padding: 2px 8px; border-radius: 99px; font-size: 11px;
  background: rgba(0, 0, 0, 0.55); color: #fff;
}
.card h2 { margin: 0; font-size: 13px; font-weight: 600; line-height: 1.4; }
.meta { margin: 0; font-size: 11px; color: var(--vp-im-text-secondary); }
.tags { margin: 0; display: flex; gap: 6px; flex-wrap: wrap; font-size: 11px; color: var(--vp-im-accent); }
.caption {
  margin: 0; font-size: 12px; line-height: 1.5; color: var(--vp-im-text-secondary);
  display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}
.empty, .hint { margin-top: 24px; font-size: 13px; color: var(--vp-im-text-secondary); }
.more {
  display: block; margin: 24px auto 0; padding: 9px 24px; border-radius: 99px;
  border: 1px solid rgba(255, 255, 255, 0.16); background: transparent;
  color: var(--vp-im-text-primary); font-size: 13px; cursor: pointer;
}
.more:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
