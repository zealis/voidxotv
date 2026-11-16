<script setup lang="ts">
// 单条沉浸内容：媒体槽（单例播放器由页面挂载）、边缘渐变遮罩、操作栏、信息层、
// 播放器悬浮控件。画面之上没有任何玻璃遮罩（UI 规范 §4.2 / §5.1）。
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { FeedItem } from '~/composables/useFeed'
import { ApiError, errorMessage } from '~/composables/useApi'

interface PlayerView {
  playing: boolean
  muted: boolean
  loading: boolean
  failed: boolean
  ready: boolean
  currentTime: number
  duration: number
  message: string
  videoWidth: number
  videoHeight: number
  // 播放设置（§6.4 悬浮控件：倍速 / 清晰度 / 音量浮层）。
  rate: number
  volume: number
  levels: { index: number; label: string }[]
  level: number
}

const props = defineProps<{
  item: FeedItem
  active: boolean
  avatar: string
  liked: boolean
  favorited: boolean
  following: boolean
  expanded: boolean
  counts: { likes: number; comments: number; favorites: number; shares: number }
  label: (value: number) => string
  player: PlayerView
  caption: string
  tags: string[]
  /** 连播开关（沉浸流传入；详情页不传则隐藏连播/清屏按钮）。 */
  autoplay?: boolean
  /** 清屏开关（沉浸流传入；详情页不传则隐藏清屏按钮）。 */
  clearUi?: boolean
  /** 底栏是否收起：收起时控制条贴到屏幕底部，避免下方留一条空区。 */
  navHidden?: boolean
  /** 当前视频是否已放进「晚点再看」。 */
  watchlisted?: boolean
}>()

const emit = defineEmits<{
  toggle: []
  like: []
  comment: []
  favorite: []
  share: []
  follow: []
  watchLater: []
  report: []
  expand: []
  retry: []
  /** 切换静音：同一个键既取消静音也能重新静音。 */
  muteToggle: []
  seek: [ratio: number]
  rate: [value: number]
  level: [value: number]
  volume: [value: number]
  autoplayToggle: []
  clearToggle: []
  /** 图片浏览态点画面：唤出/收起底栏与控制条。 */
  toggleChrome: []
  qualityAuto: []
  pip: []
  danmakuToggle: []
  danmakuSend: [text: string]
  fullscreen: []
  /** 未实现条目的统一提示，由页面用 toast 呈现。 */
  notice: [text: string]
}>()

const flash = ref<'' | 'play' | 'pause' | 'like'>('')
// 沉浸流视频自适应（§5.1 全屏沉浸）：
//   双横屏（源与视口都是横屏）→ cover 裁两侧消除上下黑边（桌面横屏视口看
//   横屏源 contain 会留黑边，裁两侧 ≤20% 主体居中可接受）；
//   其余组合（手机竖屏看横屏源、桌面看竖屏源）→ contain 保留完整画面，
//   cover 会重裁（手机横屏源裁 ~70% / 桌面竖屏源裁上下过半），黑边可接受。
// SSR 阶段默认竖屏比例（产品主形态）。
const viewportRatio = ref(1.78)
// 弹幕已持久化到后端：挂载时按内容拉取，播放到对应时间点逐条触发，
// 视频 loop 回绕后重新播放，刷新页面重新拉取，不再依赖组件内存。
const { api } = useApi()
const { open: openAuthDialog } = useAuthDialog()

interface DanmakuItem {
  id: number
  text: string
  timeMs: number
  lane: number
}

const danmakuOn = ref(true)
const danmakuInput = ref('')
const danmakuInputEl = ref<HTMLInputElement | null>(null)
const danmakuEmojiOpen = ref(false)
const danmakuList = ref<DanmakuItem[]>([])
const danmakuPlaying = ref<{ key: number; item: DanmakuItem; bornAt: number }[]>([])
const danmakuLoaded = ref(false)
let danmakuSeq = 0
let lastDanmakuMs = -1
const firedDanmaku = new Set<number>()

function fireDanmaku(item: DanmakuItem) {
  danmakuPlaying.value.push({ key: ++danmakuSeq, item, bornAt: Date.now() })
}

async function loadDanmaku() {
  if (danmakuLoaded.value || props.item.type !== 'video' || typeof window === 'undefined') return
  danmakuLoaded.value = true
  try {
    const data = await api<{ items: { id: number; body: string; time_ms: number }[] }>(
      `/api/v1/contents/${props.item.id}/danmaku`,
    )
    danmakuList.value = (data?.items || []).map((entry, index) => ({
      id: entry.id, text: entry.body, timeMs: entry.time_ms, lane: index % 6,
    }))
  } catch {
    danmakuLoaded.value = false // 下次激活时重试
  }
}

watch(() => props.active, (active) => {
  if (active) void loadDanmaku()
}, { immediate: true })

// 按视频时间轴触发：只处理这一帧跨过的时间区间，seek 跳跃过大时跳过；
// loop 回绕（currentTime 变小）清空触发标记，下一轮重新播放同一批弹幕。
watch(() => props.player.currentTime, (seconds) => {
  const nowMs = Math.round((seconds || 0) * 1000)
  const now = Date.now()
  if (danmakuPlaying.value.some((entry) => now - entry.bornAt >= 8000)) {
    danmakuPlaying.value = danmakuPlaying.value.filter((entry) => now - entry.bornAt < 8000)
  }
  if (nowMs < lastDanmakuMs) {
    firedDanmaku.clear()
    lastDanmakuMs = -1
    return
  }
  const span = nowMs - lastDanmakuMs
  lastDanmakuMs = nowMs
  if (!props.active || !danmakuOn.value || props.item.type !== 'video' || span > 2000) return
  for (const item of danmakuList.value) {
    if (firedDanmaku.has(item.id)) continue
    if (item.timeMs <= nowMs && item.timeMs >= nowMs - span) {
      firedDanmaku.add(item.id)
      fireDanmaku(item)
    }
  }
})

function fmtTime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '00:00'
  const total = Math.round(seconds)
  const m = Math.floor(total / 60)
  const sec = String(total % 60).padStart(2, '0')
  const min = String(m).padStart(2, '0')
  return `${min}:${sec}`
}

