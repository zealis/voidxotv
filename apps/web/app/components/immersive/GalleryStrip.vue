<script setup lang="ts">
// 图集条目（UI 规范 §5.4）：横向 scroll-snap，不与上下切条手势冲突；
// 底部圆点 / 序号明确；不自动轮播；预载当前张前后各一张。
import { computed, ref, watch } from 'vue'

// index 可选受控：图片控制条的自动轮播由父级驱动，手动滑动仍以本组件为准。
const props = defineProps<{ images: string[]; alt?: string; index?: number }>()
const emit = defineEmits<{ change: [index: number] }>()
const scroller = ref<HTMLElement | null>(null)
const active = ref(0)

const preload = computed(() => {
  const result = new Set<string>()
  for (const index of [active.value - 1, active.value, active.value + 1]) {
    const url = props.images[index]
    if (url) result.add(url)
  }
  return [...result]
})

watch(preload, (urls) => {
  if (!import.meta.client) return
  for (const url of urls) {
    const image = new Image()
    image.src = url
  }
}, { immediate: true })

function onScroll() {
  const element = scroller.value
  if (!element) return
  const index = Math.round(element.scrollLeft / Math.max(element.clientWidth, 1))
  active.value = Math.min(Math.max(index, 0), Math.max(props.images.length - 1, 0))
  emit('change', active.value)
}

function go(index: number) {
  const element = scroller.value
  const target = Math.min(Math.max(index, 0), Math.max(props.images.length - 1, 0))
  if (!element || target === active.value) return
  element.scrollTo({ left: target * element.clientWidth, behavior: 'smooth' })
  active.value = target
  emit('change', active.value)
}

// 父级（图片控制条的自动轮播）改 index 时跟随；值相同则为自身滚动的回声，忽略。
watch(() => props.index, (value) => {
  if (typeof value === 'number') go(value)
})
</script>

<template>
  <div class="gallery">
    <div ref="scroller" class="strip" @scroll.passive="onScroll">
      <div v-for="(url, index) in props.images" :key="url" class="slide">
        <img
          :src="url" :alt="`${props.alt || '图集作品'} 第 ${index + 1} 张，共 ${props.images.length} 张`"
          :loading="Math.abs(index - active) > 1 ? 'lazy' : 'eager'"
        />
      </div>
    </div>
    <button
      v-if="active > 0" type="button" class="arrow left" aria-label="上一张"
      @click.stop="go(active - 1)"
    ><ImIcon name="back" :size="20" /></button>
    <button
      v-if="active < props.images.length - 1" type="button" class="arrow right" aria-label="下一张"
      @click.stop="go(active + 1)"
    ><ImIcon name="back" :size="20" /></button>
  </div>
</template>

<style scoped>
.gallery { position: absolute; inset: 0; overflow: hidden; background: var(--vp-im-bg); }
.strip {
  display: flex;
  width: 100%;
  height: 100%;
  overflow-x: auto;
  overflow-y: hidden;
  scroll-snap-type: x mandatory;
  scrollbar-width: none;
  overscroll-behavior-x: contain;
}
.strip::-webkit-scrollbar { display: none; }
.slide {
  flex: 0 0 100%;
  height: 100%;
  scroll-snap-align: start;
  display: grid;
  place-items: center;
}
.slide img { max-width: 100%; max-height: 100%; object-fit: contain; }
.arrow {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  width: 44px;
  height: 44px;
  display: grid;
  place-items: center;
  border: 0;
  border-radius: 50%;
  color: #fff;
  background: rgba(10, 10, 20, 0.55);
  cursor: pointer;
}
.arrow.left { left: 8px; }
.arrow.right { right: 8px; rotate: 180deg; }
</style>
