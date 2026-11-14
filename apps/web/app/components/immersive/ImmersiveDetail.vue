<script setup lang="ts">
// 深链接落地页共用视图：/video/[slug] 与 /image/[slug] 都在这里渲染。
// SSR 通过 Nitro 内部同源代理取详情，刷新与分享都能定位到同一条目。
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ApiError, errorMessage } from '~/composables/useApi'
import type { FeedItem } from '~/composables/useFeed'

interface SEOPage {
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

const props = defineProps<{ slug: string; expectedType: 'video' | 'image' }>()

const { data, error } = await useAsyncData(`feed-detail-${props.slug}`, () =>
  $fetch<{ data: FeedItem }>(`/api/v1/feed/${encodeURIComponent(props.slug)}`))

const item = computed<FeedItem | null>(() => data.value?.data || null)


const deepLink = computed(() => item.value
  ? (item.value.type === 'image' ? `/image/${item.value.slug}` : `/video/${item.value.slug}`)
  : '')

// SEO 元数据统一由 Go 公开接口提供，服务端 await 完成后再写 head（§11.2）。
const { data: seoPayload } = await useAsyncData(`seo-${props.slug}`, () =>
  $fetch<{ data: SEOPage }>(`/api/v1/seo/content/${encodeURIComponent(props.slug)}`).catch(() => null))
const seoPage = computed<SEOPage | null>(() => seoPayload.value?.data || null)
const indexable = computed(() => !!seoPage.value?.indexable && !!item.value)
// 私密 / 未审核 / 失效内容返回正确状态码，避免「200 但无内容」的软 404（§11.1 / §11.3）。
const requestEvent = useRequestEvent()
if (requestEvent && (error.value || !indexable.value)) {
  requestEvent.node.res.statusCode = 404
}
// SEO 接口已给出绝对 canonical；回退时同样按请求头推导 origin。
const siteOrigin = useSiteOrigin()
const canonical = computed(() => seoPage.value?.canonical || `${siteOrigin}${deepLink.value || '/'}`)

const jsonLd = computed(() => {
  const page = seoPage.value
  if (!page) return []
  const creator = { '@type': 'Person', name: page.handle }
  if (page.type === 'image') {
    return (page.images || []).map((url, index) => ({
      '@context': 'https://schema.org',
      '@type': 'ImageObject',
      name: page.images && page.images.length > 1 ? `${page.title}（${index + 1}/${page.images.length}）` : page.title,
      contentUrl: url,
      thumbnailUrl: url,
      uploadDate: page.published_at,
      description: page.description,
      creator,
    }))
  }
  return [{
    '@context': 'https://schema.org',
    '@type': 'VideoObject',
    name: page.title,
    description: page.description,
    thumbnailUrl: page.thumbnail_url ? [page.thumbnail_url] : [],
    uploadDate: page.published_at,
    duration: page.duration_iso,
    contentUrl: page.content_url,
    creator,
  }]
})

useSeoMeta({
  title: () => seoPage.value ? `${seoPage.value.title} · 夜街` : '夜街',
  description: () => seoPage.value?.description || '夜街 · 沉浸式短视频',
  ogTitle: () => seoPage.value?.title || '夜街',
  ogDescription: () => seoPage.value?.description || '',
  ogImage: () => seoPage.value?.thumbnail_url || '',
  ogType: () => seoPage.value?.type === 'video' ? 'video.other' : 'article',
  twitterCard: 'summary_large_image',
  twitterTitle: () => seoPage.value?.title || '夜街',
  twitterDescription: () => seoPage.value?.description || '',
  twitterImage: () => seoPage.value?.thumbnail_url || '',
})

useHead(() => ({
  link: [{ rel: 'canonical', href: canonical.value }],
  meta: [{ name: 'robots', content: indexable.value ? 'index,follow' : 'noindex,nofollow' }],
  script: [{ type: 'application/ld+json', innerHTML: JSON.stringify(jsonLd.value) }],
}))

const { avatar, tags, caption, formatCount } = useFeed()
const { state, applyServer, toggleLike, toggleFavorite, toggleFollow } = useInteractions()
const { open: openAuthDialog } = useAuthDialog()
const { api } = useApi()
const player = useImmersivePlayer()
// 服务端互动标记覆盖默认未点亮（§6.2）：详情带当前查看者的点赞 / 收藏。
if (item.value) applyServer(item.value.slug, !!item.value.liked, !!item.value.favorited)
// 观看进度（§9.1）：播放中定期上报，隐藏/离开补报；进入详情时取最近位置续播。
const reporter = useProgressReporter(player, () => (item.value ? { id: item.value.id, type: item.value.type } : null))

// 底栏收起状态：视频跟随播放；图片默认收起，点画面唤出。移动端由 CSS 断点忽略。
// 加载期（ready=false、playing 尚未起来）一律先收起，避免底栏出现再消失地闪一下。
const imageNavPinned = ref(false)
const chromeCollapsed = ref(true)
let chromeTimer: ReturnType<typeof setTimeout> | null = null
const rawCollapsed = computed(() => {
  if (!item.value) return chromeCollapsed.value
  if (item.value.type === 'image') return !imageNavPinned.value
  if (!player.state.ready) return true
  return player.state.playing
})
watch(rawCollapsed, (next) => {
  if (chromeTimer) clearTimeout(chromeTimer)
  if (next) {
    chromeCollapsed.value = true
    return
  }
  chromeTimer = setTimeout(() => { chromeCollapsed.value = false }, 120)
})
function toggleImageChrome() {
  if (item.value?.type === 'image') imageNavPinned.value = !imageNavPinned.value
}
watch(() => item.value?.id, () => { imageNavPinned.value = false })
let resumed = false
const watchlisted = ref(false)
watch(() => item.value?.id, () => { resumed = false; void loadWatchlist() })

const sheetOpen = ref(false)
const reportOpen = ref(false)
// 评论抽屉回传服务端口径的总数，写回条目供操作栏角标使用。
function onCommentCount(total: number) {
  if (item.value) item.value.stats.comments = total
}
watch(() => player.state.ready, async (ready) => {
  if (!ready || resumed || !item.value || item.value.type !== 'video') return
  resumed = true
  reporter.start()
  try {
    const data = await api<{ position_ms: number; duration_ms: number }>(
      `/api/v1/contents/${item.value.id}/progress`)
    const ratio = data.duration_ms > 0 ? data.position_ms / data.duration_ms : 0
    // 起头 15 秒内或看完 95% 以上不续播，避免「从头看」被劫持。
    if (data.position_ms > 15_000 && ratio < 0.95) player.seekRatio(ratio)
  } catch { /* 匿名/无记录：直接从头播 */ }
})

async function loadWatchlist() {
  watchlisted.value = false
  if (!item.value) return
  try {
    const data = await api<{ saved: boolean }>(`/api/v1/contents/${item.value.id}/watchlist`)
    watchlisted.value = data.saved
  } catch { /* 匿名：默认未保存 */ }
}

// 互动状态（§6.2）：SSR 详情取数不带查看者会话，挂载后按会话补一次服务端真相
// （点赞 / 收藏标记 + 真实计数），与晚点再看的取数口径一致。
async function loadReactions() {
  if (!item.value) return
  try {
    const data = await api<{ likes: number; favorites: number; liked: boolean; favorited: boolean }>(
      `/api/v1/contents/${item.value.id}/reactions`)
    applyServer(item.value.slug, data.liked, data.favorited)
    item.value.stats.likes = data.likes
    item.value.stats.favorites = data.favorites
  } catch { /* 匿名：保持 SSR 计数与未点亮 */ }
}

async function toggleWatchlist() {
  if (!item.value) return
  try {
    const data = await api<{ saved: boolean }>(`/api/v1/contents/${item.value.id}/watchlist`, { method: 'POST' })
    watchlisted.value = data.saved
    notify(data.saved ? '已放入晚点再看' : '已从晚点再看移除')
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可把视频放进晚点再看' })
      return
    }
    else notify(errorMessage(error))
  }
}

