<script setup lang="ts">
// 评论表情面板：Twemoji 图形，按分类分页。选中后由父组件插入输入框。
// 图形全部是同源 SVG（public/emoji），尺寸固定 24px，避免不同表情视觉大小不一。
const emit = defineEmits<{ pick: [value: string] }>()

const active = ref(emojiCategories[0]?.key || '')
const current = computed(
  () => emojiCategories.find((category) => category.key === active.value) || emojiCategories[0],
)
</script>

<template>
  <div class="emoji-panel" role="dialog" aria-label="选择表情">
    <div class="emoji-tabs" role="tablist" aria-label="表情分类">
      <button
        v-for="category in emojiCategories" :key="category.key"
        type="button" role="tab" :aria-selected="active === category.key"
        :class="{ on: active === category.key }"
        @click="active = category.key"
      >{{ category.label }}</button>
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
  </div>
</template>

<style scoped>
.emoji-panel {
  position: absolute;
  right: 0;
  bottom: calc(100% + 8px);
  z-index: var(--vp-im-z-controls);
  display: grid;
  grid-template-rows: auto 1fr;
  width: min(340px, calc(100vw - 40px));
  max-height: 264px;
  border-radius: 14px;
  border: 1px solid var(--vp-im-glass-border);
  background: rgba(18, 18, 32, 0.96);
  box-shadow: 0 18px 40px rgba(0, 0, 0, 0.45);
  overflow: hidden;
}
.emoji-tabs {
  display: flex;
  gap: 2px;
  padding: 6px 6px 0;
  overflow-x: auto;
  scrollbar-width: none;
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
.emoji-cell img { width: 24px; height: 24px; display: block; }
</style>
