<script setup lang="ts">
// M2 沉浸流主界面（Page 1 / UI 规范 §5、§6）。
// 单例 <video> 在切条时被移动到当前条目的媒体槽；只渲染当前及前后各一条。
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { FeedItem } from '~/composables/useFeed'
import type { MediaWindow } from '~/composables/useImmersivePlayer'
import { ApiError, errorMessage } from '~/composables/useApi'
const { api } = useApi()

const { list, detail, following, follow, avatar, tags, caption, formatCount } = useFeed()
const { state, applyServer, setFollowing, toggleLike, toggleFavorite } = useInteractions()
const { open: openAuthDialog } = useAuthDialog()
// 列表只带签名摘要，预载窗口内的 HLS 条目要补一次详情才能拿到 master playlist。
const player = useImmersivePlayer({
  resolveMedia: async (item) => (await detail(item.slug)).media,
})
// 观看进度上报（§9.1）：当前条目播放中每 10 秒补报，隐藏/离开立即补报。
const reporter = useProgressReporter(player, () => (currentItem.value ? { id: currentItem.value.id, type: currentItem.value.type } : null))
const { preferences, load: loadPreferences } = usePreferences()

const route = useRoute()
const items = ref<FeedItem[]>([])
const current = ref(0)
// 桌面发布页「朋友」导航用 /?channel=follow 直达关注流（UI 规范 §8.1）。
const channel = ref<'follow' | 'recommend'>(route.query.channel === 'follow' ? 'follow' : 'recommend')
const loading = ref(true)
const errorText = ref('')
const sheetOpen = ref(false)
// 评论抽屉回传服务端口径的总数，写回当前条目供操作栏角标使用。
function onCommentCount(total: number) {
  if (currentItem.value) currentItem.value.stats.comments = total
}

const reportOpen = ref(false)
function onReported() {
  toast.value = '举报已提交，处置结果将在「我的举报」中反馈'
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 2600)
}
const expanded = ref(false)
// 控制条上的播放选项（§6.4）：连播 / 清屏都是纯前端开关，先把状态与视觉跑通。
const barAutoplay = ref(true)
const barClear = ref(false)
function toggleBarAutoplay() {
  barAutoplay.value = !barAutoplay.value
  player.setAutoplay(barAutoplay.value)
}
const toast = ref('')
const scroller = ref<HTMLElement | null>(null)

let observer: IntersectionObserver | null = null
let activation = 0
let toastTimer: ReturnType<typeof setTimeout> | null = null

function notify(message: string) {
  toast.value = message
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 2400)
}

const currentItem = computed<FeedItem | null>(() => items.value[current.value] || null)

// 当前视频是否已放进「晚点再看」：接口是幂等 toggle，必须先知道初始状态，
// 否则按钮文案永远显示「晚点再看」，第二次点击会把视频悄悄移出。
const watchlisted = ref(false)

// 底栏收起状态：视频跟随播放（播放收起、暂停恢复）；图片没有播放语义，
// 默认收起保持沉浸，点画面可唤出（唤出后再点收起）。移动端由 CSS 断点忽略该状态。
const imageNavPinned = ref(false)
// 切条瞬间旧视频会先 pause（playing=false），新视频加载完再 play（true）。
// 如果不锁住，底栏会在滑动时弹出来再收回去，所以切换期间强制收起，
// 等新内容 ready / failed（或 3s 兜底）后交还给真实播放状态。
const switching = ref(false)
let switchTimer: ReturnType<typeof setTimeout> | null = null
const chromeCollapsed = ref(true)
let chromeTimer: ReturnType<typeof setTimeout> | null = null

const rawCollapsed = computed(() => {
  const item = currentItem.value
  if (!item) return chromeCollapsed.value
  if (item.type === 'image') return !imageNavPinned.value
  if (switching.value || !player.state.ready) return true
  return player.state.playing
})

function toggleImageChrome() {
  if (currentItem.value?.type === 'image') imageNavPinned.value = !imageNavPinned.value
}

// 收起立即生效（别让底栏在滑动手势里露一下）；展开延迟 120ms 吸收播放器的瞬时抖动。
watch(rawCollapsed, (next) => {
  if (chromeTimer) clearTimeout(chromeTimer)
  if (next) {
    chromeCollapsed.value = true
    return
  }
  chromeTimer = setTimeout(() => { chromeCollapsed.value = false }, 120)
})

// 切到另一条内容：图片回到默认收起，进入切换窗口。
watch(current, () => {
  imageNavPinned.value = false
  switching.value = true
  if (switchTimer) clearTimeout(switchTimer)
  switchTimer = setTimeout(() => { switching.value = false }, 3000)
}, { flush: 'sync' })

