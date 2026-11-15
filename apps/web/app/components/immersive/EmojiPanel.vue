<script setup lang="ts">
// 表情面板：默认两个页签——「表情」（Google Noto 3D，按分类分页）与「GIF」（搜索图源）。
// emojiOnly 给弹幕这类纯文本输入用：隐藏底部切换、不加载 GIF，只留表情。
// 表情图形是同源 PNG（public/emoji3d，由 scripts/sync-emoji3d.mjs 导出），
// 尺寸固定 26px，避免不同表情视觉大小不一；3D 渲染比线性图标需要更大的显示尺寸。
const props = withDefaults(defineProps<{ emojiOnly?: boolean }>(), { emojiOnly: false })
const emit = defineEmits<{ pick: [value: string]; 'pick-gif': [url: string] }>()

// emoji：分类 + 表情栅格；gif：搜索框 + 结果栅格（见 GifPanel）。
const mode = ref<'emoji' | 'gif'>('emoji')

const active = ref(emojiCategories[0]?.key || '')
const current = computed(
  () => emojiCategories.find((category) => category.key === active.value) || emojiCategories[0],
)

// 8 个分类的宽度超过面板宽度（340px），必须能横向滚动才看得到「符号」。
// 触控板可以左右滑，桌面鼠标只能滚轮——所以把竖轮映射成横向滚动。
const tabs = ref<HTMLElement | null>(null)
const edges = reactive({ left: false, right: false })

function syncEdges() {
  const el = tabs.value
  if (!el) return
  const max = el.scrollWidth - el.clientWidth
  edges.left = el.scrollLeft > 1
  edges.right = max > 1 && el.scrollLeft < max - 1
}

function onTabsWheel(event: WheelEvent) {
  const el = tabs.value
  if (!el) return
  const max = el.scrollWidth - el.clientWidth
  if (max <= 1) return
  // deltaMode：0 像素（Chrome/Edge 默认）、1 行、2 页。按行滚时按 16px/行折算，
  // 否则 Firefox 上一格滚轮只能挪 3px。
  const unit = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? el.clientWidth : 1
  const delta = (event.deltaY || event.deltaX) * unit
  if (!delta) return
  const before = el.scrollLeft
  el.scrollLeft = Math.max(0, Math.min(max, before + delta))
  // 只有在真的滚动时才拦默认行为：滚到头以后让滚轮继续作用于外层，
  // 不要出现「面板里怎么滚都没反应」的死区。
  if (el.scrollLeft !== before) event.preventDefault()
  syncEdges()
}

// 点选把该分类滚进可视区（离边缘只有半个字时也能看到完整标签）。
function selectTab(key: string, event: MouseEvent) {
  active.value = key
  ;(event.currentTarget as HTMLElement | null)?.scrollIntoView({ inline: 'nearest', block: 'nearest' })
  requestAnimationFrame(syncEdges)
}

onMounted(() => {
  // 首次渲染后才知道有没有溢出；字体加载完宽度还会变一次，所以再补一帧。
  requestAnimationFrame(syncEdges)
  window.addEventListener('resize', syncEdges)
})
onBeforeUnmount(() => window.removeEventListener('resize', syncEdges))
</script>

