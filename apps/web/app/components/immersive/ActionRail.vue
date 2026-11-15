<script setup lang="ts">
// 右侧操作栏（UI 规范 §6.2）：圆形玻璃底仅用于按钮，头像使用实色描边。
// 低频的「晚点再看」与「举报」收进分享下方三个点的菜单，避免操作栏被次要动作拉长。
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { FeedItem } from '~/composables/useFeed'

const props = defineProps<{
  item: FeedItem
  avatar: string
  liked: boolean
  favorited: boolean
  following: boolean
  /** 当前视频是否已放进「晚点再看」，驱动菜单项的图标与文案。 */
  watchlisted?: boolean
  counts: { likes: number; comments: number; favorites: number; shares: number }
  label: (value: number) => string
}>()

// 与信息层一致的作者称呼：display_name 缺省时回落「用户 + 夜街号」，
// 纯数字夜街号直接读出来认不出是谁（useFeed.ts 的约定）。
const authorName = computed(() => props.item.author.display_name || `用户${props.item.author.handle}`)

const emit = defineEmits<{
  like: []
  comment: []
  favorite: []
  share: []
  follow: []
  watchLater: []
  report: []
}>()

const moreOpen = ref(false)
const moreWrap = ref<HTMLElement | null>(null)
const moreButton = ref<HTMLButtonElement | null>(null)
const watchLaterItem = ref<HTMLButtonElement | null>(null)
const reportItem = ref<HTMLButtonElement | null>(null)

function closeMore(restoreFocus = false) {
  if (!moreOpen.value) return
  moreOpen.value = false
  if (restoreFocus) moreButton.value?.focus()
}

function toggleMore() {
  moreOpen.value = !moreOpen.value
}

function onWatchLater() {
  closeMore()
  emit('watchLater')
}

function onReport() {
  closeMore()
  emit('report')
}

function onPointerDown(event: PointerEvent) {
  const target = event.target as Node | null
  if (target && !moreWrap.value?.contains(target)) closeMore()
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') closeMore(true)
}

// Tab 移出菜单时收起，键盘用户不会把浮层留在屏幕上。
function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null
  if (next && moreWrap.value?.contains(next)) return
  closeMore()
}

// 监听只在菜单打开期间挂载：沉浸流一次有多条内容，常驻监听没必要。
watch(moreOpen, async (open) => {
  if (open) {
    document.addEventListener('pointerdown', onPointerDown, true)
    document.addEventListener('keydown', onKeydown, true)
    await nextTick()
    // 视频条目第一项是「晚点再看」，图集没有该项时回落到「举报」。
    if (moreOpen.value) (watchLaterItem.value ?? reportItem.value)?.focus()
  } else {
    document.removeEventListener('pointerdown', onPointerDown, true)
    document.removeEventListener('keydown', onKeydown, true)
  }
})

// 切换作品时菜单不能挂在上一条作品上。
watch(() => props.item.id, () => closeMore())

onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', onPointerDown, true)
  document.removeEventListener('keydown', onKeydown, true)
})
</script>

