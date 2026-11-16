<script setup lang="ts">
// 沉浸流顶部栏（关注 / 推荐）+ 底部主导航（Page 1）。
// 底栏本体在 BottomNav.vue，普通工具页也复用它；这里只负责顶部的频道切换。
// 直播入口位为首期演进预留，按 §6.1 不渲染、不留空占位。
const props = withDefaults(defineProps<{
  channel?: 'follow' | 'recommend'
  showNav?: boolean
  // 底栏是否收起：视频播放中、或图片浏览态（点画面可唤出）。
  // 只在 PC 端（≥781px）生效，移动端始终常驻。
  collapsed?: boolean
}>(), {
  channel: 'recommend',
  showNav: true,
  collapsed: false,
})
const emit = defineEmits<{
  'update:channel': [value: 'follow' | 'recommend']
}>()
</script>

<template>
  <header
    class="topbar vp-glass"
    :class="{ 'topbar--no-nav': !props.showNav, 'topbar--auto-hide': props.collapsed }"
  >
    <div class="channels" role="tablist" aria-label="内容频道">
      <button
        type="button" role="tab" :aria-selected="props.channel === 'follow'"
        :class="{ active: props.channel === 'follow' }"
        @click="emit('update:channel', 'follow')"
      >
        关注<span v-if="props.channel === 'follow'" class="indicator" />
      </button>
      <button
        type="button" role="tab" :aria-selected="props.channel === 'recommend'"
        :class="{ active: props.channel === 'recommend' }"
        @click="emit('update:channel', 'recommend')"
      >
        推荐<span v-if="props.channel === 'recommend'" class="indicator" />
      </button>
    </div>
    <NuxtLink to="/search" class="icon-btn" aria-label="搜索">
      <ImIcon name="search" :size="22" />
    </NuxtLink>
  </header>

  <BottomNav v-if="props.showNav" :collapsed="props.collapsed" />
</template>

<style scoped>
.topbar {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: var(--vp-im-z-topbar);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: calc(10px + env(safe-area-inset-top)) 16px 10px;
  border-width: 0 0 1px;
  border-radius: 0;
}
.channels { display: flex; align-items: center; gap: 20px; }
.channels button {
  position: relative;
  padding: 6px 0 8px;
  border: 0;
  background: none;
  color: var(--vp-im-text-secondary);
  font-size: 15px;
  font-weight: 400;
  cursor: pointer;
}
.channels button.active { color: var(--vp-im-text-primary); font-weight: 700; }
.indicator {
  position: absolute;
  left: 50%;
  bottom: 0;
  width: 18px;
  height: 3px;
  border-radius: 2px;
  transform: translateX(-50%);
  background: var(--vp-im-accent);
}
.icon-btn {
  display: grid;
  place-items: center;
  width: 44px;
  height: 44px;
  color: var(--vp-im-text-primary);
  text-decoration: none;
}
/* PC 端：与底栏同一套收起逻辑——视频播放中 / 图片浏览态顶栏一起退场，
   暂停（视频）或点画面（图片）才回来。移动端（≤780px）不匹配这条规则，顶栏常驻。
   collapsed 的语义由页面层算好（index.vue / ImmersiveDetail.vue 的 chromeCollapsed）。 */
@media (min-width: 781px) {
  .topbar { transition: opacity 200ms ease, transform 200ms ease, visibility 0s; }
  .topbar--auto-hide {
    opacity: 0;
    transform: translateY(-100%);
    visibility: hidden;
    pointer-events: none;
    transition: opacity 200ms ease, transform 200ms ease, visibility 0s linear 200ms;
  }
}
</style>