async function sendDanmaku() {
  const text = danmakuInput.value.trim().slice(0, 30)
  if (!text) return
  const timeMs = Math.max(0, Math.round(props.player.currentTime * 1000))
  try {
    const data = await api<{ item: { id: number; body: string; time_ms: number } }>(
      `/api/v1/contents/${props.item.id}/danmaku`,
      { method: 'POST', body: { body: text, time_ms: timeMs } },
    )
    const item: DanmakuItem = {
      id: data.item.id, text: data.item.body, timeMs: data.item.time_ms,
      lane: danmakuList.value.length % 6,
    }
    danmakuList.value.push(item)
    firedDanmaku.add(item.id) // 本轮已经立即显示，别再按时间轴触发一次
    fireDanmaku(item)
    danmakuInput.value = ''
    danmakuEmojiOpen.value = false
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      openAuthDialog('login', { reason: '登录后即可发弹幕' })
      return
    }
    // 失败保留草稿；服务端给的是可操作提示（含不当用语被拒时要原样告诉用户）。
    emit('notice', errorMessage(error))
  }
}

function toggleDanmaku() {
  danmakuOn.value = !danmakuOn.value
}
const sourceRatio = computed(() => {
  if (!props.player.videoWidth || !props.player.videoHeight) return 0
  return props.player.videoWidth / props.player.videoHeight
})
const fitCover = computed(() => sourceRatio.value >= 1 && viewportRatio.value >= 1)
if (typeof window !== 'undefined') {
  const syncViewport = () => { viewportRatio.value = window.innerWidth / Math.max(window.innerHeight, 1) }
  syncViewport()
  window.addEventListener('resize', syncViewport)
  onBeforeUnmount(() => window.removeEventListener('resize', syncViewport))
}
// 控制条弹出层（倍速 / 清晰度 / 音量）：同一时间只开一个，点条外或 Esc 收起。
const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2]
const bar = ref<HTMLElement | null>(null)
const itemRoot = ref<HTMLElement | null>(null)
const openPop = ref<'' | 'rate' | 'quality' | 'volume'>('')
// 移动端没有功能条：「弹」按钮唤起底部输入条（§6.3）。
const mobileDanmaku = ref(false)
const mobileInput = ref<HTMLInputElement | null>(null)

// 弹幕是纯文本：复用评论的表情面板，但用 emojiOnly 去掉 GIF 页签。
function toggleDanmakuEmoji() {
  danmakuEmojiOpen.value = !danmakuEmojiOpen.value
}

// 插到光标处（与评论同一口径）；maxlength=30 拦不住直接赋值，自己挡一道。
function insertDanmakuEmoji(char: string) {
  const element = mobileDanmaku.value ? mobileInput.value : danmakuInputEl.value
  if (danmakuInput.value.length + char.length > 30) return
  if (!element) {
    danmakuInput.value += char
    return
  }
  const start = element.selectionStart ?? danmakuInput.value.length
  const end = element.selectionEnd ?? start
  danmakuInput.value = danmakuInput.value.slice(0, start) + char + danmakuInput.value.slice(end)
  void nextTick(() => {
    element.focus()
    const caret = start + char.length
    element.setSelectionRange(caret, caret)
  })
}

const qualityLabel = computed(() => {
  if (props.player.level < 0) return '智能'
  return props.player.levels.find((entry) => entry.index === props.player.level)?.label || '智能'
})

function togglePop(name: 'rate' | 'quality' | 'volume') {
  openPop.value = openPop.value === name ? '' : name
}

// 音量键只做一件事：切换静音状态（同一个键既能取消静音，也能重新静音）。
// 音量滑块改成鼠标悬浮时出现——点击被「静音开关」占用了，再让它兼职开弹层会互相打架。
// 滑块浮在按钮上方 8px，鼠标走过去会先落进不属于任何元素的空隙并触发 mouseleave；
// 立刻关弹层会让人永远碰不到滑块，所以给关闭留一段宽容期。
const VOLUME_HOVER_GRACE = 300
let volumeCloseTimer: ReturnType<typeof setTimeout> | null = null

function cancelVolumeClose() {
  if (volumeCloseTimer) {
    clearTimeout(volumeCloseTimer)
    volumeCloseTimer = null
  }
}

function onVolumeClick() {
  cancelVolumeClose()
  emit('muteToggle')
  openPop.value = ''
}

function hoverVolume(open: boolean) {
  cancelVolumeClose()
  if (open) {
    openPop.value = 'volume'
  } else if (openPop.value === 'volume') {
    volumeCloseTimer = setTimeout(() => {
      volumeCloseTimer = null
      // 宽容期内可能又开了别的浮层，只收掉仍是音量的那个。
      if (openPop.value === 'volume') openPop.value = ''
    }, VOLUME_HOVER_GRACE)
  }
}

// 暂未实现的条目明确说「即将开放」，不做点了没反应的死按钮。
function soon(name: string) {
  openPop.value = ''
  emit('notice', `${name}即将开放`)
}

// 点条上的其它控件（播放、输入框…）时把弹出层收掉，避免滑杆一直挂在上面。
function onRowClick(event: MouseEvent) {
  const target = event.target as HTMLElement | null
  if (!target?.closest('.pb-anchor')) openPop.value = ''
}

function openMobileDanmaku() {
  openPop.value = ''
  mobileDanmaku.value = true
  void nextTick(() => mobileInput.value?.focus())
}

function closeMobileDanmaku() {
  mobileDanmaku.value = false
  danmakuEmojiOpen.value = false
}

function sendMobileDanmaku() {
  sendDanmaku()
  // 发完保留输入条，方便连着发第二条。
  void nextTick(() => mobileInput.value?.focus())
}

function onDocPointerDown(event: PointerEvent) {
  const target = event.target as HTMLElement | null
  if (!target) return
  if (openPop.value && !bar.value?.contains(target)) openPop.value = ''
  if (mobileDanmaku.value && !target.closest('.dm-sheet')) closeMobileDanmaku()
  // 面板与触发按钮分别在 .pb-input / .dm-sheet 内，点这两处之外才收起。
  if (danmakuEmojiOpen.value && !target.closest('.pb-input') && !target.closest('.dm-sheet')) {
    danmakuEmojiOpen.value = false
  }
}

