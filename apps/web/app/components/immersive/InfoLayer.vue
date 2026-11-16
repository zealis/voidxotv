<script setup lang="ts">
// 底部信息层（UI 规范 §6.3）：作者、文案（默认两行）、话题、音乐条胶囊。
// 文案不放在玻璃面板上，可读性由画面底部渐变遮罩保证。
const props = defineProps<{
  /** 作者显示名：默认昵称「用户 + 夜街号」，后台可改。 */
  author: string
  caption: string
  tags: string[]
  expanded: boolean
  poi?: { name: string; address?: string } | null
  /** 图片没有弹幕，「弹」入口只在视频里出现。 */
  danmaku?: boolean
  /** 发布时间（ISO 串）：作者名右侧显示「· 3天前 / 11月8日」。 */
  publishedAt?: string
  /** 去中心化（external_direct）：作者名右侧展示来源标识（不落存储，按用户链接播放）。 */
  decentralized?: boolean
}>()
const emit = defineEmits<{ 'toggle-expand': []; danmaku: [] }>()

const timeLabel = computed(() => publishedLabel(props.publishedAt))
</script>

<template>
  <section class="info" aria-label="作品信息">
    <!-- 移动端没有播放控制条，「弹」入口放在作者名上方（§6.3）。 -->
    <button
      v-if="props.danmaku !== false" type="button" class="danmaku-entry"
      aria-label="发弹幕" @click="emit('danmaku')"
    >弹</button>
    <div class="author">
      <span class="name">{{ props.author }}</span>
      <!-- 去中心化：视频来自用户提供的在线地址（不落存储），图标仅为来源标识，
           悬浮说明播放方式。 -->
      <span
        v-if="props.decentralized" class="decentralized"
        title="视频系用户链接展示播放，本网未存储此视频。"
      >
        <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1024 1024" width="1em" height="1em" aria-hidden="true"><path fill="#39FF14" d="M512 32c-265.094 0-480 214.925-480 480s214.906 480 480 480 480-214.925 480-480-214.906-480-480-480zM512 944.998c-239.146 0-432.998-193.853-432.998-432.998s193.853-432.998 432.998-432.998 432.998 193.853 432.998 432.998-193.853 432.998-432.998 432.998zM773.581 530.605l0.154-8.871h160.234c0.211-3.206 0.413-6.442 0.413-9.725s-0.202-6.48-0.413-9.725h-160.234l-0.154-8.832c-1.277-69.216-9.619-135.322-24.826-196.435l-1.977-8.064 7.814-2.659c38.314-13.085 67.277-27.197 86.726-38.045-4.205-5.203-8.525-10.301-12.941-15.322-23.203 12.729-49.411 24.115-77.971 34.032l-8.851 3.043-2.736-8.957c-16.128-53.049-37.037-99.36-62.237-137.597-11.289-4.819-22.857-9.101-34.579-12.931 31.277 39.859 57.706 93.341 77.232 156.652l2.736 8.871-8.957 2.468c-55.632 15.36-116.88 23.846-182.074 25.277l-9.245 0.192v-213.908c-3.188-0.231-6.412-0.461-9.696-0.461s-6.489 0.231-9.715 0.461v213.917l-9.245-0.192c-65.002-1.421-126.24-9.994-182.026-25.421l-8.966-2.468 2.736-8.88c19.546-63.283 45.974-116.678 77.194-156.461-11.741 3.792-23.289 8.064-34.598 12.892-25.143 38.16-46.032 84.384-62.16 137.405l-2.755 8.995-8.842-3.091c-28.474-9.869-54.643-21.293-77.923-33.994-4.454 5.088-8.823 10.224-13.037 15.475 19.44 10.838 48.413 24.96 86.717 38.045l7.814 2.659-1.968 8.064c-15.188 61.075-23.549 127.181-24.845 196.435l-0.173 8.832h-160.176c-0.221 3.235-0.413 6.432-0.413 9.715s0.202 6.518 0.413 9.725h160.205l0.173 8.871c1.315 69.869 9.802 136.551 25.219 198.134l2.045 8.064-7.872 2.659c-37.872 12.922-66.682 26.851-86.054 37.584 4.205 5.212 8.544 10.301 12.979 15.322 23.059-12.499 49.075-23.808 77.395-33.6l8.842-3.052 2.717 8.909c16.080 52.397 36.816 98.131 61.728 135.936 11.309 4.781 22.857 9.101 34.608 12.892-30.989-39.437-57.216-92.294-76.685-154.886l-2.794-8.909 9.014-2.468c55.459-15.235 116.534-23.692 181.497-25.123l9.245-0.192v212.103c3.226 0.202 6.432 0.394 9.715 0.394s6.509-0.192 9.715-0.394v-212.103l9.245 0.192c64.762 1.421 125.808 9.917 181.459 25.277l8.976 2.468-2.774 8.909c-19.469 62.506-45.706 115.334-76.617 154.733 11.741-3.792 23.309-8.112 34.608-12.892 24.874-37.805 45.609-83.463 61.671-135.744l2.736-8.957 8.842 3.082c28.253 9.763 54.211 21.024 77.299 33.532 4.522-5.049 8.88-10.186 13.094-15.428-19.354-10.732-48.125-24.653-86.035-37.584l-7.863-2.659 2.016-8.064c15.417-61.574 23.904-128.256 25.2-198.125zM502.285 702.416l-8.851 0.202c-67.181 1.383-130.195 10.023-187.306 25.651l-8.889 2.468-2.246-8.909c-15.331-60.384-23.779-124.589-25.143-190.877l-0.173-9.216h232.608v180.682zM502.285 502.275h-232.608l0.173-9.216c1.373-65.664 9.677-129.293 24.691-189.149l2.246-8.909 8.889 2.429c57.254 15.783 120.423 24.471 187.766 25.814l8.851 0.231v178.8zM521.715 323.475l8.851-0.231c67.344-1.354 130.512-10.032 187.766-25.814l8.889-2.429 2.246 8.909c15.024 59.846 23.337 123.475 24.663 189.149l0.202 9.216h-232.608v-178.8zM729.008 721.828l-2.266 8.909-8.871-2.468c-57.111-15.629-120.125-24.269-187.306-25.661l-8.851-0.202v-180.682h232.608l-0.202 9.216c-1.363 66.326-9.802 130.579-25.114 190.886z"/></svg>
      </span>
      <template v-if="timeLabel">
        <span class="sep" aria-hidden="true">·</span>
        <time class="time" :datetime="props.publishedAt">{{ timeLabel }}</time>
      </template>
    </div>
    <p class="caption" :class="{ expanded: props.expanded }">{{ props.caption || '暂无文案' }}</p>
    <button v-if="props.caption.length > 42" type="button" class="expand" @click="emit('toggle-expand')">
      {{ props.expanded ? '收起' : '展开' }}
    </button>
    <!-- 位置（§8 发布设置）：发布时添加了 POI 的作品在信息层展示。 -->
    <p v-if="props.poi?.name" class="poi">📍 {{ props.poi.name }}</p>
    <div v-if="props.tags.length" class="tags">
      <span v-for="tag in props.tags" :key="tag">{{ tag }}</span>
    </div>
  </section>