// 新条目转码就绪或失败即结束切换窗口；失败时也要让用户能唤出底栏。
watch([() => player.state.ready, () => player.state.failed], ([ready, failed]) => {
  if (!ready && !failed) return
  switching.value = false
  if (switchTimer) clearTimeout(switchTimer)
})

// 点赞 / 收藏落库（§6.2）：接口成功才更新乐观状态与角标；未登录是预期场景，给引导不报错。
async function onLike(item: FeedItem) {
  try {
    const liked = await toggleLike(item.slug, item.id)
    item.stats.likes = Math.max(0, item.stats.likes + (liked ? 1 : -1))
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可点赞' })
      return
    }
    notify(errorMessage(error))
  }
}

async function loadWatchlisted(item: FeedItem, ticket: number) {
  watchlisted.value = false
  if (item.type !== 'video') return
  try {
    const data = await api<{ saved: boolean }>(`/api/v1/contents/${item.id}/watchlist`)
    if (ticket !== activation) return
    watchlisted.value = data.saved
  } catch { /* 游客态：保持未加入，点击时会唤起登录弹窗 */ }
}

async function onWatchLater(item: FeedItem) {
  try {
    const data = await api<{ saved: boolean }>(`/api/v1/contents/${item.id}/watchlist`, { method: 'POST' })
    watchlisted.value = data.saved
    notify(data.saved ? '已放入晚点再看' : '已从晚点再看移除')
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可把视频放进晚点再看' })
      return
    }
    notify(errorMessage(error))
  }
}

async function onFavorite(item: FeedItem) {
  try {
    const favorited = await toggleFavorite(item.slug, item.id)
    item.stats.favorites = Math.max(0, item.stats.favorites + (favorited ? 1 : -1))
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可收藏' })
      return
    }
    notify(errorMessage(error))
  }
}

// 预加载 / 保留窗口（§8.2）：用户定案「向下 3 / 向上 5」。
// 这是内存与流量的直接成本，所以按网络状况自动收缩：省流或 2G 只留上一条，
// 3G 折半，其余按满窗口。CSS 断点无关——移动端同样遵守，只是窗口更保守。
function mediaWindow(): MediaWindow {
  const conn = (navigator as Navigator & {
    connection?: { saveData?: boolean; effectiveType?: string }
  }).connection
  if (conn?.saveData) return { forward: 0, backward: 1 }
  const type = conn?.effectiveType || ''
  if (type.includes('2g')) return { forward: 0, backward: 1 }
  if (type.includes('3g')) return { forward: 1, backward: 2 }
  return { forward: 3, backward: 5 }
}

function preloadNeighbours() {
  if (!import.meta.client) return
  const { forward, backward } = mediaWindow()
  // 封面只服务渲染窗口（±1），多备一张防快速连滑；按视频窗口铺开纯属白下载。
  const ahead = Math.min(forward, 2)
  const behind = Math.min(backward, 2)
  for (let i = current.value - behind; i <= current.value + ahead; i += 1) {
    if (i === current.value) continue
    const url = items.value[i]?.media.poster_url
    if (!url) continue
    const image = new Image()
    image.src = url
  }
}

async function activateCurrent() {
  const item = items.value[current.value]
  if (!item) return
  const ticket = ++activation
  expanded.value = false
  player.pause()
  // 沉浸流内滑动不再同步 URL（§5.1 定案修订）：地址栏保持 /，刷新回沉浸流，
  // 滑动体验不丢。单视频深链接（/video/{slug}）由详情页与分享面板承载。

  // 列表只带签名摘要；当前条目再取一次详情以拿到重写后的 HLS 播放列表。
  try {
    const full = await detail(item.slug)
    if (ticket !== activation) return
    items.value[current.value] = {
      ...item,
      media: full.media,
      images: full.images.length ? full.images : item.images,
    }
  } catch {
    if (ticket !== activation) return
  }

  const active = items.value[current.value]
  if (!active || ticket !== activation) return
  const root = scroller.value
  const slot = root?.querySelector<HTMLElement>(`[data-feed-index="${current.value}"] .media-slot`)
  player.attach(slot || null)
  await player.load(active)
  void loadWatchlisted(active, ticket)
  // 当前条目已就位，再铺开预载窗口：先播当前，后台慢速补周边。
  player.prepare(items.value, current.value, mediaWindow())
  preloadNeighbours()
}