const expanded = ref(false)
const toast = ref('')
const slot = ref<HTMLElement | null>(null)
let toastTimer: ReturnType<typeof setTimeout> | null = null

function notify(message: string) {
  toast.value = message
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => { toast.value = '' }, 2400)
}

// 点赞 / 收藏落库（§6.2）：接口成功才更新乐观状态与角标；未登录是预期场景，给引导不报错。
async function onLike(entry: FeedItem) {
  try {
    const liked = await toggleLike(entry.slug, entry.id)
    entry.stats.likes = Math.max(0, entry.stats.likes + (liked ? 1 : -1))
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可点赞' })
      return
    }
    notify(errorMessage(error))
  }
}

async function onFavorite(entry: FeedItem) {
  try {
    const favorited = await toggleFavorite(entry.slug, entry.id)
    entry.stats.favorites = Math.max(0, entry.stats.favorites + (favorited ? 1 : -1))
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可收藏' })
      return
    }
    notify(errorMessage(error))
  }
}

function onKeydown(event: KeyboardEvent) {
  const target = event.target as HTMLElement | null
  if (target?.matches('input, textarea, [contenteditable="true"]') || sheetOpen.value) return
  if (event.key === ' ') { event.preventDefault(); void player.toggle() }
  if (event.key === 'm' || event.key === 'M') player.setMuted(!player.state.muted)
  if (event.key === 'f' || event.key === 'F') void player.toggleFullscreen()
}