</template>

<style scoped>
.info {
  position: absolute;
  left: 0;
  right: 84px;
  /* 有底栏 126px、底栏收起 65px（由 ImmersiveItem 按状态下发），过渡与控制条同步。 */
  bottom: calc(var(--vp-im-info-offset-bottom, 126px) + env(safe-area-inset-bottom));
  transition: bottom 200ms ease;
  /* 高于播放控制条一层（同为 controls 层级时后写的条会盖住作者行）：
     否则去中心化图标的悬浮与点击会被控制条的盒子挡住。 */
  z-index: calc(var(--vp-im-z-controls) + 1);
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
.author {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 6px;
  margin-bottom: 6px;
  font-size: 16px;
  font-weight: 600;
}
/* 去中心化图标：绿色 (#39FF14) 全球网络标志随 SVG 自带，轻投影保证亮图可读。 */
.decentralized {
  display: inline-flex;
  /* svg 的基线在底边，跟随 baseline 会整体下沉；改为与作者行垂直居中。 */
  align-items: center;
  align-self: center;
  /* .info 整层是点击穿透的（pointer-events: none），交互子元素必须单独放开，
     否则悬浮说明与前往源站的点击都会穿到画面上。 */
  pointer-events: auto;
  text-decoration: none;
}
.decentralized svg { filter: drop-shadow(0 1px 2px rgba(10, 10, 31, 0.45)); }
/* 时间比作者名弱一档：同一行里先看到人，再看到多久以前。 */
.author .sep { color: rgba(255, 255, 255, 0.5); }
.author .time { font-size: 14px; font-weight: 400; color: rgba(255, 255, 255, 0.75); }
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
</style>