<template>
  <aside class="rail" aria-label="作品操作">
    <div class="avatar-wrap">
      <NuxtLink
        class="avatar-link" :to="`/creator/${props.item.author.handle}`"
        :aria-label="`查看 ${authorName} 的主页`"
      >
        <img class="avatar" :src="props.avatar" :alt="`${props.item.author.handle} 的头像`" />
      </NuxtLink>
      <button
        type="button" class="follow" :class="{ following: props.following }"
        :aria-label="props.following ? '已关注，点击取消' : '关注作者'"
        @click="emit('follow')"
      >
        <ImIcon :name="props.following ? 'check' : 'follow'" :size="14" />
      </button>
    </div>

    <button
      type="button" class="action act-like" :class="{ active: props.liked }"
      :aria-pressed="props.liked" aria-label="点赞"
      @click="emit('like')"
    >
      <span class="bubble"><ImIcon name="heart" :size="24" filled /></span>
      <span class="count">{{ props.label(props.counts.likes) }}</span>
    </button>
    <button type="button" class="action act-comment" aria-label="查看评论" @click="emit('comment')">
      <span class="bubble"><ImIcon name="comment" :size="24" filled /></span>
      <span class="count">{{ props.label(props.counts.comments) }}</span>
    </button>
    <button
      type="button" class="action act-favorite" :class="{ active: props.favorited }"
      :aria-pressed="props.favorited" aria-label="收藏"
      @click="emit('favorite')"
    >
      <span class="bubble"><ImIcon name="star" :size="24" filled /></span>
      <span class="count">{{ props.label(props.counts.favorites) }}</span>
    </button>
    <button type="button" class="action act-share" aria-label="分享" @click="emit('share')">
      <span class="bubble"><ImIcon name="share" :size="24" filled /></span>
      <span class="count">{{ props.label(props.counts.shares) }}</span>
    </button>
    <!-- 更多操作：分享下方的三个点，低频动作收进菜单（§9.5 的举报链路不变）。 -->
    <div ref="moreWrap" class="more-wrap" @focusout="onFocusOut">
      <button
        ref="moreButton" type="button" class="action" :class="{ 'more-on': moreOpen }"
        aria-label="更多操作" aria-haspopup="menu" :aria-expanded="moreOpen"
        @click="toggleMore"
      >
        <span class="bubble">
          <span class="dots" aria-hidden="true"><i></i><i></i><i></i></span>
        </span>
        <!-- 与其它按钮的计数行等高，保持操作栏节奏一致。 -->
        <span class="count" aria-hidden="true">&nbsp;</span>
      </button>
      <div v-if="moreOpen" class="more-menu" role="menu" aria-label="更多操作">
        <!-- 只有视频能放进「晚点再看」；图集条目不显示这一项。 -->
        <button
          v-if="props.item.type === 'video'"
          ref="watchLaterItem" type="button" class="more-item" role="menuitem"
          :aria-pressed="!!props.watchlisted" @click="onWatchLater"
        >
          <ImIcon :name="props.watchlisted ? 'check' : 'clock'" :size="18" />
          <span>{{ props.watchlisted ? '已在晚点再看' : '晚点再看' }}</span>
        </button>
        <button
          ref="reportItem" type="button" class="more-item" role="menuitem" @click="onReport"
        >
          <ImIcon name="flag" :size="18" />
          <span>举报</span>
        </button>
      </div>
    </div>

  </aside>
</template>

<style scoped>
.rail {
  position: absolute;
  right: calc(var(--vp-im-action-rail-offset-right) + env(safe-area-inset-right));
  bottom: calc(var(--vp-im-action-rail-offset-bottom) + env(safe-area-inset-bottom));
  transition: bottom 200ms ease;
  z-index: var(--vp-im-z-controls);
  display: grid;
  justify-items: center;
  gap: var(--vp-im-action-gap);
}
.avatar-wrap {
  position: relative;
  /* 固定成头像边框盒尺寸：img 是行内替换元素，直接用内容盒会把基线以下的
     行距空档算进来，导致 + 号被压到头像下方而不是骑在下缘。 */
  width: 50px;
  height: 50px;
  margin-bottom: 6px;
}
.avatar {
  display: block;
  /* 边框计入宽高，保证 50px 就是头像可见圆的外径。 */
  box-sizing: border-box;
  width: 100%;
  height: 100%;
  border-radius: 50%;
  object-fit: cover;
  border: 2px solid var(--vp-im-text-primary);
  background: var(--vp-im-surface);
  transition: border-color 160ms ease;
}
/* 头像整块是可点的链接，占满 50px 头像盒；hover 只把描边染成品牌粉，不加底框。 */
.avatar-link { display: block; width: 100%; height: 100%; border-radius: 50%; cursor: pointer; }
.avatar-link:hover .avatar { border-color: var(--vp-im-accent); }
.follow {
  position: absolute;
  left: 50%;
  /* 22px 按钮压 11px 上去，正好骑在头像下缘中点。 */
  bottom: -11px;
  transform: translateX(-50%);
  display: grid;
  place-items: center;
  width: 22px;
  height: 22px;
  /* 不重置 padding 时浏览器默认的 1px 6px 会把 14px 图标挤出内容盒，
     网格轨道从内容盒左边起算，加号会偏右 2px。 */
  padding: 0;
  box-sizing: border-box;
  line-height: 0;
  border: 0;
  border-radius: 50%;
  color: var(--vp-im-on-accent);
  background: var(--vp-im-accent);
  cursor: pointer;
}
.follow.following { background: var(--vp-im-surface-raised); color: var(--vp-im-text-secondary); }
/* 按钮 = 圆底 + 圆外计数：图标只占圆底中间，文字落在圆外（UI §6.2 改版）。
   圆底用半透明白，不再套 vp-glass 的描边，避免 44px 圆里塞 30px 图标顶着边框。 */
