<script setup lang="ts">
// 评论表情面板的 GIF 页：上方搜索、下方结果网格，点一下把这条 GIF 交给父组件待发送。
//
// 图源由后台「功能开关」配置，搜索走本站代理（密钥不出站、结果按天缓存），
// 所以这里只认自家的 /api/v1/gifs/search，不直连任何第三方接口。
import { ApiError, errorMessage } from '~/composables/useApi'

const emit = defineEmits<{ pick: [url: string] }>()

const { api } = useApi()
const { open: openAuthDialog } = useAuthDialog()

const query = ref('')
const items = ref<string[]>([])
const page = ref(1)
// max_page = 0 表示上游没告诉我们总页数（API 盒子的热门接口就是这样），
// 这时不能当成「只有一页」，要一直加载到某一页返回空、或返回的全是已见过的图为止。
const maxPage = ref(0)
const exhausted = ref(false)
const loading = ref(false)
const error = ref('')
const needLogin = ref(false)
const needConfig = ref(false)
const loadedOnce = ref(false)

const hasMore = computed(() => {
  if (exhausted.value) return false
  if (maxPage.value > 0) return page.value < maxPage.value
  return items.value.length > 0
})

async function load(reset = true) {
  if (loading.value) return
  loading.value = true
  error.value = ''
  needLogin.value = false
  needConfig.value = false
  const target = reset ? 1 : page.value + 1
  try {
    const data = await api<{ items: string[]; page: number; max_page: number }>(
      `/api/v1/gifs/search?q=${encodeURIComponent(query.value.trim())}&page=${target}`,
    )
    const incoming = data.items || []
    // 去重：上游分页界限偶尔会把同一批图再给一遍（热门尤其明显），重复 key 会让列表抖。
    const known = new Set(items.value)
    const fresh = reset ? incoming : incoming.filter((url) => !known.has(url))
    if (reset) {
      items.value = incoming
      exhausted.value = false
    } else {
      // 空页或原地打转 = 没有更多了。上游不给 total 时，这是唯一的终止条件。
      if (!incoming.length || !fresh.length) exhausted.value = true
      items.value = [...items.value, ...fresh]
    }
    page.value = data.page || target
    maxPage.value = data.max_page || 0
    if (maxPage.value > 0 && page.value >= maxPage.value) exhausted.value = true
    loadedOnce.value = true
    // 一页填不满容器时滚不动，滚动事件永远不触发：补一次「撑满」检查。
    await nextTick()
    const el = scrollBox.value
    if (el && el.scrollHeight <= el.clientHeight + 4) void loadMoreIfNeeded()
  } catch (cause) {
    // 三种状态要分开说：没配源、没登录、真出错。混成一句「加载失败」等于让人无从下手。
    if (cause instanceof ApiError && cause.code === 'gif_not_configured') {
      needConfig.value = true
      error.value = '表情包源尚未配置'
    } else if (cause instanceof ApiError && (cause.status === 401 || cause.code === 'unauthorized')) {
      needLogin.value = true
      error.value = '登录后可以搜索 GIF'
    } else {
      error.value = errorMessage(cause)
    }
  } finally {
    loading.value = false
  }
}

// 滚到底自动接下一批（无限加载）：容器滚不动的一屏内容太少，所以用阈值提前 120px 触发，
// 避免用户看到「滑到底了但还是空的」那一下停顿。
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
  items.value = []
  page.value = 1
  maxPage.value = 0
  exhausted.value = false
  loadedOnce.value = false
  void load(true)
}

function signIn() {
  openAuthDialog('login', { reason: '登录后才能搜索表情包' })
}

// 面板一打开就拉一次热门（空关键词 = 热门），省掉「必须先输入」的门槛。
onMounted(() => { void load(true) })
</script>

<template>
  <div class="gif-panel">
    <form class="gif-search" @submit.prevent="search">
      <input
        v-model="query" type="search" maxlength="10"
        placeholder="搜索 GIF 表情包" aria-label="搜索 GIF 表情包"
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
        <span v-for="n in 6" :key="n" class="gif-skeleton" />
      </div>
      <p v-else-if="!items.length && loadedOnce" class="gif-msg">没有结果，换个关键词试试</p>

      <div v-if="items.length" class="gif-grid">
        <button
          v-for="url in items" :key="url" type="button" class="gif-cell"
          :aria-label="`发送这张 GIF`" @click="emit('pick', url)"
        >
          <img :src="url" alt="" loading="lazy" decoding="async">
        </button>
      </div>

      <!-- 无限加载：到底自动续，这一行只是在续接时给个「在加载」的反馈。 -->
      <p v-if="items.length && hasMore" class="gif-more">
        {{ loading || loadingMore ? '加载中…' : '继续下滑加载更多' }}
      </p>
      <p v-else-if="items.length" class="gif-more">没有更多了</p>
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
</style>
