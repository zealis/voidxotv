<script setup lang="ts">
// 评论表情面板的 GIF 页：上方搜索、下方结果网格，点一下把这条 GIF 交给父组件待发送。
//
// 两个图源，走法不同（后台「功能开关」二选一）：
// - API 盒子：服务端代理（本站 /api/v1/gifs/search）。密钥只在服务端，结果还能缓存省配额。
// - KLIPY：浏览器直连它的官方接口。它的条款要求请求由终端发起、不得经服务器转发或缓存，
//   所以 app_key 会出现在前端请求里（这是它的设计），缓存也只能落在访客自己的 localStorage。
import { ApiError, errorMessage } from '~/composables/useApi'

const emit = defineEmits<{ pick: [url: string] }>()

const { api } = useApi()
const { open: openAuthDialog } = useAuthDialog()

interface KlipyConfig {
  key: string
  filter: string
  locale: string
  limit: number
  cache_days: number
}

interface GifConfig {
  provider: 'off' | 'apihz' | 'klipy'
  ready: boolean
  apihz?: { limit: number }
  klipy?: KlipyConfig
}

const KLIPY_BASE = 'https://api.klipy.com/api/v1/'
const KLIPY_CACHE_PREFIX = 'yj-gif-klipy:'

const cfg = ref<GifConfig | null>(null)
const query = ref('')
const items = ref<string[]>([])
const page = ref(1)
// max_page = 0 表示上游没告诉我们总页数（API 盒子的热门接口就是这样），
// 这时不能当成「只有一页」，要一直加载到某一页返回空、或返回的全是已见过的图为止。
const maxPage = ref(0)
// KLIPY 用自己的 has_next 表达「还有下一页」。
const klipyNext = ref(false)
const exhausted = ref(false)
const loading = ref(false)
const error = ref('')
const needLogin = ref(false)
const needConfig = ref(false)
const loadedOnce = ref(false)

const isKlipy = computed(() => cfg.value?.provider === 'klipy')
const hasMore = computed(() => {
  if (exhausted.value) return false
  if (!items.value.length) return false
  if (isKlipy.value) return klipyNext.value
  if (maxPage.value > 0) return page.value < maxPage.value
  return true
})

const placeholder = computed(() => (isKlipy.value ? 'Search KLIPY' : '搜索 GIF 表情包'))

function describe(cause: unknown) {
  // 三种状态要分开说：没配源、没登录、真出错。混成一句「加载失败」等于让人无从下手。
  if (cause instanceof ApiError && cause.code === 'gif_not_configured') {
    needConfig.value = true
    error.value = '表情包源尚未配置'
    return
  }
  if (cause instanceof ApiError && (cause.status === 401 || cause.code === 'unauthorized')) {
    needLogin.value = true
    error.value = '登录后可以搜索 GIF'
    return
  }
  error.value = errorMessage(cause)
}

/** 拿图源配置：KLIPY 的 key 随这个接口下发，apihz 的 id/key 永远不会。 */
async function loadConfig() {
  cfg.value = await api<GifConfig>('/api/v1/gifs/config')
  if (!cfg.value.ready) {
    needConfig.value = true
    error.value = '表情包源尚未配置'
  }
}

function resetState() {
  items.value = []
  page.value = 1
  maxPage.value = 0
  klipyNext.value = false
  exhausted.value = false
  loadedOnce.value = false
}

function applyItems(incoming: string[], reset: boolean) {
  // 去重：上游分页界限偶尔会把同一批图再给一遍，重复 key 会让列表抖。
  const known = new Set(items.value)
  const fresh = reset ? incoming : incoming.filter((url) => !known.has(url))
  if (reset) {
    items.value = incoming
    exhausted.value = false
  } else {
    if (!incoming.length || !fresh.length) exhausted.value = true
    items.value = [...items.value, ...fresh]
  }
}

async function load(reset = true) {
  if (loading.value || !cfg.value?.ready) return
  loading.value = true
  error.value = ''
  needLogin.value = false
  const target = reset ? 1 : page.value + 1
  try {
    if (isKlipy.value) await loadFromKlipy(target, reset)
    else await loadFromProxy(target, reset)
    loadedOnce.value = true
    await nextTick()
    // 一页填不满容器时滚不动，滚动事件永远不触发：补一次「撑满」检查。
    const el = scrollBox.value
    if (el && el.scrollHeight <= el.clientHeight + 4) void loadMoreIfNeeded()
  } catch (cause) {
    describe(cause)
  } finally {
    loading.value = false
  }
}