.action {
  display: grid;
  justify-items: center;
  gap: 6px;
  padding: 0;
  border: 0;
  background: none;
  color: var(--vp-im-text-primary);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: transform var(--vp-im-motion-instant) var(--vp-ease-out),
    color var(--vp-im-motion-standard) var(--vp-ease-out);
}
.action:active { transform: scale(0.92); }
.action.active { color: var(--vp-im-accent); }
.bubble {
  display: grid;
  place-items: center;
  width: var(--vp-im-action-size);
  height: var(--vp-im-action-size);
  border-radius: 50%;
  background: var(--vp-im-action-disc-bg);
  -webkit-backdrop-filter: blur(8px) saturate(120%);
  backdrop-filter: blur(8px) saturate(120%);
  transition: background var(--vp-im-motion-standard) var(--vp-ease-out);
}
.bubble :deep(svg) { filter: drop-shadow(0 1px 2px rgba(10, 10, 31, 0.35)); }
/* ── 悬浮图标动画 ──────────────────────────────────────────────
   四个动作各一个短促动效，纯 CSS keyframes：项目没有动画库，挂 GSAP 到 CDN
   只会多一个 CSP 例外与外部请求。只动圆底里的 svg，用 transform 不改布局、
   不扩大命中区；@media (hover:hover) 避免触屏把 :hover 粘住。 */
.action .bubble :deep(svg) { transform-origin: center; }
@media (hover: hover) {
  .act-like:hover .bubble :deep(svg) { animation: rail-heart 460ms var(--vp-ease-out) both; }
  .act-comment:hover .bubble :deep(svg) { animation: rail-comment 420ms var(--vp-ease-out) both; }
  .act-favorite:hover .bubble :deep(svg) { animation: rail-star 520ms var(--vp-ease-out) both; }
  .act-share:hover .bubble :deep(svg) { animation: rail-share 420ms var(--vp-ease-out) both; }
}
@keyframes rail-heart {
  0% { transform: scale(1); }
  30% { transform: scale(1.26) rotate(-6deg); }
  55% { transform: scale(0.94) rotate(3deg); }
  78% { transform: scale(1.06); }
  100% { transform: scale(1); }
}
@keyframes rail-comment {
  0%, 100% { transform: translateY(0); }
  35% { transform: translateY(-4px) scale(1.08); }
  70% { transform: translateY(1px); }
}
@keyframes rail-star {
  0% { transform: rotate(0) scale(1); }
  35% { transform: rotate(-18deg) scale(1.2); }
  70% { transform: rotate(10deg) scale(1.04); }
  100% { transform: rotate(0) scale(1); }
}
@keyframes rail-share {
  0%, 100% { transform: translate(0, 0) rotate(0); }
  35% { transform: translate(4px, -3px) rotate(8deg); }
  70% { transform: translate(-2px, 1px) rotate(-4deg); }
}
@media (prefers-reduced-motion: reduce) {
  .action:hover .bubble :deep(svg) { animation: none; }
}
.action.active .bubble { background: var(--vp-im-action-disc-bg-active); }
.count {
  font-size: 12px;
  font-weight: 500;
  line-height: 1;
  letter-spacing: 0.01em;
  text-shadow: 0 1px 3px rgba(10, 10, 31, 0.5);
}
/* ── 更多操作菜单 ──────────────────────────────────────────
   三个点是实心小圆，比线性图标里的 h.01 点更醒目；菜单向左展开，
   避免贴着屏幕右缘被裁，箭头指回触发按钮。 */
.more-wrap { position: relative; }
/* 展开态只提亮圆底：把三个点染成强调色会在亮色 / 粉色画面上直接消失。 */
.action.more-on .bubble { background: rgba(255, 255, 255, 0.24); }
.dots { display: flex; align-items: center; gap: 3px; }
.dots i {
  display: block;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: currentColor;
}
.more-menu {
  position: absolute;
  right: calc(100% + 12px);
  top: calc(var(--vp-im-action-size) / 2);
  transform: translateY(-50%);
  z-index: calc(var(--vp-im-z-controls) + 1);
  min-width: 148px;
  padding: 6px;
  border: 1px solid rgba(255, 255, 255, 0.14);
  border-radius: 12px;
  background: var(--vp-im-surface-raised);
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.45);
  -webkit-backdrop-filter: blur(12px);
  backdrop-filter: blur(12px);
}
.more-menu::after {
  content: '';
  position: absolute;
  right: -5px;
  top: 50%;
  width: 8px;
  height: 8px;
  transform: translateY(-50%) rotate(45deg);
  background: var(--vp-im-surface-raised);
  border-right: 1px solid rgba(255, 255, 255, 0.14);
  border-top: 1px solid rgba(255, 255, 255, 0.14);
}
.more-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 12px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--vp-im-text-primary);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}
.more-item:hover { background: rgba(255, 255, 255, 0.08); }
.more-item:focus-visible {
  outline: none;
  background: rgba(255, 255, 255, 0.08);
  box-shadow: inset 0 0 0 1px var(--vp-im-accent);
}
</style>
