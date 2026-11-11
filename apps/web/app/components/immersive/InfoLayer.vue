<script setup lang="ts">
// 底部信息层（UI 规范 §6.3）：作者、文案（默认两行）、话题、音乐条胶囊。
// 文案不放在玻璃面板上，可读性由画面底部渐变遮罩保证。
const props = defineProps<{
  handle: string
  caption: string
  tags: string[]
  expanded: boolean
  poi?: { name: string; address?: string } | null
  /** 图片没有弹幕，「弹」入口只在视频里出现。 */
  danmaku?: boolean
}>()
const emit = defineEmits<{ 'toggle-expand': []; danmaku: [] }>()
</script>

<template>
  <section class="info" aria-label="作品信息">
    <!-- 移动端没有播放控制条，「弹」入口放在作者名上方（§6.3）。 -->
    <button
      v-if="props.danmaku !== false" type="button" class="danmaku-entry"
      aria-label="发弹幕" @click="emit('danmaku')"
    >弹</button>
    <div class="author">@{{ props.handle }}</div>
    <p class="caption" :class="{ expanded: props.expanded }">{{ props.caption || '暂无文案' }}</p>
    <button v-if="props.caption.length > 42" type="button" class="expand" @click="emit('toggle-expand')">
      {{ props.expanded ? '收起' : '展开' }}
    </button>
    <!-- 位置（§8 发布设置）：发布时添加了 POI 的作品在信息层展示。 -->
    <p v-if="props.poi?.name" class="poi">📍 {{ props.poi.name }}</p>
    <div v-if="props.tags.length" class="tags">
      <span v-for="tag in props.tags" :key="tag">{{ tag }}</span>
    </div>
    <div class="music vp-glass vp-glass-compact" :aria-label="`背景音乐：${props.handle} 的原声`">
      <ImIcon name="music" :size="16" />
      <span class="music-name">{{ props.handle }} · 原声</span>
    </div>
  </section>
</template>

<style scoped>
.info {
  position: absolute;
  left: 0;
  right: 84px;
  bottom: calc(126px + env(safe-area-inset-bottom));
  z-index: var(--vp-im-z-controls);
  padding: 0 16px;
  pointer-events: none;
}
.danmaku-entry {
  display: none;
  place-items: center;
  width: 34px;
  height: 34px;
  margin-bottom: 10px;
  padding: 0;
  border: 0;
  border-radius: 8px;
  background: rgba(10, 10, 20, 0.62);
  color: var(--vp-im-text-primary);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
  pointer-events: auto;
  -webkit-backdrop-filter: blur(8px);
  backdrop-filter: blur(8px);
}
/* 只在移动端出现：宽屏用播放控制条里的输入框。 */
@media (max-width: 780px) {
  .danmaku-entry { display: grid; }
  /* 移动端没有控制条，信息层回到底栏上方，不空出一整条。 */
  .info { bottom: calc(74px + env(safe-area-inset-bottom)); }
}
.author { font-size: 16px; font-weight: 600; margin-bottom: 6px; }
.caption {
  margin: 0;
  font-size: 14px;
  line-height: 1.55;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  max-height: 3.1em;
}
.caption.expanded {
  display: block;
  max-height: 30vh;
  overflow-y: auto;
  pointer-events: auto;
}
.expand {
  margin-top: 2px;
  padding: 4px 0;
  border: 0;
  background: none;
  color: var(--vp-im-text-secondary);
  font-size: 12px;
  cursor: pointer;
  pointer-events: auto;
}
.tags { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 6px; font-size: 13px; color: #d8b7ff; }
.poi {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--vp-im-text-primary);
  text-shadow: 0 1px 3px rgba(10, 10, 31, 0.5);
}
.music {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 230px;
  margin-top: 10px;
  padding: 7px 14px;
  border-radius: 999px;
  font-size: 12px;
}
.music-name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
</style>