function onDocKeydown(event: KeyboardEvent) {
  if (event.key !== 'Escape') return
  openPop.value = ''
  closeMobileDanmaku()
}

watch([openPop, mobileDanmaku, danmakuEmojiOpen], ([pop, mobile, emoji]) => {
  if (pop || mobile || emoji) {
    document.addEventListener('pointerdown', onDocPointerDown, true)
    document.addEventListener('keydown', onDocKeydown, true)
  } else {
    document.removeEventListener('pointerdown', onDocPointerDown, true)
    document.removeEventListener('keydown', onDocKeydown, true)
  }
})

// 切到另一条内容时浮层不能留在屏幕上。
watch(() => props.item.id, () => {
  cancelVolumeClose()
  openPop.value = ''
  closeMobileDanmaku()
  stopGallery()
  // 换作品后张序号必须归零，否则「下载原图」会指向上一件图集的第 N 张。
  galleryIndex.value = 0
  danmakuList.value = []
  danmakuPlaying.value = []
  danmakuLoaded.value = false
  firedDanmaku.clear()
  lastDanmakuMs = -1
  if (props.active) void loadDanmaku()
})

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onDocPointerDown, true)
  document.removeEventListener('keydown', onDocKeydown, true)
  cancelVolumeClose()
  stopGallery()
})

const galleryIndex = ref(0)
// 图片控制条的自动轮播：默认关闭，点 ▶ 才每 4s 切一张，到末张停。
const galleryPlaying = ref(false)
let galleryTimer: ReturnType<typeof setInterval> | null = null

function stopGallery() {
  galleryPlaying.value = false
  if (galleryTimer) {
    clearInterval(galleryTimer)
    galleryTimer = null
  }
}

function toggleGalleryPlay() {
  if (galleryPlaying.value) {
    stopGallery()
    return
  }
  if (props.item.images.length < 2) return
  galleryPlaying.value = true
  galleryTimer = setInterval(() => {
    if (galleryIndex.value >= props.item.images.length - 1) {
      stopGallery()
      return
    }
    galleryIndex.value += 1
  }, 4000)
}

// 图片没有 video 元素，全屏直接作用在整条内容容器上。
async function toggleImageFullscreen() {
  try {
    if (document.fullscreenElement) await document.exitFullscreen()
    else await itemRoot.value?.requestFullscreen()
  } catch { /* 不支持则忽略 */ }
}
let clickTimer: ReturnType<typeof setTimeout> | null = null
let flashTimer: ReturnType<typeof setTimeout> | null = null

const progress = computed(() => {
  if (!props.player.duration) return 0
  return Math.min(100, (props.player.currentTime / props.player.duration) * 100)
})

function showFlash(value: 'play' | 'pause' | 'like') {
  flash.value = value
  if (flashTimer) clearTimeout(flashTimer)
  flashTimer = setTimeout(() => { flash.value = '' }, 520)
}

function onSurfaceClick() {
  // 图片没有播放/暂停：点画面切换底栏与控制条的显隐（默认收起，保持沉浸）。
  if (props.item.type === 'image') {
    emit('toggleChrome')
    return
  }
  // 中央图标表示「下一步可执行的动作」：暂停后显示播放键（提示可继续播放），
  // 恢复播放后显示暂停键。之前写反了，播放时点按会闪暂停图标。
  showFlash(props.player.playing ? 'play' : 'pause')
  if (clickTimer) {
    clearTimeout(clickTimer)
    clickTimer = null
    showFlash('like')
    emit('like')
    return
  }
  clickTimer = setTimeout(() => {
    clickTimer = null
    emit('toggle')
  }, 230)
}

function onSeek(event: MouseEvent) {
  const element = event.currentTarget as HTMLElement
  const rect = element.getBoundingClientRect()
  emit('seek', (event.clientX - rect.left) / Math.max(rect.width, 1))
}
</script>