onMounted(async () => {
  void loadReactions()
  if (item.value && item.value.type === 'video') {
    player.attach(slot.value?.querySelector<HTMLElement>('.media-slot') || null)
    await player.load(item.value)
  }
  window.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  player.destroy()
  window.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="detail-root" data-surface="immersive">
    <ChromeBars :show-nav="true" :collapsed="chromeCollapsed" />

    <div v-if="error || !item" class="state">
      <h1>这条作品暂时看不到</h1>
      <p>可能尚未审核通过、已被设为私密，或链接有误。</p>
      <NuxtLink to="/" class="action">返回沉浸流</NuxtLink>
    </div>

    <div v-else-if="item.type !== props.expectedType" class="state">
      <h1>作品类型与链接不符</h1>
      <p>这条内容应从对应类型的深链接打开。</p>
      <NuxtLink :to="deepLink" class="action">前往正确地址</NuxtLink>
    </div>

    <template v-else>
      <div class="snap" :inert="sheetOpen || undefined">
        <div ref="slot" class="detail-slot">
          <ImmersiveItem
            :item="item" :active="true" :avatar="avatar(item)"
            :liked="state(item.slug).liked" :favorited="state(item.slug).favorited"
            :following="state(item.slug).following"
            :counts="item.stats" :label="formatCount" :player="player.state"
            :caption="caption(item.description)" :tags="tags(item.description)"
            :expanded="expanded" :nav-hidden="chromeCollapsed" :watchlisted="watchlisted"
            @toggle="player.toggle()" @like="onLike(item)"
            @toggle-chrome="toggleImageChrome"
            @comment="sheetOpen = true" @favorite="onFavorite(item)"
            @share="notify('深链接：' + deepLink)" @follow="toggleFollow(item.slug)" @report="reportOpen = true"
            @watch-later="toggleWatchlist"
            @expand="expanded = !expanded" @mute-toggle="player.setMuted(!player.state.muted)"
            @seek="player.seekRatio($event)"
            @rate="player.setRate($event)" @level="player.setLevel($event)"
            @volume="player.setVolume($event)"
            @pip="player.requestPiP()" @fullscreen="player.toggleFullscreen()"
            @notice="notify($event)"
            @retry="player.load(item)"
          />
        </div>
      </div>

      <!-- 内容摘要：SSR 直出、可被抓取，避免遮挡沉浸画面（§11.2 字幕与转录双重覆盖）。 -->
      <section v-if="seoPage" class="sr-only" aria-label="内容摘要">
        <h2>{{ seoPage.title }} · 内容摘要</h2>
        <p>{{ seoPage.description }}</p>
        <p>作者 {{ seoPage.creator }}，发布于 {{ seoPage.published_at }}</p>
        <ul v-if="seoPage.images?.length">
          <li v-for="(url, index) in seoPage.images" :key="url">
            {{ seoPage.title }} 第 {{ index + 1 }} 张图：{{ url }}
          </li>
        </ul>
      </section>

      <CommentSheet
        :open="sheetOpen" :content-id="item.id" :count="item.stats.comments"
        @close="sheetOpen = false" @count-change="onCommentCount"
      />

      <ReportDialog
        :open="reportOpen" :content-id="item.id" :content-title="item.title"
        @close="reportOpen = false" @reported="notify('举报已提交，处置结果将在「我的举报」中反馈')"
      />
      <button
        v-if="item && item.type === 'video'" type="button"
        class="watchlist vp-glass vp-glass-compact"
        :aria-pressed="watchlisted"
        @click="toggleWatchlist"
      >{{ watchlisted ? '已在晚点再看' : '晚点再看' }}</button>

      <div v-if="toast" class="toast vp-glass vp-glass-compact" role="status">{{ toast }}</div>
    </template>
  </div>
</template>

<style scoped>
.detail-root { position: relative; height: 100dvh; background: var(--vp-im-bg); }
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}
.snap { height: 100dvh; }
.detail-slot { position: relative; height: 100%; }
.state {
  position: relative;
  z-index: var(--vp-im-z-dialog);
  display: grid;
  place-content: center;
  justify-items: center;
  gap: 10px;
  height: 100%;
  padding: 24px;
  text-align: center;
  background: var(--vp-im-bg);
}
.state h1 { margin: 0; font-size: 22px; }
.state p { margin: 0; font-size: 13px; color: var(--vp-im-text-secondary); }
.action {
  margin-top: 8px;
  padding: 10px 20px;
  border-radius: 999px;
  background: var(--vp-im-accent);
  color: var(--vp-im-on-accent);
  font-size: 13px;
  text-decoration: none;
}
.watchlist {
  position: absolute;
  top: calc(20px + env(safe-area-inset-top));
  right: 20px;
  padding: 7px 14px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary);
  font-size: 12px;
  cursor: pointer;
}
.watchlist[aria-pressed="true"] {
  border-color: var(--vp-im-accent);
  color: var(--vp-im-accent);
}
/* 窄屏顶栏右侧已经排了搜索与静音开关，这颗胶囊会直接压在它们上面
   （实测 390px 下胶囊 x292–370 与搜索 x330–374 重叠）。
   晚点再看在右侧「⋯」菜单里本来就有入口，手机上收起来即可。 */
@media (max-width: 780px) {
  .watchlist { display: none; }
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
}
</style>
