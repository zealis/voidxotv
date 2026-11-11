<script setup lang="ts">
// 顶部栏与底部导航（Page 1）。直播入口位为首期演进预留，按 §6.1 不渲染、不留空占位。
const props = withDefaults(defineProps<{ channel?: 'follow' | 'recommend'; showNav?: boolean }>(), {
  channel: 'recommend',
  showNav: true,
})
const emit = defineEmits<{
  'update:channel': [value: 'follow' | 'recommend']
}>()
</script>

<template>
  <header class="topbar vp-glass" :class="{ 'topbar--no-nav': !props.showNav }">
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

  <nav v-if="props.showNav" class="bottom-nav vp-glass" aria-label="主导航">
    <NuxtLink to="/" class="nav-item active" aria-label="首页">
      <ImIcon name="home" :size="22" /><span>首页</span>
    </NuxtLink>
    <NuxtLink to="/search" class="nav-item" aria-label="发现">
      <ImIcon name="discover" :size="22" /><span>发现</span>
    </NuxtLink>
    <NuxtLink to="/publish" class="nav-publish" aria-label="发布作品">
      <ImIcon name="plus" :size="26" />
    </NuxtLink>
    <NuxtLink to="/messages" class="nav-item" aria-label="消息">
      <ImIcon name="message" :size="22" /><span>消息</span>
    </NuxtLink>
    <NuxtLink to="/me" class="nav-item" aria-label="我的">
      <ImIcon name="user" :size="22" /><span>我的</span>
    </NuxtLink>
  </nav>
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
.bottom-nav {
  position: fixed;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: var(--vp-im-z-controls);
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  align-items: center;
  padding: 8px 8px calc(8px + env(safe-area-inset-bottom));
  border-width: 1px 0 0;
  border-radius: 0;
}
.nav-item {
  display: grid;
  justify-items: center;
  gap: 2px;
  min-height: 44px;
  color: var(--vp-im-text-secondary);
  font-size: 11px;
  text-decoration: none;
}
.nav-item.active { color: var(--vp-im-text-primary); }
.nav-publish {
  display: grid;
  place-items: center;
  width: 46px;
  height: 34px;
  margin: 0 auto;
  border-radius: 11px;
  color: var(--vp-im-on-accent);
  background: linear-gradient(120deg, var(--vp-im-accent), #a03bff);
  box-shadow: 0 6px 18px rgba(255, 46, 126, 0.35);
}
</style>