<template>
  <article
    ref="itemRoot" class="item" :data-type="props.item.type"
    :class="{ 'nav-hidden': props.navHidden }"
  >
    <div class="media-slot" :class="{ 'fit-cover': fitCover }" @click="onSurfaceClick">
      <template v-if="props.item.type === 'image'">
        <GalleryStrip
          :images="props.item.images" :alt="props.item.title" :index="galleryIndex"
          @change="galleryIndex = $event"
        />
      </template>
      <template v-else>
        <img
          v-if="props.item.media.poster_url" class="poster"
          :src="props.item.media.poster_url" :alt="`${props.caption || props.item.title} 的封面`"
        />
        <div v-else class="poster-fallback">{{ props.item.media.status === 'processing' ? '转码中' : '暂无封面' }}</div>
      </template>
    </div>

    <div class="scrim scrim-top" aria-hidden="true" />
    <div class="scrim scrim-bottom" aria-hidden="true" />

    <div v-if="props.active" class="center-feedback" aria-hidden="true">
      <span v-if="flash" class="flash"><ImIcon :name="flash === 'like' ? 'heart' : flash" :size="42" :filled="flash === 'like'" /></span>
      <button v-else-if="props.player.failed" type="button" class="retry vp-glass" @click="emit('retry')">
        <ImIcon name="retry" :size="20" />{{ props.player.message || '播放失败，点击重试' }}
      </button>
      <span v-else-if="props.player.loading" class="loading">载入中…</span>
    </div>

    <!-- 弹幕层：滚动弹幕覆盖在画面上（弹开关控制显示） -->
    <div v-if="danmakuOn" class="danmaku-layer" aria-hidden="true">
      <span
        v-for="entry in danmakuPlaying" :key="entry.key" class="danmaku"
        :style="{ top: `${(entry.item.lane % 6) * 36 + 88}px` }"
      >{{ entry.item.text }}</span>
    </div>

    <!-- 播放控制条（§6.4）：视频与底栏之间常显；倍速 / 清晰度 / 音量都收在这一条上。 -->
    <div
      v-if="props.active && props.item.type === 'video' && props.player.ready"
      ref="bar" class="player-bar"
      :class="{ 'player-bar--flush': props.navHidden }"
      @click.stop
    >
      <!-- 进度：贴在控制条上沿的细线。点击定位；方向键由页面级快捷键统一处理，
           避免进度条聚焦时局部与全局各 seek 一次变成 ±10 秒。 -->
      <div
        class="pb-progress" role="slider" tabindex="0"
        :aria-valuenow="Math.round(progress)" aria-valuemin="0" aria-valuemax="100"
        aria-label="播放进度" @click="onSeek"
      >
        <span class="pb-track"><span class="pb-fill" :style="{ width: `${progress}%` }" /></span>
      </div>

      <div class="pb-row" @click="onRowClick">
        <button
          type="button" class="pb-btn"
          :aria-label="props.player.playing ? '暂停' : '播放'" @click="emit('toggle')"
        >
          <ImIcon :name="props.player.playing ? 'pause' : 'play'" :size="18" />
        </button>
        <span class="pb-time">
          {{ fmtTime(props.player.currentTime) }}<span class="pb-total"> / {{ fmtTime(props.player.duration) }}</span>
        </span>

        <button
          type="button" class="pb-word pb-icon" :class="{ on: danmakuOn }"
          :aria-pressed="danmakuOn" aria-label="弹幕开关"
          :title="danmakuOn ? '关闭弹幕' : '打开弹幕'" @click="toggleDanmaku"
        >
          <!-- 开：气泡 + 圆形对勾（.pb-word.on 把 currentColor 染成品牌粉）；关：气泡 + 圆形斜杠。
               与右侧「弹幕设置」同用 .pb-danmaku，三颗图标尺寸完全一致。 -->
          <svg
            v-if="danmakuOn" class="pb-danmaku danmaku-toggle"
            viewBox="0 0 1024 1024" aria-hidden="true" focusable="false"
          >
            <path
              fill="currentColor"
              d="M221.58 319.89h-48c-35.9 0-65-29.1-65-65s29.1-65 65-65h48c35.9 0 65 29.1 65 65s-29.1 65-65 65zM718.58 319.89h-322c-35.9 0-65-29.1-65-65s29.1-65 65-65h322c35.9 0 65 29.1 65 65s-29.1 65-65 65zM407.58 579.89h-234c-35.9 0-65-29.1-65-65s29.1-65 65-65h234c35.9 0 65 29.1 65 65s-29.1 65-65 65zM256.58 838.89h-83c-35.9 0-65-29.1-65-65s29.1-65 65-65h83c35.9 0 65 29.1 65 65s-29.1 65-65 65zM429.58 838.89h-1c-35.9 0-65-29.1-65-65s29.1-65 65-65h1c35.9 0 65 29.1 65 65s-29.1 65-65 65zM713.96 396.72c-122.04 0-220.97 98.93-220.97 220.97s98.93 220.97 220.97 220.97 220.97-98.93 220.97-220.97S836 396.72 713.96 396.72zM837.9 561.91L689.88 715.26a30.078 30.078 0 0 1-21.38 9.19h-0.27c-7.97 0-15.63-3.17-21.27-8.81l-56.57-56.57c-11.75-11.75-11.75-30.79 0-42.54s30.79-11.75 42.54 0l34.92 34.91 126.76-131.32c11.54-11.96 30.58-12.29 42.54-0.75 11.95 11.54 12.29 30.58 0.75 42.54z"
            />
          </svg>
          <svg
            v-else class="pb-danmaku danmaku-toggle"
            viewBox="0 0 1024 1024" aria-hidden="true" focusable="false"
          >
            <path
              fill="currentColor"
              d="M217.8 319.89h-48c-35.9 0-65-29.1-65-65s29.1-65 65-65h48c35.9 0 65 29.1 65 65s-29.1 65-65 65zM714.8 319.89h-322c-35.9 0-65-29.1-65-65s29.1-65 65-65h322c35.9 0 65 29.1 65 65s-29.1 65-65 65zM403.8 579.89h-234c-35.9 0-65-29.1-65-65s29.1-65 65-65h234c35.9 0 65 29.1 65 65s-29.1 65-65 65zM252.8 838.89h-83c-35.9 0-65-29.1-65-65s29.1-65 65-65h83c35.9 0 65 29.1 65 65s-29.1 65-65 65zM425.8 838.89h-1c-35.9 0-65-29.1-65-65s29.1-65 65-65h1c35.9 0 65 29.1 65 65s-29.1 65-65 65zM717.73 398.22c-122.04 0-220.97 98.93-220.97 220.97 0 122.04 98.93 220.97 220.97 220.97 122.04 0 220.97-98.93 220.97-220.97 0.01-122.04-98.93-220.97-220.97-220.97zM822 554.48l-165.25 171.2c-5.9 6.12-13.77 9.19-21.65 9.19-7.52 0-15.05-2.8-20.89-8.44-11.95-11.54-12.29-30.58-0.75-42.54l165.25-171.2c11.54-11.96 30.58-12.29 42.54-0.75 11.95 11.55 12.29 30.59 0.75 42.54z"
            />
          </svg>
        </button>
        <button
          type="button" class="pb-word pb-secondary pb-icon"
          aria-label="弹幕设置" title="弹幕设置" @click="soon('弹幕设置')"
        >
          <!-- 弹幕设置：气泡 + 六边形螺母，与开关两态同系列、同尺寸。 -->
          <svg class="pb-danmaku danmaku-settings" viewBox="0 0 1024 1024" aria-hidden="true" focusable="false">
            <path
              fill="currentColor"
              d="M226.58 320.39h-48c-35.9 0-65-29.1-65-65s29.1-65 65-65h48c35.9 0 65 29.1 65 65s-29.1 65-65 65zM723.58 320.39h-322c-35.9 0-65-29.1-65-65s29.1-65 65-65h322c35.9 0 65 29.1 65 65s-29.1 65-65 65zM402.58 580.39h-224c-35.9 0-65-29.1-65-65s29.1-65 65-65h224c35.9 0 65 29.1 65 65s-29.1 65-65 65zM251.58 839.39h-73c-35.9 0-65-29.1-65-65s29.1-65 65-65h73c35.9 0 65 29.1 65 65s-29.1 65-65 65zM424.58 839.39h-1c-35.9 0-65-29.1-65-65s29.1-65 65-65h1c35.9 0 65 29.1 65 65s-29.1 65-65 65zM954.82 588.59l-93.4-161.78a59.189 59.189 0 0 0-51.27-29.6h-186.8a59.201 59.201 0 0 0-51.27 29.6l-93.4 161.78a59.225 59.225 0 0 0 0 59.2l93.4 161.78a59.189 59.189 0 0 0 51.27 29.6h186.81c21.15 0 40.69-11.28 51.27-29.6l93.4-161.78a59.2 59.2 0 0 0-0.01-59.2z m-238.07 95.78c-36.55 0-66.18-29.63-66.18-66.18 0-36.55 29.63-66.18 66.18-66.18 36.55 0 66.18 29.63 66.18 66.18 0 36.55-29.63 66.18-66.18 66.18z"
            />
          </svg>
        </button>

        <div class="pb-input">
          <button
            type="button" class="pb-face" :class="{ on: danmakuEmojiOpen }"
            :aria-expanded="danmakuEmojiOpen" aria-label="弹幕表情" title="表情"
            @click="toggleDanmakuEmoji"
          >
            <ImIcon name="face" :size="16" />
          </button>
          <input
            ref="danmakuInputEl" v-model="danmakuInput" maxlength="30"
            placeholder="发一条友好的弹幕吧"
            aria-label="发送弹幕" @keyup.enter="sendDanmaku"
          />
          <EmojiPanel v-if="danmakuEmojiOpen" emoji-only @pick="insertDanmakuEmoji" />
        </div>
        <button type="button" class="pb-send" :disabled="!danmakuInput.trim()" @click="sendDanmaku">发送</button>

        <button
          v-if="props.autoplay !== undefined" type="button" class="pb-toggle pb-secondary"
          :class="{ on: props.autoplay }" :aria-pressed="!!props.autoplay"
          aria-label="连播" @click="emit('autoplayToggle')"
        >
          <span class="pb-switch" /><span>连播</span>
        </button>
        <button
          v-if="props.clearUi !== undefined" type="button" class="pb-toggle pb-secondary"
          :class="{ on: props.clearUi }" :aria-pressed="!!props.clearUi"
          aria-label="清屏" @click="emit('clearToggle')"
        >
          <span class="pb-switch" /><span>清屏</span>
        </button>

        <span class="pb-anchor">
          <button
            type="button" class="pb-word" :aria-expanded="openPop === 'quality'"
            aria-label="清晰度" @click="togglePop('quality')"
          >{{ qualityLabel }}</button>
          <div v-if="openPop === 'quality'" class="pb-pop" role="group" aria-label="清晰度">
            <button type="button" :class="{ on: props.player.level < 0 }" @click="emit('level', -1); openPop = ''">智能</button>
            <button
              v-for="level in props.player.levels" :key="level.index" type="button"
              :class="{ on: props.player.level === level.index }"
              @click="emit('level', level.index); openPop = ''"
            >{{ level.label }}</button>
          </div>
        </span>

        <span class="pb-anchor">
          <button
            type="button" class="pb-word" :aria-expanded="openPop === 'rate'"
            aria-label="倍速" @click="togglePop('rate')"
          >{{ props.player.rate === 1 ? '倍速' : `${props.player.rate}x` }}</button>
          <div v-if="openPop === 'rate'" class="pb-pop" role="group" aria-label="倍速">
            <button
              v-for="rate in RATES" :key="rate" type="button"
              :class="{ on: props.player.rate === rate }"
              @click="emit('rate', rate); openPop = ''"
            >{{ rate }}x</button>
          </div>
        </span>

        <button type="button" class="pb-btn pb-secondary" aria-label="画中画" @click="emit('pip')">
          <ImIcon name="pip" :size="18" />
        </button>

        <span
          class="pb-anchor pb-secondary pb-anchor-volume"
          @mouseenter="hoverVolume(true)" @mouseleave="hoverVolume(false)"
        >
          <button
            type="button" class="pb-btn" :aria-expanded="openPop === 'volume'"
            :aria-label="props.player.muted ? '取消静音' : '静音'"
            :title="props.player.muted ? '取消静音' : '静音'"
            @click="onVolumeClick" @focus="hoverVolume(true)"
          >
            <ImIcon :name="props.player.muted ? 'mute' : 'volume'" :size="18" />
          </button>
          <div
            v-if="openPop === 'volume'" class="pb-pop pb-pop-volume" role="group" aria-label="音量"
            @mouseenter="hoverVolume(true)" @mouseleave="hoverVolume(false)"
          >
            <input
              type="range" min="0" max="100" step="1"
              :value="Math.round(props.player.volume * 100)" aria-label="音量"
              @input="emit('volume', Number(($event.target as HTMLInputElement).value) / 100)"
            />
          </div>
        </span>

        <button type="button" class="pb-btn pb-secondary" aria-label="循环播放" @click="soon('循环播放')">
          <ImIcon name="loop" :size="18" />
        </button>
        <button type="button" class="pb-btn" aria-label="全屏" @click="emit('fullscreen')">
          <ImIcon name="expand" :size="18" />
        </button>
      </div>
    </div>

    <!-- ≤780px 不渲染整条功能栏（设计如此），而自动播放默认静音：
         顶栏「搜索」左边留一颗常驻的静音开关（图标随状态切换），
         点一下取消静音，再点一下重新静音——不能只在静音时才出现，否则没法再关掉声音。 -->
    <button
      v-if="props.active && props.item.type === 'video' && props.player.ready"
      type="button" class="mobile-unmute"
      :aria-label="props.player.muted ? '取消静音' : '静音'"
      :title="props.player.muted ? '取消静音' : '静音'"
      @click="emit('muteToggle')"
    >
      <ImIcon :name="props.player.muted ? 'mute' : 'volume'" :size="22" />
    </button>

    <!-- 图集指示器（§5.4）：胶囊底 + 当前张实心点 + 序号，避免散点贴在画面上看不清。 -->
    <div
      v-if="props.active && props.item.type === 'image'"
      class="gallery-indicator" role="status"
      :aria-label="`第 ${galleryIndex + 1} 张，共 ${props.item.images.length} 张`"
    >
      <span
        v-for="(_, index) in props.item.images" :key="index"
        class="gallery-dot" :class="{ on: index === galleryIndex }"
      />
      <em>{{ galleryIndex + 1 }} / {{ props.item.images.length }}</em>
    </div>

    <ActionRail
      v-if="props.active && !props.clearUi"
      :item="props.item" :avatar="props.avatar"
      :liked="props.liked" :favorited="props.favorited" :following="props.following"
      :watchlisted="props.watchlisted" :gallery-index="galleryIndex"
      :counts="props.counts" :label="props.label"
      @like="emit('like')" @comment="emit('comment')" @favorite="emit('favorite')"
      @share="emit('share')" @follow="emit('follow')"
      @watch-later="emit('watchLater')" @report="emit('report')"
      @notice="emit('notice', $event)"
    />

    <InfoLayer
      v-if="props.active && !props.clearUi"
      :author="props.item.author.display_name || `用户${props.item.author.handle}`" :caption="props.caption"
      :tags="props.tags" :expanded="props.expanded" :poi="props.item.poi"
      :published-at="props.item.published_at"
      :decentralized="props.item.origin === 'external_direct'"
      :danmaku="props.item.type === 'video'"
      @toggle-expand="emit('expand')"
      @danmaku="openMobileDanmaku"
    />

    <!-- 图片控制条（§5.4）：图片没有音轨，只留轮播 / 连播 / 清屏 / 全屏，别让底栏上方空一条。 -->
    <div
      v-if="props.active && props.item.type === 'image'"
      class="player-bar player-bar--image"
      :class="{ 'player-bar--flush': props.navHidden }"
      @click.stop
    >
      <div class="pb-row">
        <button
          type="button" class="pb-btn"
          :aria-label="galleryPlaying ? '暂停轮播' : '自动轮播'"
          :aria-pressed="galleryPlaying" @click="toggleGalleryPlay"
        >
          <ImIcon :name="galleryPlaying ? 'pause' : 'play'" :size="18" />
        </button>
        <span class="pb-spacer" />
        <button
          v-if="props.autoplay !== undefined" type="button" class="pb-toggle"
          :class="{ on: props.autoplay }" :aria-pressed="!!props.autoplay"
          aria-label="连播" @click="emit('autoplayToggle')"
        >
          <span class="pb-switch" /><span>连播</span>
        </button>
        <button
          v-if="props.clearUi !== undefined" type="button" class="pb-toggle"
          :class="{ on: props.clearUi }" :aria-pressed="!!props.clearUi"
          aria-label="清屏" @click="emit('clearToggle')"
        >
          <span class="pb-switch" /><span>清屏</span>
        </button>
        <button type="button" class="pb-btn" aria-label="全屏" @click="toggleImageFullscreen">
          <ImIcon name="expand" :size="18" />
        </button>
      </div>
    </div>

    <!-- 移动端弹幕输入条：由作者名上方的「弹」唤起，进入即聚焦（§6.3）。 -->
    <div v-if="mobileDanmaku" class="dm-sheet" role="dialog" aria-label="发送弹幕" @click.stop>
      <span class="dm-sheet-tag" aria-hidden="true">弹</span>
      <input
        ref="mobileInput" v-model="danmakuInput" maxlength="30"
        enterkeyhint="send" placeholder="发一条友好的弹幕吧" aria-label="发送弹幕"
        @keyup.enter="sendMobileDanmaku"
      />
      <button
        type="button" class="dm-sheet-face" :class="{ on: danmakuEmojiOpen }"
        :aria-expanded="danmakuEmojiOpen" aria-label="插入表情"
        @click="toggleDanmakuEmoji"
      >
        <ImIcon name="face" :size="20" />
      </button>
      <EmojiPanel v-if="danmakuEmojiOpen" emoji-only @pick="insertDanmakuEmoji" />
    </div>

  </article>