function setupObserver() {
  const root = scroller.value
  if (!root || observer) return
  observer = new IntersectionObserver((entries) => {
    let best: { index: number; ratio: number } | null = null
    for (const entry of entries) {
      const index = Number((entry.target as HTMLElement).dataset.feedIndex)
      if (!best || entry.intersectionRatio > best.ratio) best = { index, ratio: entry.intersectionRatio }
    }
    if (!best) return
    if (best.ratio >= 0.9 && best.index !== current.value) {
      current.value = best.index
      void activateCurrent()
    } else if (best.ratio < 0.9) {
      player.pause()
    }
  }, { root, threshold: [0, 0.5, 0.75, 0.9, 1] })
  root.querySelectorAll('[data-feed-index]').forEach((element) => observer?.observe(element))
}

function go(index: number) {
  const root = scroller.value
  if (!root) return
  const target = Math.min(Math.max(index, 0), items.value.length - 1)
  const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  root.scrollTo({ top: target * root.clientHeight, behavior: reduced ? 'auto' : 'smooth' })
}

function onKeydown(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null
  if (target?.matches('input, textarea, [contenteditable="true"]')) return
  if (sheetOpen.value) return
  // 方向键统一交给播放器：左右 ±5 秒、上下 ±5% 音量。
  // 沉浸流切视频仍可用滚轮 / 触摸滑动，不再占用上下键。
  if (handlePlayerArrowKeys(event, player)) return
  switch (event.key) {
    case ' ': event.preventDefault(); void player.toggle(); break
    case 'm': case 'M': player.setMuted(!player.state.muted); break
    case 'f': case 'F': void player.toggleFullscreen(); break
  }
}

function onVisibility() {
  if (document.hidden) player.pause()
}

function reload() {
  if (import.meta.client) window.location.reload()
}

async function onShare(item: FeedItem) {
  const path = item.type === 'image' ? `/image/${item.slug}` : `/video/${item.slug}`
  try {
    await navigator.clipboard.writeText(`${location.origin}${path}`)
    notify('深链接已复制')
  } catch {
    notify(`分享链接：${path}`)
  }
}

// 关注以服务端为准：登录后按 /me/following 覆盖本地缓存，
// 避免旧 localStorage 里「已关注」的假状态继续显示。
async function hydrateFollows() {
  try {
    const handles = new Set(await following())
    for (const entry of items.value) setFollowing(entry.slug, handles.has(entry.author.handle))
  } catch {
    // 游客或接口暂时不可用：保留本地状态，不阻塞播放。
  }
}

async function onFollow(item: FeedItem) {
  const next = !state(item.slug).following
  try {
    const result = await follow(item.author.handle, next)
    // 同一作者可能有多条作品，关注状态要一起同步。
    for (const entry of items.value) {
      if (entry.author.handle === item.author.handle) setFollowing(entry.slug, result.following)
    }
    notify(result.following ? `已关注 @${item.author.handle}` : `已取消关注 @${item.author.handle}`)
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可关注作者' })
      return
    }
    notify(errorMessage(error))
  }
}

async function loadFeed(scope: 'recommend' | 'follow') {
  loading.value = true
  errorText.value = ''
  try {
    const response = await list(12, '', scope)
    items.value = response.items
    current.value = 0
    if (scope === 'follow' && !response.items.length) {
      notify('关注流还没有作品，先去关注几位作者。')
    }
    // 服务端互动标记覆盖本地乐观状态（§6.2）：换列表后点赞 / 收藏以服务端为准。
    for (const item of response.items) applyServer(item.slug, !!item.liked, !!item.favorited)
    await hydrateFollows()
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      notify('登录后才能查看关注流')
      channel.value = 'recommend'
      return
    }
    items.value = []
    errorText.value = errorMessage(error)
  } finally {
    loading.value = false
  }
  // 列表整体换掉后旧的观察目标已失效，必须重建。
  observer?.disconnect()
  observer = null
  await nextTick()
  setupObserver()
  if (items.value.length) await activateCurrent()
}

watch(channel, async (value, previous) => {
  if (value === previous) return
  await loadFeed(value)
})

onMounted(() => {
  reporter.start()
})

onMounted(async () => {
  // 自动播放开关必须在首次激活前就位，所以先读偏好再拉列表。
  await loadPreferences()
  player.setAutoplay(preferences.value.autoplay)
  await loadFeed(channel.value)
  window.addEventListener('keydown', onKeydown)
  document.addEventListener('visibilitychange', onVisibility)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
  player.destroy()
  window.removeEventListener('keydown', onKeydown)
  document.removeEventListener('visibilitychange', onVisibility)
  if (import.meta.client) window.history.replaceState(window.history.state, '', '/')
})
</script>