async function loadFromProxy(target: number, reset: boolean) {
  const data = await api<{ items: string[]; page: number; max_page: number }>(
    `/api/v1/gifs/search?q=${encodeURIComponent(query.value.trim())}&page=${target}`,
  )
  applyItems(data.items || [], reset)
  page.value = data.page || target
  maxPage.value = data.max_page || 0
  if (maxPage.value > 0 && page.value >= maxPage.value) exhausted.value = true
}

/* ── KLIPY：浏览器直连 ─────────────────────────────────────────────
   响应结构文档写的是 { result, data: { data: [...] } }，但他们另有一节说与 Tenor 兼容
   （{ results } / { data }），实测还出现过「200 但解析不出结果」。所以这里不赌结构：
   找第一个「像结果集」的数组，再从每条里挖一个能当 <img src> 的 https 地址。 */

function klipyUrl(target: number): string {
  const k = cfg.value?.klipy
  if (!k) return ''
  const params = new URLSearchParams()
  params.set('per_page', String(k.limit || 24))
  params.set('page', String(target))
  if (k.filter) params.set('content_filter', k.filter)
  if (k.locale) params.set('locale', k.locale)
  const keyword = query.value.trim()
  if (keyword) {
    params.set('q', keyword)
    return `${KLIPY_BASE}${encodeURIComponent(k.key)}/gifs/search?${params.toString()}`
  }
  return `${KLIPY_BASE}${encodeURIComponent(k.key)}/gifs/trending?${params.toString()}`
}

function findList(node: unknown, depth = 0): unknown[] | null {
  if (!node || typeof node !== 'object' || depth > 3) return null
  if (Array.isArray(node)) return node.length ? node : null
  for (const key of ['data', 'results', 'gifs', 'stickers', 'items']) {
    const found = findList((node as Record<string, unknown>)[key], depth + 1)
    if (found) return found
  }
  return null
}

function pickUrl(node: unknown, depth = 0): string {
  if (!node || depth > 4) return ''
  if (typeof node === 'string') return /^https:\/\//i.test(node) ? node : ''
  if (typeof node !== 'object') return ''
  if (Array.isArray(node)) {
    for (const entry of node) {
      const url = pickUrl(entry, depth + 1)
      if (url) return url
    }
    return ''
  }
  // 先钻媒体容器再兜底裸 url：item.url 往往是网页地址，不是图。
  for (const key of ['file', 'media', 'preview', 'gif', 'webp', 'tinygif', 'hd', 'url']) {
    const url = pickUrl((node as Record<string, unknown>)[key], depth + 1)
    if (url) return url
  }
  return ''
}

function klipyHasNext(node: unknown, depth = 0): boolean {
  if (!node || typeof node !== 'object' || depth > 2) return false
  const record = node as Record<string, unknown>
  if (record.has_next === true) return true
  // Tenor 兼容口径用 next 当游标；KLIPY 自己的文档用 has_next / next_page。
  for (const key of ['next', 'next_page']) {
    const value = record[key]
    if (typeof value === 'string' && value !== '' && value !== '0') return true
    if (typeof value === 'number' && value > 0) return true
  }
  for (const key of ['data', 'result', 'results', 'pagination']) {
    if (klipyHasNext(record[key], depth + 1)) return true
  }
  return false
}

interface KlipyCacheEntry { saved: number; items: string[]; next: boolean }

function cacheKey(url: string): string {
  // djb2 只用来做 localStorage 的键，不参与任何安全判断。
  let hash = 5381
  for (let i = 0; i < url.length; i += 1) hash = ((hash << 5) + hash + url.charCodeAt(i)) >>> 0
  return `${KLIPY_CACHE_PREFIX}${hash}`
}

function readCache(url: string, days: number): KlipyCacheEntry | null {
  if (days <= 0) return null
  try {
    const raw = localStorage.getItem(cacheKey(url))
    if (!raw) return null
    const entry = JSON.parse(raw) as KlipyCacheEntry
    if (!entry?.items?.length) return null
    if (Date.now() - entry.saved > days * 86400000) return null
    return entry
  } catch {
    return null
  }
}