</template>

<style scoped>
.item {
  position: relative;
  height: 100dvh;
  scroll-snap-align: start;
  overflow: hidden;
  background: var(--vp-im-bg);
}
/* PC 端底栏收起时，左右两侧（信息层 / 操作栏）跟着落到控制条上方：
   控制条贴底后高约 51px，两侧距底 65px 正好留 14px；有底栏时仍走令牌的 126px。
   移动端不匹配媒体查询，维持 78 / 74 的原值。 */
@media (min-width: 781px) {
  .item.nav-hidden {
    --vp-im-action-rail-offset-bottom: 65px;
    --vp-im-info-offset-bottom: 65px;
  }
}
/* 不创建独立层叠上下文：图集指示器需在边缘渐变遮罩之上，而画面本身仍在遮罩之下。 */
.media-slot { position: absolute; inset: 0; }
.poster, .poster-fallback {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: var(--vp-im-bg);
}
.poster-fallback { display: grid; place-items: center; color: var(--vp-im-text-secondary); font-size: 13px; }
:deep(.feed-video) {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  object-fit: contain;
  background: transparent;
}
/* 横屏源在竖比例视口：cover 裁两侧（≤20%）消除上下黑边；竖屏源仍 contain。 */
.media-slot.fit-cover :deep(.feed-video) {
  object-fit: cover;
}
.scrim { position: absolute; left: 0; right: 0; pointer-events: none; z-index: 1; }
.scrim-top { top: 0; height: 140px; background: var(--vp-im-scrim-top); }
.scrim-bottom { bottom: 0; height: 300px; background: var(--vp-im-scrim-bottom); }
.center-feedback {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  z-index: var(--vp-im-z-player);
  pointer-events: none;
}
.flash {
  display: grid;
  place-items: center;
  width: 76px;
  height: 76px;
  border-radius: 50%;
  background: rgba(10, 10, 20, 0.5);
  color: #fff;
  animation: pop var(--vp-im-motion-enter) var(--vp-ease-out);
}
.flash :deep(svg) { color: var(--vp-im-accent); }
@keyframes pop { from { transform: scale(0.7); opacity: 0; } to { transform: scale(1); opacity: 1; } }
.retry {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 12px 18px;
  border-radius: 999px;
  color: var(--vp-im-text-primary);
  font-size: 13px;
  cursor: pointer;
  pointer-events: auto;
}
.loading { padding: 10px 16px; border-radius: 999px; background: rgba(10,10,20,.55); font-size: 12px; }