<template>
  <div class="emoji-panel" role="dialog" :aria-label="mode === 'gif' ? '选择 GIF' : '选择表情'">
    <div v-if="mode === 'emoji'" class="tabs-wrap">
      <div
        ref="tabs" class="emoji-tabs" role="tablist" aria-label="表情分类"
        @wheel="onTabsWheel" @scroll.passive="syncEdges"
      >
        <button
          v-for="category in emojiCategories" :key="category.key"
          type="button" role="tab" :aria-selected="active === category.key"
          :class="{ on: active === category.key }"
          @click="selectTab(category.key, $event)"
        >{{ category.label }}</button>
      </div>
      <!-- 两头渐隐：暗示还有被遮住的分类，而不是像被硬裁掉。 -->
      <span v-if="edges.left" class="tabs-edge left" aria-hidden="true" />
      <span v-if="edges.right" class="tabs-edge right" aria-hidden="true" />
    </div>
    <div v-if="mode === 'emoji'" class="emoji-grid">
      <button
        v-for="item in current?.items || []" :key="item"
        type="button" class="emoji-cell" :aria-label="`插入表情 ${item}`"
        :title="item" @click="emit('pick', item)"
      >
        <img :src="emojiSrc(item) || ''" :alt="item" loading="lazy" />
      </button>
    </div>
    <!-- GIF 页：搜索 + 结果，点选后交给父组件待发送。 -->
    <GifPanel v-else-if="!props.emojiOnly" class="emoji-gif" @pick="emit('pick-gif', $event)" />

    <!-- 底部类型选择器：表情 / GIF 两个页签，选中态用品牌强调色。
         emojiOnly（弹幕等纯文本输入）不渲染这一条，也就完全看不到 GIF 入口。 -->
    <div v-if="!props.emojiOnly" class="emoji-foot">
      <button
        type="button" class="kind" :class="{ on: mode === 'emoji' }"
        :aria-pressed="mode === 'emoji'" aria-label="表情" title="表情"
        @click="mode = 'emoji'"
      >
        <ImIcon name="face" :size="26" />
      </button>
      <button
        type="button" class="kind gif" :class="{ on: mode === 'gif' }"
        :aria-pressed="mode === 'gif'" aria-label="GIF" title="GIF"
        @click="mode = 'gif'"
      >
        <!-- GIF 徽标是 36 格、evenodd 填充的复合字形，和 ImIcon 的 24 格单路径线性体系不同源，
             所以直接内联；fill 用 currentColor 跟随按钮色（原稿是 #2F3035/.7，深色主题下不可见）。
             32px：外环半径 11/36 → 视觉圆环 19.6px，与 26px 圆脸图标的 19.5px 一致（要一样大）。 -->
        <svg width="32" height="32" viewBox="0 0 36 36" fill="none" aria-hidden="true" focusable="false">
          <path
            fill-rule="evenodd" clip-rule="evenodd"
            d="M27.167 18a9.167 9.167 0 1 1-18.334 0 9.167 9.167 0 0 1 18.334 0zM29 18c0 6.075-4.925 11-11 11S7 24.075 7 18 11.925 7 18 7s11 4.925 11 11zm-12.083 2.996a4.977 4.977 0 0 1-1.325.513c-.46.105-.93.158-1.409.158-.51 0-.979-.086-1.408-.257-.43-.17-.801-.414-1.114-.729a3.46 3.46 0 0 1-.737-1.163A4.256 4.256 0 0 1 10.667 18c0-.559.086-1.064.257-1.518.178-.453.424-.838.737-1.153a3.163 3.163 0 0 1 1.114-.74c.43-.17.899-.256 1.408-.256.522 0 1 .07 1.436.207a2.73 2.73 0 0 1 1.16.69l-1.012 1.183a2.02 2.02 0 0 0-.663-.493c-.246-.111-.552-.167-.92-.167-.308 0-.587.056-.838.167-.246.112-.458.27-.636.473a2.092 2.092 0 0 0-.414.71 2.817 2.817 0 0 0-.138.897c0 .328.046.63.138.907.098.27.236.506.414.71.178.197.39.351.635.463.252.111.531.167.838.167.313 0 .583-.036.81-.108.227-.079.409-.158.543-.237V18.77h-1.169v-1.42h2.55v3.647zm1.246-6.485h1.436v6.978h-1.436v-6.978zm7.17 0h-4.372v6.978h1.436v-2.72h2.715v-1.42h-2.715V15.93h2.936v-1.42z"
            fill="currentColor"
          />
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.emoji-panel {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  z-index: var(--vp-im-z-controls);
  /* 纵向三段：分类条（表情页才有）/ 内容 / 底部页签。用 flex 而不是 grid，
     是因为 GIF 页要把分类条换成搜索框，grid 的行模板会留下一条空轨。 */
  display: flex;
  flex-direction: column;
  width: min(340px, calc(100vw - 40px));
  max-height: 300px;
  border-radius: 14px;
  border: 1px solid var(--vp-im-glass-border);
  background: rgba(18, 18, 32, 0.96);
  box-shadow: 0 18px 40px rgba(0, 0, 0, 0.45);
  overflow: hidden;
}
/* 分类条外层：给两头渐隐做定位锚点（渐隐不能放在滚动容器里，否则会跟着内容滚走）。 */
.tabs-wrap {
  position: relative;
  min-width: 0;
}
.tabs-edge {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 22px;
  pointer-events: none;
  background: linear-gradient(to right, rgba(18, 18, 32, 0.96), rgba(18, 18, 32, 0));
}
.tabs-edge.left { left: 0; }
.tabs-edge.right { right: 0; transform: scaleX(-1); }
.emoji-tabs {
  display: flex;
  gap: 2px;
  padding: 6px 6px 0;
  overflow-x: auto;
  /* 滚动条隐藏，横向滚动靠滚轮映射与触控板手势；渐隐负责「还有更多」的提示。 */
  scrollbar-width: none;
  overscroll-behavior-x: contain;
}
.emoji-tabs::-webkit-scrollbar { display: none; }
.emoji-tabs button {
  flex: 0 0 auto;
  padding: 6px 10px;
  border: 0;
  border-radius: 8px;
  background: transparent;
  color: var(--vp-im-text-secondary);
  font-size: 12px;
  cursor: pointer;
  white-space: nowrap;
}
.emoji-tabs button.on {
  background: rgba(255, 255, 255, 0.1);
  color: var(--vp-im-text-primary);
  font-weight: 600;
}
.emoji-grid {
  flex: 1;
  display: grid;
  grid-template-columns: repeat(8, 1fr);
  gap: 2px;
  padding: 8px;
  /* 网格轨在 flex/grid 里默认不能小于内容，加 min-height:0 才能被 footer 挤压后滚动。 */
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
}
.emoji-cell {
  display: grid;
  place-items: center;
  width: 100%;
  aspect-ratio: 1;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
}
.emoji-cell:hover { background: rgba(255, 255, 255, 0.1); }
.emoji-cell img { width: 26px; height: 26px; display: block; }