function writeCache(url: string, urls: string[], next: boolean) {
  try {
    localStorage.setItem(cacheKey(url), JSON.stringify({ saved: Date.now(), items: urls, next }))
  } catch {
    // localStorage 满了或被禁用：整层静默失效，退化成每次都真调。
  }
}

async function loadFromKlipy(target: number, reset: boolean) {
  const url = klipyUrl(target)
  if (!url) {
    needConfig.value = true
    error.value = '表情包源尚未配置'
    return
  }
  const cacheDays = cfg.value?.klipy?.cache_days ?? 0
  const hit = reset ? readCache(url, cacheDays) : null
  if (hit) {
    // 缓存只落在访客自己的浏览器里：请求仍是终端发出的，只是同一天里同一查询不重复发。
    applyItems(hit.items, reset)
    klipyNext.value = hit.next
    page.value = target
    return
  }

  let response: Response
  try {
    response = await fetch(url, { credentials: 'omit', referrerPolicy: 'no-referrer' })
  } catch {
    error.value = '连不上 KLIPY：可能被网络策略或广告拦截挡住'
    return
  }
  if (response.status === 401 || response.status === 403) {
    error.value = 'KLIPY 拒绝了这次请求：请核对后台填的 App Key 与已授权域名'
    return
  }
  const payload = await response.json().catch(() => null)
  const list = payload ? findList(payload) : null
  if (!list) {
    error.value = response.ok
      ? 'KLIPY 正常返回，但没有解析出结果（可能是密钥对应的产品 / 授权域名不对）'
      : `KLIPY 返回异常（HTTP ${response.status}）`
    return
  }
  const urls = list
    .map((entry) => pickUrl(entry))
    .filter((item): item is string => !!item)
  if (!urls.length) {
    error.value = 'KLIPY 返回了结果，但取不到图片地址'
    return
  }
  applyItems(urls, reset)
  klipyNext.value = klipyHasNext(payload)
  page.value = target
  if (!klipyNext.value) exhausted.value = true
  // 只缓存「HTTP 200 且解析出条目」的：空结果缓存没意义，也会把一次异常记成结论。
  if (response.ok) writeCache(url, urls, klipyNext.value)
}

/* ── 滚动加载 ─────────────────────────────────────────────────── */

const scrollBox = ref<HTMLElement | null>(null)
const loadingMore = ref(false)

function nearBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight < 120
}

async function loadMoreIfNeeded() {
  if (loading.value || loadingMore.value) return
  if (!hasMore.value) return
  loadingMore.value = true
  try {
    await load(false)
  } finally {
    loadingMore.value = false
  }
}

function onScroll(event: Event) {
  const el = event.target as HTMLElement | null
  if (el && nearBottom(el)) void loadMoreIfNeeded()
}

function search() {
  resetState()
  void load(true)
}

function signIn() {
  openAuthDialog('login', { reason: '登录后才能搜索表情包' })
}

onMounted(async () => {
  try {
    await loadConfig()
  } catch (cause) {
    describe(cause)
    return
  }
  if (cfg.value?.ready) await load(true)
})
</script>

<template>
  <div class="gif-panel">
    <form class="gif-search" @submit.prevent="search">
      <input
        v-model="query" type="search" maxlength="10"
        :placeholder="placeholder" aria-label="搜索 GIF 表情包"
        @keyup.enter="search"
      >
      <button type="submit" :disabled="loading" aria-label="搜索">
        <ImIcon name="search" :size="17" />
      </button>
    </form>

    <div ref="scrollBox" class="gif-scroll" @scroll.passive="onScroll">
      <p v-if="error" class="gif-msg" role="status">
        {{ error }}
        <button v-if="needLogin" type="button" class="gif-link" @click="signIn">去登录</button>
        <span v-if="needConfig" class="gif-sub">后台「功能开关 → GIF 表情包」填写图源密钥后可用</span>
      </p>
      <div v-else-if="!items.length && loading" class="gif-grid">
        <span v-for="n in 8" :key="n" class="gif-skeleton" />
      </div>
      <p v-else-if="!items.length && loadedOnce" class="gif-msg">没有结果，换个关键词试试</p>

      <div v-if="items.length" class="gif-grid">
        <button
          v-for="url in items" :key="url" type="button" class="gif-cell"
          aria-label="发送这张 GIF" @click="emit('pick', url)"
        >
          <img :src="url" alt="" loading="lazy" decoding="async">
        </button>
      </div>

      <!-- 无限加载：到底自动续，这一行只是在续接时给个「在加载」的反馈。 -->
      <p v-if="items.length && hasMore" class="gif-more">
        {{ loading || loadingMore ? '加载中…' : '继续下滑加载更多' }}
      </p>
      <p v-else-if="items.length" class="gif-more">没有更多了</p>

      <!-- KLIPY 集成要求：搜索框占位固定 "Search KLIPY"（必需），Powered by 可选。 -->
      <p v-if="isKlipy" class="gif-credit">
        Powered by
        <a href="https://klipy.com" target="_blank" rel="noopener nofollow">KLIPY</a>
      </p>
    </div>
  </div>