.gallery-indicator {
  position: absolute;
  left: 50%;
  bottom: calc(268px + env(safe-area-inset-bottom));
  transform: translateX(-50%);
  z-index: var(--vp-im-z-controls);
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border-radius: 999px;
  background: rgba(10, 10, 20, 0.62);
  -webkit-backdrop-filter: blur(8px);
  backdrop-filter: blur(8px);
  pointer-events: none;
}
.gallery-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.45);
}
.gallery-dot.on { width: 14px; border-radius: 3px; background: var(--vp-im-accent); }
.gallery-indicator em {
  margin-left: 6px;
  font-style: normal;
  font-size: 12px;
  color: var(--vp-im-text-primary);
}
/* ── 播放控制条（视频与底栏之间）────────────────────────────
   一条横向玻璃条：播放/时间、弹幕开关与输入、连播/清屏、清晰度/倍速、
   画中画/音量/循环/全屏。倍速、清晰度、音量改成条上的弹出层，
   不再用会贴到底栏下面的浮层。 */
.player-bar {
  position: absolute;
  /* 左右通栏：与底栏连成一片，两侧不留露画面的缝。 */
  left: 0;
  right: 0;
  /* 紧贴底栏顶边（底栏 8+44+8+border = 61），中间不留一条露画面的缝。 */
  bottom: calc(61px + env(safe-area-inset-bottom));
  z-index: calc(var(--vp-im-z-controls) + 1);
  padding: 6px 12px 0;
  border: 0;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 0;
  background: rgba(10, 10, 20, 0.62);
  -webkit-backdrop-filter: blur(10px) saturate(120%);
  backdrop-filter: blur(10px) saturate(120%);
  color: var(--vp-im-text-primary);
}
/* PC 端底栏在播放时收起：控制条同步落到屏幕最底部，消除它下面那条 61px 空区；
   暂停时回到 61px 贴住底栏顶边。过渡时长与底栏收起一致，切换不会跳一下。
   移动端（≤780px）控制条本就隐藏，这条规则不生效。 */
