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
const maxPage = ref(1)
const loading = ref(false)
const error = ref('')
const needLogin = ref(false)
const needConfig = ref(false)
const loadedOnce = ref(false)

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
    items.value = reset ? data.items : [...items.value, ...data.items]
    page.value = data.page || target
    maxPage.value = data.max_page || 1
    loadedOnce.value = true
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

function search() {
  items.value = []
  page.value = 1
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

    <div class="gif-scroll">
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

      <button
        v-if="items.length && page < maxPage" type="button" class="gif-more"
        :disabled="loading" @click="load(false)"
      >{{ loading ? '加载中…' : '加载更多' }}</button>
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
.gif-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 6px; }
.gif-cell {
  padding: 0;
  border: 1px solid transparent;
  border-radius: 10px;
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
  border-radius: 10px;
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
  display: block;
  width: 100%;
  margin: 8px 0 2px;
  padding: 7px 0;
  border: 1px solid var(--vp-im-glass-border);
  border-radius: 8px;
  background: none;
  color: var(--vp-im-text-secondary);
  font-size: 12px;
  cursor: pointer;
}
.gif-more:disabled { opacity: 0.5; cursor: not-allowed; }
</style>