/* 底部类型选择器：左边「表情」是当前分类（品牌色），右边「GIF」先占位。
   GIF 功能未实现，所以做成 disabled + 「即将上线」，不做成点了没反应的按钮。 */
.emoji-foot {
  flex: none;
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 7px 12px 9px;
  border-top: 1px solid var(--vp-im-glass-border);
}
.kind {
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: none;
  color: var(--vp-im-text-secondary);
  cursor: pointer;
}
.kind.on {
  color: var(--vp-im-accent);
  cursor: default;
}
/* GIF 徽标自己画了圆环和字母，只给个默认的次级文字色；选中态由 .kind.on 接管。
   svg 用 32px：外圈环半径 11/36，渲染后圆环直径约 19.6px，与左边 26px 圆脸图标的 19.5px 等大。 */
/* GIF 页撑满中间那块，滚动交给它内部的列表。 */
.emoji-gif {
  flex: 1;
  min-height: 0;
}

/* 窄屏：不再是一张挂在表情按钮左边的 340px 卡片（会向左溢出屏幕、右侧留一大块空），
   而是横向铺满整屏、像键盘一样贴着两条边——这样 8 个分类也刚好不需要横向滚动。
   锚点由 CommentSheet 的 `.composer .pickers { position: static }` 切到 .composer 上。 */
@media (max-width: 560px) {
  .emoji-panel {
    left: 0;
    right: 0;
    width: auto;
  }
  .emoji-cell img { width: 28px; height: 28px; }
  /* 分类标签左右收 2px（10 → 8）：8 个两字分类在 360px 上正好排得下，不用滚动。 */
  .emoji-tabs button { padding: 6px 8px; }
}
</style>