@media (min-width: 781px) {
  .player-bar { transition: bottom 200ms ease; }
  .player-bar--flush { bottom: 0; }
}
/* 进度线放在条内顶部：贴在条外会被视频画面吃掉，2px 的线根本看不清。 */
.pb-progress {
  position: absolute;
  left: 8px;
  right: 8px;
  top: 0;
  height: 6px;
  display: flex;
  align-items: center;
  cursor: pointer;
}
.pb-track {
  width: 100%;
  height: 2px;
  border-radius: 1px;
  background: rgba(255, 255, 255, 0.28);
  overflow: hidden;
}
.pb-fill { display: block; height: 100%; background: var(--vp-im-accent); }
.pb-progress:hover .pb-track { height: 4px; }
.pb-row { display: flex; align-items: center; gap: 4px; height: 44px; }
/* 图片条没有播放/时间/弹幕，用弹性空档把右侧的通用开关顶到右端。 */
.pb-spacer { flex: 1 1 auto; }
.player-bar--image { padding: 0 10px; }
.player-bar--image .pb-row { height: 44px; }
.pb-btn {
  flex: none;
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: inherit;
  cursor: pointer;
}
.pb-btn:hover { background: rgba(255, 255, 255, 0.1); }
.pb-time {
  flex: none;
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.pb-word {
  position: relative;
  flex: none;
  height: 30px;
  min-width: 30px;
  padding: 0 6px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--vp-im-text-secondary);
  font-size: 13px;
  cursor: pointer;
}
.pb-word:hover { background: rgba(255, 255, 255, 0.1); }
/* 图标型按钮（弹幕开关 / 弹幕设置）：与播放 / 音量那几颗 30px 方钮同尺寸，不再吃文字内边距。 */
.pb-word.pb-icon { display: grid; place-items: center; padding: 0; }
/* 开 / 关 / 设置三颗设计稿图标共用一个尺寸，保证视觉大小完全一致。 */
.pb-word .pb-danmaku { display: block; width: 18px; height: 18px; }
.pb-word.on { color: var(--vp-im-accent); }
.pb-input {
  position: relative;
  flex: 1 1 auto;
  min-width: 76px;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 10px;
  border-radius: 8px;
  /* 透明边框只为焦点态留位置，避免 :focus-within 改颜色时产生 1px 位移。 */
  border: 1px solid transparent;
  box-sizing: border-box;
  background: rgba(255, 255, 255, 0.1);
  color: var(--vp-im-text-secondary);
  transition: border-color 160ms ease, background-color 160ms ease;
}
/* 表情入口：只做图标提亮，不画悬浮灰底（与评论框同一口径）。 */
.pb-face {
  flex: none;
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: 0;
  border-radius: 6px;
  background: transparent;
  color: var(--vp-im-text-secondary);
  cursor: pointer;
}
.pb-face:hover, .pb-face.on { color: var(--vp-im-text-primary); }
/* 表情按钮在输入区最左端，面板要贴着它上方展开。EmojiPanel 默认 right:0
   是给评论的窄锚点准备的；这里 .pb-input 有几百 px，靠右挂会离按钮很远。 */
.pb-input :deep(.emoji-panel) {
  left: 0;
  right: auto;
  width: min(340px, calc(100vw - 40px));
}
.pb-input input {
  flex: 1;
  min-width: 0;
  border: 0;
  background: transparent;
  color: var(--vp-im-text-primary);
  font-size: 13px;
  outline: none;
}
.pb-input input::placeholder { color: rgba(255, 255, 255, 0.42); }
/* 与移动端 .dm-sheet 同一口径：整条已经是输入态，内部再套一圈全局粉色
   focus-visible 环会像表单报错；焦点改由外层边框/底色提亮表达。 */