<template>
  <div class="feed-root" data-surface="immersive">
    <ChromeBars v-model:channel="channel" :collapsed="chromeCollapsed" />

    <div ref="scroller" class="scroller" :inert="sheetOpen || undefined">
      <div
        v-for="(item, index) in items" :key="item.id"
        class="snap" :data-feed-index="index"
      >
        <ImmersiveItem
          v-if="Math.abs(index - current) <= 1"
          :item="item" :active="index === current" :avatar="avatar(item)"
          :liked="state(item.slug).liked" :favorited="state(item.slug).favorited"
          :following="state(item.slug).following"
          :counts="item.stats" :label="formatCount" :player="player.state"
          :caption="caption(item.description)" :tags="tags(item.description)"
          :expanded="expanded" :watchlisted="watchlisted"
          :autoplay="barAutoplay" :clear-ui="barClear" :nav-hidden="chromeCollapsed"
          @toggle="player.toggle()" @like="onLike(item)"
          @toggle-chrome="toggleImageChrome"
          @comment="sheetOpen = true" @favorite="onFavorite(item)"
          @share="onShare(item)" @follow="onFollow(item)" @report="reportOpen = true"
          @watch-later="onWatchLater(item)"
          @expand="expanded = !expanded" @mute-toggle="player.setMuted(!player.state.muted)"
          @seek="player.seekRatio($event)"
          @rate="player.setRate($event)" @level="player.setLevel($event)"
          @volume="player.setVolume($event)"
          @autoplay-toggle="toggleBarAutoplay" @clear-toggle="barClear = !barClear"
          @pip="player.requestPiP()" @fullscreen="player.toggleFullscreen()"
          @notice="notify($event)"
          @retry="currentItem && player.load(currentItem)"
        />
        <div v-else class="placeholder">
          <img v-if="item.media.poster_url" :src="item.media.poster_url" alt="" loading="lazy" />
        </div>
      </div>
    </div>

    <div v-if="loading" class="cover-state" role="status">
      <div class="skeleton" />
      <p>正在装载夜街…</p>
    </div>
    <div v-else-if="errorText" class="cover-state">
      <p>{{ errorText }}</p>
      <button type="button" class="state-action" @click="reload()">重试</button>
    </div>
    <div v-else-if="!items.length" class="cover-state">
      <h1>夜街还没有作品</h1>
      <p>发布第一条视频或图集，让这里亮起来。</p>
      <NuxtLink to="/publish" class="state-action">去发布</NuxtLink>
    </div>

    <CommentSheet
      :open="sheetOpen" :content-id="currentItem?.id ?? 0" :count="currentItem?.stats.comments ?? 0"
      @close="sheetOpen = false"
      @count-change="onCommentCount"
    />

    <ReportDialog
      :open="reportOpen" :content-id="currentItem?.id ?? 0"
      :content-title="currentItem?.title || ''"
      @close="reportOpen = false"
      @reported="onReported"
    />

    <div v-if="toast" class="toast vp-glass vp-glass-compact" role="status">{{ toast }}</div>
  </div>
</template>

<style scoped>
.feed-root { position: relative; height: 100dvh; overflow: hidden; background: var(--vp-im-bg); }
.scroller {
  height: 100dvh;
  overflow-y: auto;
  scroll-snap-type: y mandatory;
  scrollbar-width: none;
  overscroll-behavior-y: contain;
}
.scroller::-webkit-scrollbar { display: none; }
.snap { height: 100dvh; scroll-snap-align: start; }
.placeholder { height: 100%; display: grid; place-items: center; background: var(--vp-im-bg); }
.placeholder img { max-width: 100%; max-height: 100%; object-fit: contain; opacity: 0.5; }
.cover-state {
  position: fixed;
  inset: 0;
  z-index: var(--vp-im-z-dialog);
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 12px;
  padding: 24px;
  text-align: center;
  background: var(--vp-im-bg);
}
.cover-state h1 { margin: 0; font-size: 22px; }
.cover-state p { margin: 0; font-size: 13px; color: var(--vp-im-text-secondary); }
.skeleton { width: 180px; height: 260px; border-radius: var(--vp-radius-media); background: var(--vp-im-surface-raised); }
.state-action {
  margin-top: 8px;
  padding: 10px 20px;
  border: 0;
  border-radius: 999px;
  background: var(--vp-im-accent);
  color: var(--vp-im-on-accent);
  font-size: 13px;
  text-decoration: none;
  cursor: pointer;
}
.toast {
  position: fixed;
  left: 50%;
  /* 让开播放控制条（68–118），提示不要盖住弹幕输入框。 */
  bottom: calc(132px + env(safe-area-inset-bottom));
  transform: translateX(-50%);
  z-index: var(--vp-im-z-toast);
  padding: 10px 18px;
  border-radius: 999px;
  font-size: 12px;
  white-space: nowrap;
}
</style>
