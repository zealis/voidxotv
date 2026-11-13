<script setup lang="ts">
// 评论表情面板：Google Noto 3D 表情，按分类分页。选中后由父组件插入输入框。
// 图形全部是同源 PNG（public/emoji3d，由 scripts/sync-emoji3d.mjs 导出），
// 尺寸固定 26px，避免不同表情视觉大小不一；3D 渲染比线性图标需要更大的显示尺寸。
const emit = defineEmits<{ pick: [value: string] }>()

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
  <div class="emoji-panel" role="dialog" aria-label="选择表情">
    <div class="tabs-wrap">
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
    <div class="emoji-grid">
      <button
        v-for="item in current?.items || []" :key="item"
        type="button" class="emoji-cell" :aria-label="`插入表情 ${item}`"
        :title="item" @click="emit('pick', item)"
      >
        <img :src="emojiSrc(item) || ''" :alt="item" loading="lazy" />
      </button>
    </div>
    <!-- 底部类型选择器：当前只有「表情」可用，GIF 先占位（与 @ 提及入口同一套处理）。 -->
    <div class="emoji-foot">
      <button
        type="button" class="kind on" :aria-pressed="true" aria-label="表情" title="表情"
      >
        <ImIcon name="face" :size="26" />
      </button>
      <button
        type="button" class="kind gif" disabled
        title="GIF 即将上线" aria-label="GIF（即将上线）"
      >
        GIF
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
  display: grid;
  /* 分类条 / 表情栅格 / 底部类型选择器 */
  grid-template-rows: auto 1fr auto;
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
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 7px 12px 9px;
  border-top: 1px solid var(--vp-im-glass-border);
}
.kind {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
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
/* GIF 徽标：描边圆 + 三个字母，外框尺寸与左边的圆脸图标一致。 */
.kind.gif {
  border: 2px solid currentColor;
  border-radius: 50%;
  font-size: 9px;
  font-weight: 700;
  line-height: 1;
  letter-spacing: 0.02em;
}
.kind.gif:disabled {
  cursor: not-allowed;
  opacity: 0.6;
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