.pb-input input:focus-visible { box-shadow: none; }
.pb-input:focus-within {
  border-color: rgba(255, 255, 255, 0.28);
  background: rgba(255, 255, 255, 0.14);
}
.pb-send {
  flex: none;
  height: 30px;
  padding: 0 12px;
  border: 0;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.16);
  color: var(--vp-im-text-primary);
  font-size: 12px;
  cursor: pointer;
}
.pb-send:disabled { opacity: 0.45; cursor: not-allowed; }
.pb-toggle {
  flex: none;
  display: flex;
  align-items: center;
  gap: 5px;
  height: 30px;
  padding: 0 6px;
  border: 0;
  background: transparent;
  color: var(--vp-im-text-secondary);
  font-size: 12px;
  cursor: pointer;
}
.pb-switch {
  position: relative;
  width: 24px;
  height: 14px;
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.24);
  transition: background var(--vp-im-motion-standard) var(--vp-ease-out);
}
.pb-switch::after {
  content: '';
  position: absolute;
  left: 2px;
  top: 2px;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: #fff;
  transition: transform var(--vp-im-motion-standard) var(--vp-ease-out);
}
.pb-toggle.on { color: var(--vp-im-text-primary); }
.pb-toggle.on .pb-switch { background: var(--vp-im-accent); }
.pb-toggle.on .pb-switch::after { transform: translateX(10px); }
.pb-anchor { position: relative; display: inline-flex; flex: none; }
.pb-pop {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  z-index: 1;
  display: flex;
  gap: 4px;
  padding: 6px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 10px;
  background: rgba(18, 18, 24, 0.96);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.5);
}
.pb-pop button {
  padding: 6px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--vp-im-text-primary);
  font-size: 12px;
  white-space: nowrap;
  cursor: pointer;
}
.pb-pop button:hover { background: rgba(255, 255, 255, 0.1); }
.pb-pop button.on { background: var(--vp-im-accent); color: var(--vp-im-on-accent); }
.pb-pop-volume { width: 132px; padding: 12px; }
.pb-pop-volume input { width: 100%; accent-color: var(--vp-im-accent); }
/* 弹层底边与按钮顶边之间有 8px 空隙，鼠标走过去会短暂落在命中区之外并触发
   mouseleave；用一条与按钮等宽的透明桥接条补上，让路径始终在 .pb-anchor 的后代里。
   只盖按钮正上方 30px，不伸到左边其它按钮上。 */
.pb-pop-volume::after {
  content: '';
  position: absolute;
  right: 0;
  top: 100%;
  width: 30px;
  height: 10px;
}

/* 弹幕层：从右向左滚动，按 lane 分轨（§6.3）。 */
.danmaku-layer {
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
  bottom: calc(120px + env(safe-area-inset-bottom));
  z-index: var(--vp-im-z-player);
  overflow: hidden;
  pointer-events: none;
}
.danmaku {
  position: absolute;
  right: 0;
  white-space: nowrap;
  font-size: 16px;
  line-height: 1.2;
  color: #fff;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.8);
  animation: danmaku-run 8s linear both;
}
@keyframes danmaku-run {
  from { transform: translateX(100%); }
  to { transform: translateX(calc(-100vw - 100%)); }
}
@media (prefers-reduced-motion: reduce) {
  .danmaku { animation: none; left: 16px; }
}
/* 移动端不再放整条功能栏：播放/音量交给系统与手势，
   弹幕走作者名上方的「弹」→ 底部输入条（参考移动端的克制布局）。 */
@media (max-width: 780px) {
  .player-bar { display: none; }

  /* 功能条在手机上不渲染，静音入口放在顶栏「搜索」左边（同一行、44px 见方的图标位），
     不压作者信息、不压右侧按钮列，也不挡画面中间。 */
  .mobile-unmute {
    position: absolute;
    top: calc(env(safe-area-inset-top) + 10px);
    right: 68px;
    z-index: var(--vp-im-z-controls);
    display: grid;
    place-items: center;
    width: 44px;
    height: 44px;
    padding: 0;
    border: 0;
    border-radius: 50%;
    background: none;
    color: #fff;
    cursor: pointer;
  }
  .mobile-unmute:active { background: rgba(255, 255, 255, 0.12); }
}

/* 移动端弹幕输入条（由信息层的「弹」唤起）。 */
.dm-sheet {
  position: fixed;
  left: 8px;
  right: 8px;
  /* 浮在底栏上方（底栏 61 + 8），不盖住主导航。 */
  bottom: calc(69px + env(safe-area-inset-bottom));
  z-index: var(--vp-im-z-toast);
  display: flex;
  align-items: center;
  gap: 8px;
  height: 52px;
  padding: 0 10px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 12px;
  background: rgba(18, 18, 24, 0.96);
  box-shadow: 0 12px 30px rgba(0, 0, 0, 0.45);
}
.dm-sheet-tag {
  flex: none;
  display: grid;
  place-items: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.1);
  color: var(--vp-im-accent);
  font-size: 14px;
}
.dm-sheet input {
  flex: 1;
  min-width: 0;
  height: 36px;
  border: 0;
  background: transparent;
  color: var(--vp-im-text-primary);
  font-size: 14px;
  outline: none;
}
.dm-sheet input::placeholder { color: rgba(255, 255, 255, 0.42); }
/* 整条已经是输入态，内部再套一圈粉色焦点环会像报错；焦点改由条的边框表达。 */
.dm-sheet input:focus-visible { box-shadow: none; }
.dm-sheet:focus-within { border-color: rgba(255, 255, 255, 0.28); }
.dm-sheet-face {
  flex: none;
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--vp-im-text-secondary);
  cursor: pointer;
}
.dm-sheet-face.on { color: var(--vp-im-accent); }
/* 移动端输入条本身就是定位锚点：面板横向铺满整条，不按桌面 340px 卡片靠右挂。 */
.dm-sheet :deep(.emoji-panel) { left: 0; right: 0; width: auto; }
@media (min-width: 781px) {
  .dm-sheet { display: none; }
}
</style>