</template>

<style scoped>
.gif-panel { display: flex; flex-direction: column; min-height: 0; height: 100%; }
.gif-search {
  display: flex;
  gap: 6px;
  padding: 8px 8px 6px;
  border-bottom: 1px solid var(--vp-im-glass-border);
}
.gif-search input {
  flex: 1;
  min-width: 0;
  height: 32px;
  padding: 0 12px;
  border: 1px solid var(--vp-im-border);
  border-radius: 999px;
  background: var(--vp-im-surface-raised);
  color: var(--vp-im-text-primary);
  font-size: 12px;
  outline: none;
}
.gif-search input:focus { border-color: var(--vp-im-accent); }
.gif-search button {
  display: grid;
  place-items: center;
  flex: 0 0 auto;
  width: 34px;
  height: 32px;
  border: 0;
  border-radius: 999px;
  background: var(--vp-im-surface-raised);
  color: var(--vp-im-text-secondary);
  cursor: pointer;
}
.gif-search button:hover:not(:disabled) { color: var(--vp-im-text-primary); }
.gif-search button:disabled { opacity: 0.5; cursor: not-allowed; }
.gif-scroll { flex: 1; min-height: 0; overflow-y: auto; padding: 8px; overscroll-behavior: contain; }
.gif-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 4px; }
.gif-cell {
  padding: 0;
  border: 1px solid transparent;
  border-radius: 8px;
  background: var(--vp-im-surface-raised);
  overflow: hidden;
  aspect-ratio: 1;
  cursor: pointer;
}
.gif-cell img { width: 100%; height: 100%; display: block; object-fit: cover; }
.gif-cell:hover { border-color: var(--vp-im-accent); }
/* 加载占位：与结果同尺寸的灰块，避免面板高度在空/有结果之间跳动。 */
.gif-skeleton {
  display: block;
  aspect-ratio: 1;
  border-radius: 8px;
  background: linear-gradient(100deg, rgba(255, 255, 255, 0.06), rgba(255, 255, 255, 0.12), rgba(255, 255, 255, 0.06));
  background-size: 200% 100%;
  animation: gifSweep 1.2s linear infinite;
}
@keyframes gifSweep {
  from { background-position: 200% 0; }
  to { background-position: -200% 0; }
}
.gif-msg {
  display: grid;
  gap: 6px;
  justify-items: center;
  margin: 0;
  padding: 24px 12px;
  font-size: 12px;
  line-height: 1.6;
  color: var(--vp-im-text-secondary);
  text-align: center;
}
.gif-sub { color: var(--vp-im-text-secondary); opacity: 0.75; }
.gif-link {
  border: 0;
  background: none;
  padding: 0;
  font-size: 12px;
  color: var(--vp-im-accent);
  cursor: pointer;
}
.gif-more {
  margin: 6px 0 2px;
  padding: 4px 0;
  font-size: 11px;
  color: var(--vp-im-text-secondary);
  text-align: center;
  opacity: 0.75;
}
.gif-credit {
  margin: 2px 0 4px;
  font-size: 10px;
  color: var(--vp-im-text-secondary);
  text-align: center;
  opacity: 0.7;
}
.gif-credit a { color: inherit; text-decoration: none; }
.gif-credit a:hover { text-decoration: underline; }
</style>
